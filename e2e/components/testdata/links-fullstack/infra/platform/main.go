package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
	"github.com/cloudvoyant/openbase/libs/components/vercel"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
		teamID := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_ID")
		teamSlug := os.Getenv("OPENBASE_TEST_VERCEL_TEAM_SLUG")

		vercelPlat, err := vercel.NewPlatform(ctx, "vercel-platform", &vercel.PlatformArgs{
			Org:      teamSlug,
			TeamID:   teamID,
			TeamSlug: teamSlug,
		})
		if err != nil {
			return err
		}
		_, err = gcp.NewPlatform(ctx, "gcp-platform", &gcp.PlatformArgs{
			Org:         teamSlug,
			GCPProject:  pulumi.String(gcpProject),
			PoolSuffix:  os.Getenv("OPENBASE_TEST_RUN_ID"),
			Links:       []core.OIDCProviderSource{vercelPlat},
			SkipKeyRing: true,
		})
		return err
	})
}
