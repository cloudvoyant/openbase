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
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
		forceDestroy := true

		_, err := openbase.Project{
			Project:          appProject,
			PlatformStackRef: "organization/platform/platform",
			Resources: []core.ScopedSpec{
				gcp.Project{},
				vercel.Project{
					VercelProjectName:    "openbase-e2e-" + appProject,
					SkipStageEnvironment: true,
				},
				gcp.Bucket{
					Name:            "shared-assets",
					GCPProject:      pulumi.String(gcpProject),
					BucketNamespace: gcpProject,
					ForceDestroy:    &forceDestroy,
				},
			},
		}.Register(ctx)
		return err
	})
}
