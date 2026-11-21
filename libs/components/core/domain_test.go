package core_test

import (
	"reflect"
	"testing"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

func TestEnvironmentDomain(t *testing.T) {
	t.Run("given a persistent baseDomain", func(t *testing.T) {
		t.Run("when computing domains across dev, stage, prod, and preview-<id>, then it formats {project}-{tier|previewId}.{baseDomain}", func(t *testing.T) {
			// Arrange.
			cases := []struct {
				env  core.Environment
				svc  string
				want string
			}{
				{core.EnvDev, "web", "storefront-dev.vercel.cloudvoyant.io"},
				{core.EnvStage, "web", "storefront-stage.vercel.cloudvoyant.io"},
				{core.EnvProd, "web", "storefront-prod.vercel.cloudvoyant.io"},
				{core.Environment("preview-pr-42"), "web", "storefront-pr-42.vercel.cloudvoyant.io"},
				{core.Environment("preview-dev"), "web", "storefront-preview-dev.vercel.cloudvoyant.io"},
				{core.EnvProd, "api", "storefront-api-prod.vercel.cloudvoyant.io"},
			}

			// Act / Assert.
			for _, tc := range cases {
				got, err := core.EnvironmentDomain("storefront", tc.env, tc.svc, "https://vercel.cloudvoyant.io/")
				if err != nil || got != tc.want {
					t.Fatalf("EnvironmentDomain(%s, %s) = (%q, %v), want %q", tc.env, tc.svc, got, err, tc.want)
				}
			}
		})

		t.Run("when deduplicating multiple domain lists, then it normalizes schemes and preserves order", func(t *testing.T) {
			// Arrange / Act.
			got := core.DedupeDomains(
				[]string{"https://App.Acme.com/", "storefront-prod.vercel.cloudvoyant.io"},
				[]string{"app.acme.com", "", "www.acme.com"},
			)

			// Assert.
			want := []string{"app.acme.com", "storefront-prod.vercel.cloudvoyant.io", "www.acme.com"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("DedupeDomains = %v, want %v", got, want)
			}
		})
	})
}
