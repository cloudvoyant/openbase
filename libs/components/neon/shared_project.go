// Package neon provides OpenBase Pulumi components for Neon serverless PostgreSQL.
package neon

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/internal/providers/neon"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

// SharedProjectArgs configures a cross-environment Neon Project root in the "shared" stack.
type SharedProjectArgs struct {
	Project                 string
	Repo                    string
	Environment             string
	OrgID                   string
	RegionID                string
	PostgresVersion         int
	HistoryRetentionSeconds int
}

// SharedProject provisions the shared Neon Project cluster consumed by per-environment branches.
type SharedProject struct {
	pulumi.ResourceState
	core.StatelessMeta

	Project         *neonprov.Project
	ProjectID       pulumi.StringOutput
	DefaultBranchID pulumi.StringOutput
	RegionID        pulumi.StringOutput
}

// NewSharedProject creates a shared Neon Project root and exports projectId and defaultBranchId.
func NewSharedProject(ctx *pulumi.Context, name string, args SharedProjectArgs, opts ...pulumi.ResourceOption) (*SharedProject, error) {
	res := &SharedProject{}
	env := args.Environment
	if env == "" {
		env = string(core.EnvShared)
	}
	meta, err := core.InitStateless(ctx, "openbase:neon:SharedProject", name, "neon-shared", res, &res.StatelessMeta, core.StatelessResourceArgs{
		Project:     args.Project,
		Repo:        args.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	proj, err := createNeonProject(ctx, name, meta, args, res)
	if err != nil {
		return nil, err
	}
	res.Project = proj
	res.ProjectID = proj.ID().ToStringOutput()
	res.DefaultBranchID = proj.DefaultBranchId
	res.RegionID = proj.RegionId
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			"NEON_PROJECT_ID": proj.ID().ToStringOutput(),
		},
	})
	if err := ctx.RegisterResourceOutputs(res, pulumi.Map{
		"projectId":       res.ProjectID,
		"defaultBranchId": res.DefaultBranchID,
		"regionId":        res.RegionID,
	}); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

func createNeonProject(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args SharedProjectArgs,
	parent pulumi.Resource,
) (*neonprov.Project, error) {
	region := args.RegionID
	if region == "" {
		region = "aws-us-east-1"
	}
	pgVer := args.PostgresVersion
	if pgVer == 0 {
		pgVer = 17
	}
	retention := args.HistoryRetentionSeconds
	if retention == 0 {
		retention = 86400
	}
	orgID := args.OrgID
	if orgID == "" {
		orgID = os.Getenv("NEON_ORG_ID")
	}
	projArgs := &neonprov.ProjectArgs{
		Name:                    pulumi.String(meta.CanonicalName),
		RegionId:                pulumi.String(region),
		PgVersion:               pulumi.Float64(float64(pgVer)),
		HistoryRetentionSeconds: pulumi.Float64(float64(retention)),
		StorePassword:           pulumi.String("yes"),
	}
	if orgID != "" {
		projArgs.OrgId = pulumi.String(orgID)
	}
	return neonprov.NewProject(ctx, name+"-project", projArgs, pulumi.Parent(parent))
}
