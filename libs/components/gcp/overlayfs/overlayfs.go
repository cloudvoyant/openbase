// Package overlayfs provides zero-copy Google Cloud Storage reads across a primary and fallback bucket.
package overlayfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"cloud.google.com/go/storage"
)

// Public API -----------------------------------------------------------------

// ErrPrimaryBucketRequired reports a missing primary bucket configuration.
var ErrPrimaryBucketRequired = errors.New("overlayfs: primary bucket name is required")

// Backend abstracts GCS object operations for production clients and offline unit tests.
type Backend interface {
	ReadObject(ctx context.Context, bucket, key string) ([]byte, error)
	WriteObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	DeleteObject(ctx context.Context, bucket, key string) error
}

// Store reads from PrimaryBucket with automatic fallback to FallbackBucket, writing only to PrimaryBucket.
type Store struct {
	PrimaryBucket  string
	FallbackBucket string
	backend        Backend
}

// NewStore constructs an overlay Store backed by a live *storage.Client.
func NewStore(client *storage.Client, primaryBucket, fallbackBucket string) (*Store, error) {
	return NewStoreWithBackend(&gcsBackend{client: client}, primaryBucket, fallbackBucket)
}

// NewStoreWithBackend constructs an overlay Store over any Backend implementation.
func NewStoreWithBackend(backend Backend, primaryBucket, fallbackBucket string) (*Store, error) {
	if strings.TrimSpace(primaryBucket) == "" {
		return nil, ErrPrimaryBucketRequired
	}
	if backend == nil {
		return nil, errors.New("overlayfs: backend must not be nil")
	}
	return &Store{
		PrimaryBucket:  primaryBucket,
		FallbackBucket: fallbackBucket,
		backend:        backend,
	}, nil
}

// Read returns (bytes, sourceBucket, nil), falling back to FallbackBucket on storage.ErrObjectNotExist.
func (s *Store) Read(ctx context.Context, key string) ([]byte, string, error) {
	data, err := s.backend.ReadObject(ctx, s.PrimaryBucket, key)
	if err == nil {
		return data, s.PrimaryBucket, nil
	}
	if !errors.Is(err, storage.ErrObjectNotExist) || s.FallbackBucket == "" {
		return nil, "", err
	}
	fallbackData, fallbackErr := s.backend.ReadObject(ctx, s.FallbackBucket, key)
	if fallbackErr != nil {
		return nil, "", fallbackErr
	}
	return fallbackData, s.FallbackBucket, nil
}

// Write stores data exclusively in PrimaryBucket without modifying FallbackBucket.
func (s *Store) Write(ctx context.Context, key string, data []byte, contentType string) error {
	return s.backend.WriteObject(ctx, s.PrimaryBucket, key, data, contentType)
}

// Delete removes key exclusively from PrimaryBucket without deleting objects in FallbackBucket.
func (s *Store) Delete(ctx context.Context, key string) error {
	return s.backend.DeleteObject(ctx, s.PrimaryBucket, key)
}

// Private helpers ------------------------------------------------------------

type gcsBackend struct {
	client *storage.Client
}

func (g *gcsBackend) ReadObject(ctx context.Context, bucket, key string) ([]byte, error) {
	rc, err := g.client.Bucket(bucket).Object(key).NewReader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("overlayfs: read %s/%s: %w", bucket, key, err)
	}
	return data, nil
}

func (g *gcsBackend) WriteObject(ctx context.Context, bucket, key string, data []byte, contentType string) error {
	wc := g.client.Bucket(bucket).Object(key).NewWriter(ctx)
	if contentType != "" {
		wc.ContentType = contentType
	}
	if _, err := wc.Write(data); err != nil {
		_ = wc.Close()
		return fmt.Errorf("overlayfs: write %s/%s: %w", bucket, key, err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("overlayfs: close %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (g *gcsBackend) DeleteObject(ctx context.Context, bucket, key string) error {
	return g.client.Bucket(bucket).Object(key).Delete(ctx)
}
