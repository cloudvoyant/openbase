// Package neon provides OpenBase Pulumi components for Neon serverless PostgreSQL.
package neon

import (
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/internal/providers/neon"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*SharedProjectResource)(nil)

// SharedProject declares a cross-environment Neon Project root in the "shared" stack.
type SharedProject struct {
	Name                    string
	Project                 string
	Repo                    string
	Environment             string
	OrgID                   string
	RegionID                string
	PostgresVersion         int
	HistoryRetentionSeconds int
}

// SharedProjectResource is the provisioned shared Neon Project cluster handle consumed by per-environment branches.
type SharedProjectResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	Project         *neonprov.Project
	ProjectID       pulumi.StringOutput
	DefaultBranchID pulumi.StringOutput
	RegionID        pulumi.StringOutput
}

// Register provisions a shared Neon Project root and exports projectId and defaultBranchId.
func (s SharedProject) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*SharedProjectResource, error) {
	s.Name = defaultIdentifier(s.Name, "shared")
	env := defaultIdentifier(s.Environment, string(core.EnvShared))
	res := &SharedProjectResource{}
	meta, err := core.InitStateless(ctx, "openbase:neon:SharedProject", "neon-shared", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        s.Name,
		Project:     s.Project,
		Repo:        s.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	proj, err := createNeonProject(ctx, s.Name, meta, s, res)
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
	spec SharedProject,
	parent pulumi.Resource,
) (*neonprov.Project, error) {
	region := defaultIdentifier(spec.RegionID, "aws-us-east-1")
	pgVer := spec.PostgresVersion
	if pgVer == 0 {
		pgVer = 17
	}
	retention := spec.HistoryRetentionSeconds
	if retention == 0 {
		retention = 86400
	}
	orgID := defaultIdentifier(spec.OrgID, os.Getenv("NEON_ORG_ID"))
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
