package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	var devBucket, stageBucket, prodBucket, previewBucket, previewFallback string

	t.Run("when planning a bucket", func(t *testing.T) {
		// Arrange.
		h.InitStack(t, "dev")

		// Act.
		previewOut := h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=a"}, "preview", "--stack", "dev")

		// Assert.
		t.Run("it previews gcp:storage:Bucket and leaves remote cloud untouched", func(t *testing.T) {
			assertContains(t, previewOut, "gcp:storage:Bucket")
			expectedDev := fmt.Sprintf("%s-%s-dev-uploads", gcpProject, appProject)
			assertBucketAbsent(t, expectedDev)
		})
	})

	t.Run("when applying updates across dev, stage, prod, and preview", func(t *testing.T) {
		// Arrange.
		h.InitStack(t, "stage")
		h.InitStack(t, "prod")
		h.InitStack(t, "preview-pr-1")

		// Act.
		for _, stack := range []string{"dev", "stage", "prod", "preview-pr-1"} {
			h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=a"}, "up", "--stack", stack, "--yes", "--non-interactive", "--skip-preview")
		}
		devBucket = h.Output(t, "dev", "bucketName")
		stageBucket = h.Output(t, "stage", "bucketName")
		prodBucket = h.Output(t, "prod", "bucketName")
		previewBucket = h.Output(t, "preview-pr-1", "bucketName")
		previewFallback = h.Output(t, "preview-pr-1", "fallbackBucketName")

		h.Pulumi(t, []string{"OPENBASE_TEST_REVISION=b"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
		devAfterUpdate := h.Output(t, "dev", "bucketName")

		// Assert.
		t.Run("it names each bucket for its environment, updates labels in-place, and saves state in gs://", func(t *testing.T) {
			assertEqualStr(t, "in-place dev bucket name", devAfterUpdate, devBucket)
			devMeta := describeGCSBucket(t, devBucket)
			assertEqualStr(t, "updated revision label", devMeta.Labels["openbase-revision"], "b")
			assertRemoteStateExists(t, h.BackendURL, "dev")
		})

		t.Run("it enforces uniform bucket-level access, public-access prevention, and prod/stage Protect", func(t *testing.T) {
			assertProtectedBucketMeta(t, prodBucket)
			assertProtectedBucketMeta(t, stageBucket)
			failProd := h.PulumiFail(t, nil, "destroy", "--stack", "prod", "--yes", "--non-interactive")
			failStage := h.PulumiFail(t, nil, "destroy", "--stack", "stage", "--yes", "--non-interactive")
			assertContains(t, strings.ToLower(failProd), "protect")
			assertContains(t, strings.ToLower(failStage), "protect")
			_ = describeGCSBucket(t, prodBucket)
		})

		t.Run("when the program fails (OPENBASE_TEST_FAIL_INFRA=1), it preserves the provisioned bucket", func(t *testing.T) {
			failOut := h.PulumiFail(t, []string{"OPENBASE_TEST_FAIL_INFRA=1"}, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
			assertContains(t, failOut, "simulated infrastructure program failure")
			_ = describeGCSBucket(t, devBucket)
		})
	})

	t.Run("when querying from a fresh checkout", func(t *testing.T) {
		// Arrange.
		freshDir := copyFixture(t, "gcp-bucket")
		freshHome := t.TempDir()
		freshEnv := []string{
			"PULUMI_HOME=" + freshHome,
			"PULUMI_BACKEND_URL=" + h.BackendURL,
			"PULUMI_CONFIG_PASSPHRASE=",
			"PULUMI_SKIP_UPDATE_CHECK=true",
		}

		// Act.
		runCommandEnv(t, freshDir, freshEnv, "pulumi", "login", h.BackendURL)
		queried := strings.TrimSpace(runCommandEnv(t, freshDir, freshEnv, "pulumi", "stack", "output", "bucketName", "--stack", "dev"))

		// Assert.
		t.Run("it reads exported bucket outputs from remote gs:// state", func(t *testing.T) {
			assertEqualStr(t, "fresh checkout bucketName", queried, devBucket)
		})
	})

	t.Run("when branching preview from seeded dev", func(t *testing.T) {
		// Arrange / Act / Assert.
		t.Run("overlayfs reads dev objects on miss and writes delta objects only to preview", func(t *testing.T) {
			verifyLiveOverlayFS(t, devBucket, previewBucket, previewFallback)
		})
	})

	t.Run("when destroying preview", func(t *testing.T) {
		// Arrange / Act.
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-1", "--yes", "--non-interactive", "--skip-preview")
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-1", "--yes", "--non-interactive", "--skip-preview")

		// Assert.
		t.Run("it deletes the preview bucket (404), succeeds on repeat destroy, and keeps dev intact", func(t *testing.T) {
			assertBucketAbsent(t, previewBucket)
			_ = describeGCSBucket(t, devBucket)
		})
	})

	h.OnCleanupVerify(func(t *testing.T) {
		for _, b := range []string{devBucket, stageBucket, prodBucket, previewBucket} {
			if b != "" {
				assertBucketAbsent(t, b)
			}
		}
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

func describeGCSBucket(t *testing.T, bucketName string) gcsBucketMetadata {
	t.Helper()
	raw := runCommand(t, repositoryRoot(t), "gcloud", "storage", "buckets", "describe", "gs://"+bucketName, "--format=json")
	var meta gcsBucketMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("unmarshal bucket metadata: %v\n%s", err, raw)
	}
	return meta
}

func assertProtectedBucketMeta(t *testing.T, bucketName string) {
	t.Helper()
	meta := describeGCSBucket(t, bucketName)
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
	lsOut := runCommand(t, repositoryRoot(t), "gcloud", "storage", "ls", backendURL+"/.pulumi/stacks/**")
	assertContains(t, lsOut, stack+".json")
}

func verifyLiveOverlayFS(t *testing.T, devBucket, previewBucket, previewFallback string) {
	t.Helper()
	assertEqualStr(t, "preview fallback target", previewFallback, devBucket)
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
	if err := devStore.Write(ctx, "seed.txt", []byte("hello-from-dev"), "text/plain"); err != nil {
		t.Fatalf("seed dev bucket: %v", err)
	}

	previewStore, err := overlayfs.NewStore(client, previewBucket, previewFallback)
	if err != nil {
		t.Fatal(err)
	}
	assertOverlayBehavior(t, ctx, devStore, previewStore, devBucket, previewBucket)
}

func assertOverlayBehavior(
	t *testing.T,
	ctx context.Context,
	devStore, previewStore *overlayfs.Store,
	devBucket, previewBucket string,
) {
	t.Helper()
	seedBytes, src, err := previewStore.Read(ctx, "seed.txt")
	if err != nil {
		t.Fatalf("overlay fallback read: %v", err)
	}
	assertEqualStr(t, "fallback source", src, devBucket)
	assertEqualStr(t, "fallback content", string(seedBytes), "hello-from-dev")

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
	assertEqualStr(t, "override source", overSrc, previewBucket)
	assertEqualStr(t, "override content", string(overBytes), "preview-override")

	devSeedAfter, _, err := devStore.Read(ctx, "seed.txt")
	if err != nil {
		t.Fatal(err)
	}
	assertEqualStr(t, "dev seed preserved", string(devSeedAfter), "hello-from-dev")
	if _, _, err := devStore.Read(ctx, "delta.txt"); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Fatalf("expected delta.txt absent from dev bucket, got err=%v", err)
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
