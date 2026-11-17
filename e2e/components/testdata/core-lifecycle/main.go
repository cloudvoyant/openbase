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
		ledger, err := core.Stateful{
			Name:        "ledger",
			Project:     "storefront",
			Repo:        "cloudvoyant/openbase",
			Environment: explicitEnv,
			Kind:        "ledger-store",
			BranchFrom:  branchFrom,
			Env: map[string]pulumi.StringInput{
				"LEDGER_ENDPOINT": pulumi.String("https://ledger.storefront.internal"),
			},
		}.Register(ctx)
		if err != nil {
			return err
		}
		worker, err := core.Stateless{
			Name:        "worker",
			Project:     "storefront",
			Repo:        "cloudvoyant/openbase",
			Environment: explicitEnv,
			Kind:        "background-worker",
			Links:       []core.Resource{ledger},
		}.Register(ctx)
		if err != nil {
			return err
		}
		ctx.Export("ledgerCanonicalName", pulumi.String(ledger.OpenBaseMeta().CanonicalName))
		ctx.Export("ledgerBranchSource", pulumi.String(ledger.BranchSource()))
		ctx.Export("workerCanonicalName", pulumi.String(worker.OpenBaseMeta().CanonicalName))
		ctx.Export("workerLinkedEndpoint", worker.Link().Env["LEDGER_ENDPOINT"])
		return nil
	})
}
