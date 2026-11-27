package vercel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnsureVercelCert(t *testing.T) {
	t.Run("given a Vercel certs API server", func(t *testing.T) {
		t.Run("when a valid exact-match cert already exists, then it skips POST /v7/certs", func(t *testing.T) {
			// Arrange.
			existing := []vercelCertSummary{{
				ID:        "cert_existing",
				CNs:       []string{"difflab-io-pr-1.vercel.cloudvoyant.io"},
				ExpiresAt: time.Now().Add(30 * 24 * time.Hour).UnixMilli(),
			}}
			srv, postCalls, _, _ := newMockVercelCertsServer(existing)
			defer srv.Close()

			// Act.
			err := ensureVercelCert(context.Background(), "tok_test", srv.URL, "team_123", "difflab-io-pr-1.vercel.cloudvoyant.io")

			// Assert.
			assertNoCertErr(t, err)
			if got := postCalls.Load(); got != 0 {
				t.Fatalf("expected 0 POST calls, got %d", got)
			}
		})

		t.Run("when a valid wildcard cert covers the hostname, then it skips POST /v7/certs", func(t *testing.T) {
			// Arrange.
			existing := []vercelCertSummary{{
				ID:        "cert_wild",
				CNs:       []string{"*.vercel.cloudvoyant.io"},
				ExpiresAt: time.Now().Add(30 * 24 * time.Hour).UnixMilli(),
			}}
			srv, postCalls, _, _ := newMockVercelCertsServer(existing)
			defer srv.Close()

			// Act.
			err := ensureVercelCert(context.Background(), "tok_test", srv.URL, "team_123", "difflab-io-pr-42.vercel.cloudvoyant.io")

			// Assert.
			assertNoCertErr(t, err)
			if got := postCalls.Load(); got != 0 {
				t.Fatalf("expected 0 POST calls, got %d", got)
			}
		})

		t.Run("when no valid cert covers the hostname, then it issues an HTTP-01 cert via POST /v7/certs with teamId", func(t *testing.T) {
			// Arrange.
			existing := []vercelCertSummary{{
				ID:        "cert_expiring",
				CNs:       []string{"difflab-io-pr-9.vercel.cloudvoyant.io"},
				ExpiresAt: time.Now().Add(2 * time.Hour).UnixMilli(),
			}}
			srv, postCalls, gotTeamID, gotCNs := newMockVercelCertsServer(existing)
			defer srv.Close()

			// Act.
			err := ensureVercelCert(context.Background(), "tok_test", srv.URL, "team_123", "difflab-io-pr-9.vercel.cloudvoyant.io")

			// Assert.
			assertNoCertErr(t, err)
			if got := postCalls.Load(); got != 1 {
				t.Fatalf("expected 1 POST call, got %d", got)
			}
			if *gotTeamID != "team_123" {
				t.Fatalf("expected teamId=team_123, got %q", *gotTeamID)
			}
			if len(*gotCNs) != 1 || (*gotCNs)[0] != "difflab-io-pr-9.vercel.cloudvoyant.io" {
				t.Fatalf("expected cns=[difflab-io-pr-9.vercel.cloudvoyant.io], got %v", *gotCNs)
			}
		})
	})
}

func TestCertCNCoversHost(t *testing.T) {
	t.Run("given certCNCoversHost", func(t *testing.T) {
		t.Run("when matching wildcard and multi-label subdomains, then wildcard only covers one label", func(t *testing.T) {
			// Arrange / Act / Assert.
			if !certCNCoversHost("*.vercel.cloudvoyant.io", "difflab-io-pr-1.vercel.cloudvoyant.io") {
				t.Fatal("expected single-label subdomain to match wildcard")
			}
			if certCNCoversHost("*.vercel.cloudvoyant.io", "a.b.vercel.cloudvoyant.io") {
				t.Fatal("expected multi-label subdomain not to match single-level wildcard")
			}
			if certCNCoversHost("*.vercel.cloudvoyant.io", "vercel.cloudvoyant.io") {
				t.Fatal("expected apex not to match wildcard")
			}
		})
	})
}

func newMockVercelCertsServer(existing []vercelCertSummary) (*httptest.Server, *atomic.Int32, *string, *[]string) {
	var postCalls atomic.Int32
	var gotTeamID string
	var gotCNs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(vercelCertsListPayload{Certs: existing})
			return
		}
		postCalls.Add(1)
		gotTeamID = r.URL.Query().Get("teamId")
		var req vercelIssueCertRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotCNs = req.CNs
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"cert_new"}`))
	}))
	return srv, &postCalls, &gotTeamID, &gotCNs
}

func assertNoCertErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
