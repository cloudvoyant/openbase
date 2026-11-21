package overlayfs_test

import (
	"context"
	"errors"
	"testing"

	"cloud.google.com/go/storage"

	"github.com/cloudvoyant/openbase/libs/components/gcp/overlayfs"
)

// Public API -----------------------------------------------------------------

func TestOverlayStore(t *testing.T) {
	t.Run("given an overlayfs.Store backed by primary preview-bkt and fallback dev-bkt", func(t *testing.T) {
		t.Run("when reading seeded fallback objects and writing overrides, then fallback stays untouched while primary serves overrides", func(t *testing.T) {
			// Arrange.
			ctx := context.Background()
			mem := newMemoryBackend()
			_ = mem.WriteObject(ctx, "dev-bkt", "seed.txt", []byte("from-dev"), "text/plain")
			store, err := overlayfs.NewStoreWithBackend(mem, "preview-bkt", "dev-bkt")
			requireOverlayNoErr(t, err)

			// Act.
			seedBytes, seedSource, errSeed := store.Read(ctx, "seed.txt")
			errWrite := store.Write(ctx, "seed.txt", []byte("from-preview"), "text/plain")
			overrideBytes, overrideSource, errOverride := store.Read(ctx, "seed.txt")
			devAfterBytes, errDevAfter := mem.ReadObject(ctx, "dev-bkt", "seed.txt")

			// Assert.
			requireOverlayNoErr(t, errSeed)
			requireOverlayNoErr(t, errWrite)
			requireOverlayNoErr(t, errOverride)
			requireOverlayNoErr(t, errDevAfter)
			assertOverlayStr(t, "fallback source", seedSource, "dev-bkt")
			assertOverlayStr(t, "fallback payload", string(seedBytes), "from-dev")
			assertOverlayStr(t, "override source", overrideSource, "preview-bkt")
			assertOverlayStr(t, "override payload", string(overrideBytes), "from-preview")
			assertOverlayStr(t, "dev untouched", string(devAfterBytes), "from-dev")
		})

		t.Run("when deleting a primary override or reading a missing key, then fallback resurfaces and missing keys return ErrObjectNotExist", func(t *testing.T) {
			// Arrange.
			ctx := context.Background()
			mem := newMemoryBackend()
			_ = mem.WriteObject(ctx, "dev-bkt", "keep.txt", []byte("dev-keep"), "text/plain")
			_ = mem.WriteObject(ctx, "preview-bkt", "keep.txt", []byte("preview-temp"), "text/plain")
			store, err := overlayfs.NewStoreWithBackend(mem, "preview-bkt", "dev-bkt")
			requireOverlayNoErr(t, err)

			// Act.
			errDel := store.Delete(ctx, "keep.txt")
			restored, source, errRead := store.Read(ctx, "keep.txt")
			_, _, errMissing := store.Read(ctx, "does-not-exist.txt")
			_, errEmptyPrimary := overlayfs.NewStoreWithBackend(mem, "", "dev-bkt")

			// Assert.
			requireOverlayNoErr(t, errDel)
			requireOverlayNoErr(t, errRead)
			assertOverlayStr(t, "restored source", source, "dev-bkt")
			assertOverlayStr(t, "restored payload", string(restored), "dev-keep")
			if !errors.Is(errMissing, storage.ErrObjectNotExist) {
				t.Fatalf("expected storage.ErrObjectNotExist, got %v", errMissing)
			}
			if !errors.Is(errEmptyPrimary, overlayfs.ErrPrimaryBucketRequired) {
				t.Fatalf("expected ErrPrimaryBucketRequired, got %v", errEmptyPrimary)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

type memoryBackend struct {
	buckets map[string]map[string][]byte
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{buckets: map[string]map[string][]byte{}}
}

func (m *memoryBackend) ReadObject(_ context.Context, bucket, key string) ([]byte, error) {
	if objs, ok := m.buckets[bucket]; ok {
		if b, exists := objs[key]; exists {
			return append([]byte(nil), b...), nil
		}
	}
	return nil, storage.ErrObjectNotExist
}

func (m *memoryBackend) WriteObject(_ context.Context, bucket, key string, data []byte, _ string) error {
	if m.buckets[bucket] == nil {
		m.buckets[bucket] = map[string][]byte{}
	}
	m.buckets[bucket][key] = append([]byte(nil), data...)
	return nil
}

func (m *memoryBackend) DeleteObject(_ context.Context, bucket, key string) error {
	if objs, ok := m.buckets[bucket]; ok {
		delete(objs, key)
	}
	return nil
}

func requireOverlayNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertOverlayStr(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}
