package main

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		explicitEnv := os.Getenv("OPENBASE_TEST_ENV_OVERRIDE")
		branchFrom := os.Getenv("OPENBASE_TEST_BRANCH_FROM")
		if branchFrom == "" {
			branchFrom = "dev"
		}
		ledger, err := core.NewStateful(ctx, "ledger", &core.StatefulArgs{
			ProjectName: "storefront",
			Repo:        "cloudvoyant/openbase",
			Environment: explicitEnv,
			Kind:        "ledger-store",
			BranchFrom:  branchFrom,
			Attributes: map[string]pulumi.StringInput{
				"endpoint": pulumi.String("https://ledger.storefront.internal"),
			},
		})
		if err != nil {
			return err
		}
		worker, err := core.NewStateless(ctx, "worker", &core.StatelessArgs{
			ProjectName: "storefront",
			Repo:        "cloudvoyant/openbase",
			Environment: explicitEnv,
			Kind:        "background-worker",
			Links:       []core.Resource{ledger},
		})
		if err != nil {
			return err
		}
		ctx.Export("ledgerCanonicalName", pulumi.String(ledger.OpenBaseMeta().CanonicalName))
		ctx.Export("ledgerBranchSource", pulumi.String(ledger.BranchSource()))
		ctx.Export("workerCanonicalName", pulumi.String(worker.OpenBaseMeta().CanonicalName))
		ctx.Export("workerLinkedEndpoint", ledger.Link().Attributes["endpoint"])
		return nil
	})
}
