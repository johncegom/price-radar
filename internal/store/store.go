// Package store reads and appends to the price history: a single JSON object,
// a map[product-URL][]Snapshot, marshaled with encoding/json.
//
// Two backends share the same shape and interface. Which one is live is driven
// purely by config presence (never by which trigger invoked the run): if an S3
// bucket is configured, the S3 backend is used (whole-object GetObject/PutObject
// guarded by an ETag conditional-write retry loop, see s3.go); otherwise a
// local-file backend (os.ReadFile/os.WriteFile) is used for pre-deployment,
// single-process on-demand runs, where no concurrency protection is needed.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"priceradar/internal/model"
)

// defaultLocalPath is the local-file backend's history file when none is set.
const defaultLocalPath = "price-history.local.json"

// History is the whole price-history object: append-only snapshots keyed by
// product URL. Shared by both backends.
type History map[string][]model.Snapshot

// Store is the read/append interface both backends implement.
type Store interface {
	// Load returns the current history (missing/empty → empty History, not error).
	Load(ctx context.Context) (History, error)
	// Append merges additions (per-URL snapshots) into the current history and
	// persists the whole object, returning the resulting history.
	Append(ctx context.Context, additions map[string][]model.Snapshot) (History, error)
}

// Config selects and configures a backend. An empty S3Bucket selects the
// local-file backend.
type Config struct {
	S3Bucket  string
	S3Key     string
	LocalPath string
}

// New builds the backend indicated by cfg. With no S3 bucket it returns a
// local-file store; otherwise it builds an S3 client from the default AWS
// credential chain and returns the S3 store.
func New(ctx context.Context, cfg Config) (Store, error) {
	if cfg.S3Bucket == "" {
		path := cfg.LocalPath
		if path == "" {
			path = defaultLocalPath
		}
		return NewLocalStore(path), nil
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsCfg)
	return NewS3Store(client, cfg.S3Bucket, cfg.S3Key), nil
}

// mergeHistory appends additions into h in place (additive, no dedup).
func mergeHistory(h History, additions map[string][]model.Snapshot) {
	for url, snaps := range additions {
		h[url] = append(h[url], snaps...)
	}
}

// LocalStore is the pre-deployment backend: a single JSON file, no concurrency
// protection (single machine, single process).
type LocalStore struct {
	path string
}

// NewLocalStore returns a local-file store backed by path.
func NewLocalStore(path string) *LocalStore {
	return &LocalStore{path: path}
}

func (s *LocalStore) read() (History, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return History{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return History{}, nil
	}
	var h History
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	if h == nil {
		h = History{}
	}
	return h, nil
}

// Load returns the current history from the local file.
func (s *LocalStore) Load(ctx context.Context) (History, error) {
	return s.read()
}

// Append merges additions into the local history file and writes it back whole.
func (s *LocalStore) Append(ctx context.Context, additions map[string][]model.Snapshot) (History, error) {
	h, err := s.read()
	if err != nil {
		return nil, err
	}
	mergeHistory(h, additions)
	data, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return nil, err
	}
	return h, nil
}
