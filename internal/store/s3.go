package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"

	"priceradar/internal/model"
)

const (
	// defaultMaxAttempts bounds the conditional-write retry loop. This is a
	// single-user tool with at most two writers (scheduled Lambda + on-demand
	// local trigger), so a small fixed bound is sufficient.
	defaultMaxAttempts = 5
	// defaultBaseBackoff is the first inter-attempt delay; it doubles each retry
	// (100ms, 200ms, 400ms, ...).
	defaultBaseBackoff = 100 * time.Millisecond
)

// s3API is the subset of the S3 client used here, so tests can inject a fake.
type s3API interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// S3Store is the deployed backend: a single S3 object, whole-object
// GetObject/PutObject guarded by an ETag conditional-write retry loop.
type S3Store struct {
	client      s3API
	bucket      string
	key         string
	maxAttempts int
	baseBackoff time.Duration
}

// NewS3Store returns an S3-backed store for bucket/key.
func NewS3Store(client s3API, bucket, key string) *S3Store {
	return &S3Store{
		client:      client,
		bucket:      bucket,
		key:         key,
		maxAttempts: defaultMaxAttempts,
		baseBackoff: defaultBaseBackoff,
	}
}

// get reads the current history object, returning it, its ETag, and whether an
// object existed. A missing key is not an error: it yields an empty history with
// existed=false (so the caller uses an If-None-Match "create" precondition).
func (s *S3Store) get(ctx context.Context) (hist History, etag string, existed bool, err error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key),
	})
	if err != nil {
		if isNotFound(err) {
			return History{}, "", false, nil
		}
		return nil, "", false, err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", false, err
	}
	hist = History{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &hist); err != nil {
			return nil, "", false, err
		}
		if hist == nil {
			hist = History{}
		}
	}
	if out.ETag != nil {
		etag = *out.ETag
	}
	return hist, etag, true, nil
}

// put writes the whole history object with a precondition tied to the ETag read:
// If-Match when an object existed, else If-None-Match "*" for the create race.
func (s *S3Store) put(ctx context.Context, hist History, etag string, existed bool) error {
	data, err := json.Marshal(hist)
	if err != nil {
		return err
	}
	in := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key),
		Body:   bytes.NewReader(data),
	}
	if existed {
		in.IfMatch = aws.String(etag)
	} else {
		in.IfNoneMatch = aws.String("*")
	}
	_, err = s.client.PutObject(ctx, in)
	return err
}

// Load returns the current history object.
func (s *S3Store) Load(ctx context.Context) (History, error) {
	hist, _, _, err := s.get(ctx)
	return hist, err
}

// Append merges additions into the current object and writes it back, retrying
// from a fresh read on a 412 Precondition Failed (a concurrent writer changed
// the object) up to maxAttempts, with short exponential backoff. On exhausted
// retries it returns an error rather than silently dropping the snapshot.
func (s *S3Store) Append(ctx context.Context, additions map[string][]model.Snapshot) (History, error) {
	var lastErr error
	for attempt := 0; attempt < s.maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := s.baseBackoff * time.Duration(1<<(attempt-1))
			if backoff > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(backoff):
				}
			}
		}
		// Re-read on every attempt so a retry applies the append to the current
		// state and uses the current ETag — never a blind overwrite with stale data.
		hist, etag, existed, err := s.get(ctx)
		if err != nil {
			return nil, err
		}
		mergeHistory(hist, additions)
		err = s.put(ctx, hist, etag, existed)
		if err == nil {
			return hist, nil
		}
		if isPreconditionFailed(err) {
			lastErr = err
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("store: conditional write failed after %d attempts: %w", s.maxAttempts, lastErr)
}

// isNotFound reports whether err is an S3 missing-object error.
func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}

// isPreconditionFailed reports whether err is an S3 412 Precondition Failed,
// signalling a concurrent write that invalidated the read ETag.
func isPreconditionFailed(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "PreconditionFailed" {
		return true
	}
	return false
}
