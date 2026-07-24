package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"

	"priceradar/internal/model"
)

// fakeS3 is an in-memory S3 double modeling the object semantics store.go relies
// on: NoSuchKey on a missing key, ETag on read, and If-Match/If-None-Match
// precondition checks (412 PreconditionFailed) on write.
type fakeS3 struct {
	exists  bool
	data    []byte
	etag    string
	version int

	getCalls int
	putCalls int

	// onConflict runs once, before the store's first PutObject precondition
	// check, to simulate a concurrent writer mutating the object mid-run.
	onConflict func(f *fakeS3)

	// alwaysReject makes every PutObject return 412 even when the precondition
	// matches, simulating perpetual contention (retries-exhausted case).
	alwaysReject bool
}

func precondErr() error {
	return &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "At least one of the preconditions you specified did not hold."}
}

func (f *fakeS3) bumpETag() {
	f.version++
	f.etag = fmt.Sprintf("%q", fmt.Sprintf("v%d", f.version))
}

func (f *fakeS3) GetObject(ctx context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.getCalls++
	if !f.exists {
		return nil, &types.NoSuchKey{}
	}
	etag := f.etag
	body := make([]byte, len(f.data))
	copy(body, f.data)
	return &s3.GetObjectOutput{
		Body: io.NopCloser(readerFor(body)),
		ETag: &etag,
	}, nil
}

func (f *fakeS3) PutObject(ctx context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putCalls++
	if f.onConflict != nil && f.putCalls == 1 {
		f.onConflict(f)
	}
	// Precondition check.
	if in.IfNoneMatch != nil {
		if f.exists {
			return nil, precondErr()
		}
	} else if in.IfMatch != nil {
		if !f.exists || *in.IfMatch != f.etag {
			return nil, precondErr()
		}
	}
	if f.alwaysReject {
		return nil, precondErr()
	}
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.data = body
	f.exists = true
	f.bumpETag()
	return &s3.PutObjectOutput{}, nil
}

func readerFor(b []byte) io.Reader { return &sliceReader{b: b} }

type sliceReader struct {
	b   []byte
	off int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

func newTestS3Store(client s3API) *S3Store {
	s := NewS3Store(client, "bucket", "price-history.json")
	s.baseBackoff = 0 // keep tests fast
	return s
}

func snap(price int) model.Snapshot {
	return model.Snapshot{Price: price, InStock: true, ObservedAt: time.Unix(int64(price), 0).UTC()}
}

// T6.1: missing key on first run yields an empty history, not an error.
func TestS3_MissingKeyFirstRunIsEmpty(t *testing.T) {
	f := &fakeS3{}
	s := newTestS3Store(f)

	h, err := s.Load(context.Background())
	if err != nil {
		t.Fatalf("Load on missing key: %v", err)
	}
	if len(h) != 0 {
		t.Fatalf("want empty history, got %v", h)
	}
}

// T6.1: append then reload round-trips through the mocked S3 client; T6.2: this
// is also the no-conflict single-writer path (one GetObject, one PutObject).
func TestS3_AppendThenReloadRoundTrip(t *testing.T) {
	f := &fakeS3{}
	s := newTestS3Store(f)
	ctx := context.Background()

	const url = "https://example.com/p1"
	if _, err := s.Append(ctx, map[string][]model.Snapshot{url: {snap(100)}}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if f.putCalls != 1 {
		t.Fatalf("no-conflict append should PutObject once, got %d", f.putCalls)
	}

	got, err := s.Load(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got[url]) != 1 || got[url][0].Price != 100 {
		t.Fatalf("round-trip mismatch: %+v", got[url])
	}
}

// T6.1: multiple appends to the same URL are additive (no dedup).
func TestS3_AppendIsAdditive(t *testing.T) {
	f := &fakeS3{}
	s := newTestS3Store(f)
	ctx := context.Background()
	const url = "https://example.com/p1"

	for _, p := range []int{100, 100, 90} {
		if _, err := s.Append(ctx, map[string][]model.Snapshot{url: {snap(p)}}); err != nil {
			t.Fatalf("append %d: %v", p, err)
		}
	}
	got, err := s.Load(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got[url]) != 3 {
		t.Fatalf("want 3 retained snapshots (incl. duplicate price), got %d", len(got[url]))
	}
}

// T6.2: a concurrent writer changes the object between this writer's read and
// write. The first PutObject gets 412; the retry re-reads the new state and both
// writers' snapshots end up present.
func TestS3_ConcurrentConflictRetrySucceeds(t *testing.T) {
	const url = "https://example.com/p1"

	// Seed the object with an existing snapshot so both writers append.
	f := &fakeS3{}
	f.data = []byte(`{"https://example.com/p1":[{"price":100,"discount_pct":0,"in_stock":true,"observed_at":"1970-01-01T00:01:40Z"}]}`)
	f.exists = true
	f.bumpETag() // "v1"

	// Simulate the other writer committing snap(90) just before our first Put,
	// which invalidates our If-Match on "v1".
	f.onConflict = func(f *fakeS3) {
		f.data = []byte(`{"https://example.com/p1":[{"price":100,"discount_pct":0,"in_stock":true,"observed_at":"1970-01-01T00:01:40Z"},{"price":90,"discount_pct":10,"in_stock":true,"observed_at":"1970-01-01T00:01:30Z"}]}`)
		f.bumpETag() // "v2" — our read ETag "v1" is now stale
	}

	s := newTestS3Store(f)
	ctx := context.Background()

	// Our writer appends snap(80).
	our := snap(80)
	our.DiscountPct = 20
	got, err := s.Append(ctx, map[string][]model.Snapshot{url: {our}})
	if err != nil {
		t.Fatalf("append with conflict: %v", err)
	}
	if f.putCalls != 2 {
		t.Fatalf("expected one 412 then a retry (2 PutObject calls), got %d", f.putCalls)
	}
	if f.getCalls < 2 {
		t.Fatalf("retry must re-GetObject, got %d GetObject calls", f.getCalls)
	}

	prices := map[int]bool{}
	for _, sn := range got[url] {
		prices[sn.Price] = true
	}
	for _, want := range []int{100, 90, 80} {
		if !prices[want] {
			t.Fatalf("expected both writers' snapshots present, missing price %d in %+v", want, got[url])
		}
	}
}

// T6.2: when conflicts never clear, retries are exhausted and Append returns an
// error rather than silently dropping the snapshot.
func TestS3_RetriesExhaustedReturnsError(t *testing.T) {
	f := &fakeS3{alwaysReject: true}
	s := newTestS3Store(f)
	ctx := context.Background()

	_, err := s.Append(ctx, map[string][]model.Snapshot{"https://example.com/p1": {snap(100)}})
	if err == nil {
		t.Fatal("expected error after exhausted retries, got nil")
	}
	if f.putCalls != s.maxAttempts {
		t.Fatalf("expected %d PutObject attempts, got %d", s.maxAttempts, f.putCalls)
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "PreconditionFailed" {
		t.Fatalf("error should wrap the precondition failure, got %v", err)
	}
}

// T6.3: with no S3 bucket configured, New selects the local-file backend and it
// round-trips through os.ReadFile/os.WriteFile.
func TestLocalBackend_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "price-history.local.json")
	st, err := New(context.Background(), Config{LocalPath: path})
	if err != nil {
		t.Fatalf("New (local): %v", err)
	}
	if _, ok := st.(*LocalStore); !ok {
		t.Fatalf("absent S3 config should select LocalStore, got %T", st)
	}
	ctx := context.Background()
	const url = "https://example.com/p1"

	// Missing file → empty, not error.
	h, err := st.Load(ctx)
	if err != nil {
		t.Fatalf("load missing local file: %v", err)
	}
	if len(h) != 0 {
		t.Fatalf("want empty, got %v", h)
	}

	if _, err := st.Append(ctx, map[string][]model.Snapshot{url: {snap(100)}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := st.Append(ctx, map[string][]model.Snapshot{url: {snap(90)}}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	got, err := st.Load(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got[url]) != 2 {
		t.Fatalf("local round-trip additive: want 2, got %d", len(got[url]))
	}
}
