package main

import (
	"errors"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		if os.Getenv("OPENBASE_TEST_FAIL_INFRA") == "1" {
			return errors.New("simulated infrastructure program failure")
		}
		gcpProject := os.Getenv("OPENBASE_TEST_GCP_PROJECT")
		appProject := os.Getenv("OPENBASE_TEST_APP_PROJECT")
		if appProject == "" {
			appProject = "obe2ebkt"
		}
		revision := os.Getenv("OPENBASE_TEST_REVISION")
		if revision == "" {
			revision = "a"
		}
		env, err := core.ResolveEnvironment(ctx, "")
		if err != nil {
			return err
		}
		branchFrom := ""
		if env.IsPreview() {
			branchFrom = "dev"
		}
		uploads, err := gcp.NewBucket(ctx, "uploads", gcp.BucketArgs{
			StatefulResourceArgs: core.StatefulResourceArgs{
				Project:    appProject,
				Repo:       "cloudvoyant/openbase",
				BranchFrom: branchFrom,
			},
			GCPProject:      pulumi.String(gcpProject),
			BucketNamespace: gcpProject,
			CORS: []gcp.BucketCORSRule{
				{Origins: []string{"https://example.com"}, Methods: []string{"GET", "HEAD"}},
			},
			ExtraLabels: map[string]string{"openbase-revision": revision},
		})
		if err != nil {
			return err
		}
		ctx.Export("bucketName", uploads.BucketName)
		ctx.Export("bucketUrl", uploads.BucketURL)
		ctx.Export("fallbackBucketName", uploads.FallbackBucketName)
		return nil
	})
}
