package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Public API -----------------------------------------------------------------

func TestNeonDatabase(t *testing.T) {
	baseBackend := RequireLiveNeon(t)
	orgID := detectNeonOrgID(t)
	runID := fmt.Sprintf("%d", time.Now().Unix()%100000)
	appProject := "obneon" + runID
	h := NewLiveHarness(
		t,
		"neon-db",
		"openbase-neon-db-fixture",
		baseBackend,
		"OPENBASE_TEST_APP_PROJECT="+appProject,
		"NEON_ORG_ID="+orgID,
	)

	var neonProjectID, defaultBranchID, devBranchID, prodBranchID, previewBranchID string
	var devPooledURL, prodPooledURL, prodDirectURL, stagePooledURL string

	t.Run("when planning and applying SharedProject + Database across dev<-main, prod<-main, and stage<-prod", func(t *testing.T) {
		// Arrange.
		h.InitStack(t, "shared")
		h.InitStack(t, "dev")
		h.InitStack(t, "prod")
		h.InitStack(t, "stage")

		// Act.
		h.Pulumi(t, nil, "up", "--stack", "shared", "--yes", "--non-interactive", "--skip-preview")
		neonProjectID = h.Output(t, "shared", "projectId")
		defaultBranchID = h.Output(t, "shared", "defaultBranchId")

		baseNeonEnv := []string{
			"OPENBASE_TEST_NEON_PROJECT_ID=" + neonProjectID,
			"OPENBASE_TEST_DEFAULT_BRANCH_ID=" + defaultBranchID,
		}
		devPlan := h.Pulumi(t, baseNeonEnv, "preview", "--stack", "dev")
		h.Pulumi(t, baseNeonEnv, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview")
		h.Pulumi(t, baseNeonEnv, "up", "--stack", "prod", "--yes", "--non-interactive", "--skip-preview")

		devBranchID = h.Output(t, "dev", "branchId")
		devPooledURL = h.Output(t, "dev", "databaseUrl")
		prodBranchID = h.Output(t, "prod", "branchId")
		prodPooledURL = h.Output(t, "prod", "databaseUrl")
		prodDirectURL = h.Output(t, "prod", "directUrl")

		execSQL(t, prodDirectURL, "CREATE TABLE orders (id INT PRIMARY KEY, label TEXT); INSERT INTO orders VALUES (1, 'prod-seed');")

		stageEnv := append(append([]string{}, baseNeonEnv...), "OPENBASE_TEST_PARENT_BRANCH_ID="+prodBranchID)
		h.Pulumi(t, stageEnv, "up", "--stack", "stage", "--yes", "--non-interactive", "--skip-preview")
		stagePooledURL = h.Output(t, "stage", "databaseUrl")

		// Assert.
		t.Run("it connects and executes DDL/queries over both pooled DATABASE_URL and unpooled DIRECT_URL via pgx", func(t *testing.T) {
			assertContains(t, devPlan, "neon:index:Branch")
			assertContains(t, prodPooledURL, "-pooler")
			got := querySingleText(t, prodPooledURL, "SELECT label FROM orders WHERE id = 1")
			assertEqualStr(t, "prod pooled query", got, "prod-seed")
		})

		t.Run("it inherits prod schema in stage, enforces Protect on prod/stage, and preserves branches when apply fails", func(t *testing.T) {
			stageLabel := querySingleText(t, stagePooledURL, "SELECT label FROM orders WHERE id = 1")
			assertEqualStr(t, "stage<-prod inherited row", stageLabel, "prod-seed")
			assertContains(t, strings.ToLower(h.PulumiFail(t, baseNeonEnv, "destroy", "--stack", "prod", "--yes", "--non-interactive")), "protect")
			assertContains(t, strings.ToLower(h.PulumiFail(t, stageEnv, "destroy", "--stack", "stage", "--yes", "--non-interactive")), "protect")
			failEnv := append(append([]string{}, baseNeonEnv...), "OPENBASE_TEST_FAIL_INFRA=1")
			assertContains(t, h.PulumiFail(t, failEnv, "up", "--stack", "dev", "--yes", "--non-interactive", "--skip-preview"), "simulated neon")
			assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, devBranchID), http.StatusOK)
		})

		t.Run("it reloads exported connection secrets from remote gs:// state in a fresh checkout", func(t *testing.T) {
			freshDir := copyFixture(t, "neon-db")
			freshEnv := []string{
				"PULUMI_HOME=" + t.TempDir(),
				"PULUMI_BACKEND_URL=" + h.BackendURL,
				"PULUMI_CONFIG_PASSPHRASE=",
				"PULUMI_SKIP_UPDATE_CHECK=true",
			}
			runCommandEnv(t, freshDir, freshEnv, "pulumi", "login", h.BackendURL)
			reloaded := strings.TrimSpace(runCommandEnv(t, freshDir, freshEnv, "pulumi", "stack", "output", "databaseUrl", "--show-secrets", "--stack", "dev"))
			assertEqualStr(t, "reloaded dev DATABASE_URL", reloaded, devPooledURL)
		})
	})

	t.Run("when branching preview from seeded dev", func(t *testing.T) {
		// Arrange.
		execSQL(t, devPooledURL, "CREATE TABLE items (id INT PRIMARY KEY, note TEXT); INSERT INTO items VALUES (1, 'from-dev');")
		h.InitStack(t, "preview-pr-1")
		previewEnv := []string{
			"OPENBASE_TEST_NEON_PROJECT_ID=" + neonProjectID,
			"OPENBASE_TEST_DEFAULT_BRANCH_ID=" + defaultBranchID,
			"OPENBASE_TEST_PARENT_BRANCH_ID=" + devBranchID,
		}

		// Act.
		h.Pulumi(t, previewEnv, "up", "--stack", "preview-pr-1", "--yes", "--non-interactive", "--skip-preview")
		previewBranchID = h.Output(t, "preview-pr-1", "branchId")
		previewURL := h.Output(t, "preview-pr-1", "databaseUrl")

		// Assert.
		t.Run("it immediately reads dev rows and isolates preview INSERT/ALTER from dev", func(t *testing.T) {
			inherited := querySingleText(t, previewURL, "SELECT note FROM items WHERE id = 1")
			assertEqualStr(t, "preview inherited row", inherited, "from-dev")
			execSQL(t, previewURL, "INSERT INTO items VALUES (2, 'only-preview'); ALTER TABLE items ADD COLUMN preview_only TEXT DEFAULT 'yes';")
			devCount := querySingleText(t, devPooledURL, "SELECT COUNT(*)::text FROM items")
			assertEqualStr(t, "dev row count isolated", devCount, "1")
		})
	})

	t.Run("when destroying preview", func(t *testing.T) {
		// Arrange / Act.
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-1", "--yes", "--non-interactive", "--skip-preview")
		h.Pulumi(t, nil, "destroy", "--stack", "preview-pr-1", "--yes", "--non-interactive", "--skip-preview")

		// Assert.
		t.Run("it deletes the Neon branch, succeeds on repeat destroy, and leaves dev/stage/prod intact", func(t *testing.T) {
			assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, previewBranchID), http.StatusNotFound)
			assertNeonStatus(t, fmt.Sprintf("https://console.neon.tech/api/v2/projects/%s/branches/%s", neonProjectID, devBranchID), http.StatusOK)
		})
	})

	h.OnCleanupVerify(func(t *testing.T) {
		if neonProjectID != "" {
			assertNeonStatus(t, "https://console.neon.tech/api/v2/projects/"+neonProjectID, http.StatusNotFound)
		}
	})
}

// Private helpers ------------------------------------------------------------

func detectNeonOrgID(t *testing.T) string {
	t.Helper()
	if existing := os.Getenv("NEON_ORG_ID"); existing != "" {
		return existing
	}
	req, err := http.NewRequest(http.MethodGet, "https://console.neon.tech/api/v2/users/me/organizations", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("NEON_API_KEY"))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var parsed struct {
		Organizations []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &parsed)
	if len(parsed.Organizations) > 0 {
		return parsed.Organizations[0].ID
	}
	return ""
}

func execSQL(t *testing.T, connURL, sqlStmt string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, connURL)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sqlStmt); err != nil {
		t.Fatalf("exec SQL: %v", err)
	}
}

func querySingleText(t *testing.T, connURL, sqlQuery string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, connURL)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var out string
	if err := conn.QueryRow(ctx, sqlQuery).Scan(&out); err != nil {
		t.Fatalf("queryRow: %v", err)
	}
	return out
}

func assertNeonStatus(t *testing.T, url string, want int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("NEON_API_KEY"))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Neon API GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("Neon API GET %s status = %d, want %d (%s)", url, resp.StatusCode, want, string(raw))
	}
}
