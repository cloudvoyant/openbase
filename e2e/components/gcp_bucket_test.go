package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"

	"github.com/cloudvoyant/openbase/libs/components/gcp/overlayfs"
)

// Public API -----------------------------------------------------------------

func TestGCPBucket(t *testing.T) {
	gcpProject, baseBackend := RequireLiveGCP(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "obbkt" + runID
	h := NewLiveHarness(
		t,
		"gcp-bucket",
		"openbase-gcp-bucket-fixture",
		baseBackend,
		"OPENBASE_TEST_GCP_PROJECT="+gcpProject,
		"GOOGLE_PROJECT="+gcpProject,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
	)

	var devBucket, stageBucket, prodBucket, previewBucket string
	h.OnCleanupVerify(func(t *testing.T) {
		assertBucketsAbsent(t, devBucket, stageBucket, prodBucket, previewBucket)
	})

	t.Run("given a standalone gcp.Bucket consumer program", func(t *testing.T) {
		h.InitStack(t, "dev")

		t.Run("when previewing dev, then it plans gcp:storage:Bucket without creating cloud buckets", func(t *testing.T) {
			// Arrange.
			expectedDev := fmt.Sprintf("%s-%s-dev-uploads", gcpProject, appProject)

			// Act.
			previewOut := h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=a"}, "preview", "--stack", "dev")
			h.WriteArtifact(t, "dev-preview-plan.txt", previewOut)

			// Assert.
			assertContains(t, previewOut, "gcp:storage:Bucket")
			assertBucketAbsent(t, expectedDev)
		})

		t.Run("given provisioned dev, stage, and prod buckets", func(t *testing.T) {
			for _, stack := range []string{"stage", "prod"} {
				h.InitStack(t, stack)
			}
			for _, stack := range []string{"dev", "stage", "prod"} {
				h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=a"}, "up", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")
			}
			devBucket = h.Output(t, "dev", "bucketName")
			stageBucket = h.Output(t, "stage", "bucketName")
			prodBucket = h.Output(t, "prod", "bucketName")

			t.Run("when updating dev in place", func(t *testing.T) {
				// Arrange / Act.
				h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=b"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
				devAfterUpdate := h.Output(t, "dev", "bucketName")
				devMeta := describeGCSBucket(t, devBucket)
				h.WriteJSONArtifact(t, "dev-bucket-updated.json", devMeta)

				// Assert.
				t.Run("then it preserves the bucket identity and updates labels on GCS", func(t *testing.T) {
					assertEqualStr(t, "in-place dev bucket name", devAfterUpdate, devBucket)
					assertEqualStr(t, "updated revision label", devMeta.Labels["openbase-revision"], "b")
				})

				t.Run("then it persists stack state in the remote gs:// backend", func(t *testing.T) {
					assertRemoteStateExists(t, h.BackendURL, "dev")
				})
			})

			t.Run("when querying from a fresh checkout, then it reads exported outputs from gs:// state", func(t *testing.T) {
				// Arrange.
				freshDir := copyFixture(t, "gcp-bucket")
				freshEnv := []string{
					"PULUMI_HOME=" + t.TempDir(),
					"PULUMI_BACKEND_URL=" + h.BackendURL,
					"PULUMI_CONFIG_PASSPHRASE=",
					"PULUMI_SKIP_UPDATE_CHECK=true",
				}

				// Act.
				runCommandEnv(t, freshDir, freshEnv, "pulumi", "login", h.BackendURL)
				queried := strings.TrimSpace(runCommandEnv(t, freshDir, freshEnv, "pulumi", "stack", "output", "bucketName", "--stack", "dev"))

				// Assert.
				assertEqualStr(t, "fresh checkout bucketName", queried, devBucket)
			})

			t.Run("when destroying protected prod or stage, then Pulumi rejects the destroy and keeps buckets live", func(t *testing.T) {
				// Arrange.
				prodMeta := describeGCSBucket(t, prodBucket)
				stageMeta := describeGCSBucket(t, stageBucket)
				h.WriteJSONArtifact(t, "prod-bucket-protected.json", prodMeta)

				// Act.
				failProd := h.PulumiFail(t, nil, "destroy", "--stack", "prod", "--yes", "--non-interactive")
				failStage := h.PulumiFail(t, nil, "destroy", "--stack", "stage", "--yes", "--non-interactive")

				// Assert.
				assertProtectedBucketFields(t, prodBucket, prodMeta)
				assertProtectedBucketFields(t, stageBucket, stageMeta)
				assertContains(t, strings.ToLower(failProd), "protect")
				assertContains(t, strings.ToLower(failStage), "protect")
				_ = describeGCSBucket(t, prodBucket)
			})

			t.Run("when a subsequent apply fails, then it preserves the existing dev bucket", func(t *testing.T) {
				// Arrange.
				failEnv := []string{"OPENBASE_TEST_FAIL_INFRA=1"}

				// Act.
				failOut := h.PulumiFail(t, failEnv, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")

				// Assert.
				assertContains(t, failOut, "simulated infrastructure program failure")
				_ = describeGCSBucket(t, devBucket)
			})

			t.Run("given a seeded dev bucket and preview-pr-1 branched from dev", func(t *testing.T) {
				seedGCSObject(t, devBucket, "seed.txt", "hello-from-dev")
				h.InitStack(t, "preview-pr-1")
				h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=a"}, "up", "--stack", "preview-pr-1", "--yes", "--non-interactive", "--skip-preview")
				previewBucket = h.Output(t, "preview-pr-1", "bucketName")
				previewFallback := h.Output(t, "preview-pr-1", "fallbackBucketName")

				t.Run("when reading and writing through overlayfs", func(t *testing.T) {
					// Arrange / Act.
					report := exerciseOverlayFS(t, devBucket, previewBucket, previewFallback)
					h.WriteJSONArtifact(t, "overlayfs-isolation.json", report)

					// Assert.
					t.Run("then it falls back to dev on cache miss", func(t *testing.T) {
						assertEqualStr(t, "preview fallback target", previewFallback, devBucket)
						assertEqualStr(t, "fallback source", report.FallbackSource, devBucket)
						assertEqualStr(t, "fallback content", report.FallbackContent, "hello-from-dev")
					})

					t.Run("then it isolates preview delta and override writes from dev", func(t *testing.T) {
						assertEqualStr(t, "override source", report.OverrideSource, previewBucket)
						assertEqualStr(t, "override content", report.OverrideContent, "preview-override")
						assertEqualStr(t, "dev seed preserved", report.DevSeedAfter, "hello-from-dev")
						if !report.DeltaAbsentInDev {
							t.Fatal("expected delta.txt to remain absent from dev bucket")
						}
					})
				})

				t.Run("when destroying preview-pr-1 twice, then it deletes the preview bucket (404) idempotently while keeping dev intact", func(t *testing.T) {
					h.SkipIfKeep(t)
					// Arrange / Act.
					h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-1", "--yes", "--non-interactive", "--skip-preview")
					h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-1", "--yes", "--non-interactive", "--skip-preview")

					// Assert.
					assertBucketAbsent(t, previewBucket)
					_ = describeGCSBucket(t, devBucket)
				})
			})
		})
	})
}

// Private helpers ------------------------------------------------------------

type gcsBucketMetadata struct {
	Labels                   map[string]string `json:"labels"`
	VersioningEnabled        bool              `json:"versioning_enabled"`
	PublicAccessPrevention   string            `json:"public_access_prevention"`
	UniformBucketLevelAccess bool              `json:"uniform_bucket_level_access"`
	SoftDeletePolicy         struct {
		RetentionDurationSeconds string `json:"retentionDurationSeconds"`
	} `json:"soft_delete_policy"`
}

type overlayFSReport struct {
	FallbackSource   string `json:"fallbackSource"`
	FallbackContent  string `json:"fallbackContent"`
	OverrideSource   string `json:"overrideSource"`
	OverrideContent  string `json:"overrideContent"`
	DevSeedAfter     string `json:"devSeedAfter"`
	DeltaAbsentInDev bool   `json:"deltaAbsentInDev"`
}

func assertBucketsAbsent(t *testing.T, names ...string) {
	t.Helper()
	for _, b := range names {
		if b != "" {
			assertBucketAbsent(t, b)
		}
	}
}

func describeGCSBucket(t *testing.T, bucketName string) gcsBucketMetadata {
	t.Helper()
	raw := runCommand(t, repositoryRoot(t), "gcloud", "storage", "buckets", "describe", "gs://"+bucketName, "--format=json")
	var meta gcsBucketMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("unmarshal bucket metadata: %v\n%s", err, raw)
	}
	return meta
}

func assertProtectedBucketFields(t *testing.T, bucketName string, meta gcsBucketMetadata) {
	t.Helper()
	if !meta.UniformBucketLevelAccess {
		t.Fatalf("expected uniform_bucket_level_access on %s", bucketName)
	}
	assertEqualStr(t, "public_access_prevention", meta.PublicAccessPrevention, "enforced")
	if !meta.VersioningEnabled {
		t.Fatalf("expected versioning_enabled on %s", bucketName)
	}
	assertEqualStr(t, "soft_delete_policy", meta.SoftDeletePolicy.RetentionDurationSeconds, "604800")
}

func assertBucketAbsent(t *testing.T, bucketName string) {
	t.Helper()
	out := commandFailureEnv(t, repositoryRoot(t), nil, "gcloud", "storage", "buckets", "describe", "gs://"+bucketName)
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "not found") && !strings.Contains(lower, "404") {
		t.Fatalf("expected 404/not found for gs://%s, got:\n%s", bucketName, out)
	}
}

func assertRemoteStateExists(t *testing.T, backendURL, stack string) {
	t.Helper()
	if strings.HasPrefix(backendURL, "file://") {
		stateRoot := strings.TrimPrefix(backendURL, "file://")
		matches, _ := filepath.Glob(filepath.Join(stateRoot, ".pulumi", "stacks", "*", stack+".json"))
		if len(matches) == 0 {
			t.Fatalf("expected stack file %s.json under %s", stack, stateRoot)
		}
		return
	}
	lsOut := runCommand(t, repositoryRoot(t), "gcloud", "storage", "ls", backendURL+"/.pulumi/stacks/**")
	assertContains(t, lsOut, stack+".json")
}

func seedGCSObject(t *testing.T, bucketName, key, content string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()
	store, err := overlayfs.NewStore(client, bucketName, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, key, []byte(content), "text/plain"); err != nil {
		t.Fatalf("seed bucket %s: %v", bucketName, err)
	}
}

func exerciseOverlayFS(t *testing.T, devBucket, previewBucket, previewFallback string) overlayFSReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()
	devStore, err := overlayfs.NewStore(client, devBucket, "")
	if err != nil {
		t.Fatal(err)
	}
	previewStore, err := overlayfs.NewStore(client, previewBucket, previewFallback)
	if err != nil {
		t.Fatal(err)
	}
	return runOverlayOperations(t, ctx, devStore, previewStore)
}

func runOverlayOperations(t *testing.T, ctx context.Context, devStore, previewStore *overlayfs.Store) overlayFSReport {
	t.Helper()
	seedBytes, src, err := previewStore.Read(ctx, "seed.txt")
	if err != nil {
		t.Fatalf("overlay fallback read: %v", err)
	}
	if err := previewStore.Write(ctx, "delta.txt", []byte("only-preview"), "text/plain"); err != nil {
		t.Fatalf("preview write delta: %v", err)
	}
	if err := previewStore.Write(ctx, "seed.txt", []byte("preview-override"), "text/plain"); err != nil {
		t.Fatalf("preview override seed: %v", err)
	}
	overBytes, overSrc, err := previewStore.Read(ctx, "seed.txt")
	if err != nil {
		t.Fatal(err)
	}
	devSeedAfter, _, err := devStore.Read(ctx, "seed.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _, deltaErr := devStore.Read(ctx, "delta.txt")
	return overlayFSReport{
		FallbackSource:   src,
		FallbackContent:  string(seedBytes),
		OverrideSource:   overSrc,
		OverrideContent:  string(overBytes),
		DevSeedAfter:     string(devSeedAfter),
		DeltaAbsentInDev: errors.Is(deltaErr, storage.ErrObjectNotExist),
	}
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}

func assertEqualStr(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}
