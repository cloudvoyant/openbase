package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	openbase "github.com/cloudvoyant/openbase/libs/components"
	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
		teamID := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID")
		teamSlug := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_SLUG")

		vercelPlat := vercel.Platform{TeamID: teamID, TeamSlug: teamSlug}
		_, err := openbase.Platform{
			Org: teamSlug,
			Resources: []core.PlatformSpecItem{
				vercelPlat,
				gcp.Platform{
					GCPProject:  pulumi.String(gcpProject),
					PoolSuffix:  os.Getenv("OPENBASE_TEST_RUN_ID"),
					Links:       []core.OIDCProviderSource{vercelPlat},
					SkipKeyRing: true,
				},
			},
		}.Register(ctx)
		return err
	})
}
