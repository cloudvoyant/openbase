package neon

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-neon-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatefulResource = (*Project)(nil)
	_ ProjectInput          = (*Project)(nil)
	_ ProjectInput          = (*ProjectRef)(nil)
)

// ProjectInput is satisfied by in-memory *Project handles and cross-stack *ProjectRef lookups.
type ProjectInput interface {
	neonProjectValues() neonProjectData
}

// ProjectArgs configures an application's shared Neon Project cluster in Premise's infra/shared stack.
type ProjectArgs struct {
	ProjectName             string
	Repo                    string
	Account                 AccountInput
	OrgID                   string
	RegionID                string
	APIKey                  pulumi.StringInput
	APIBaseURL              string
	PostgresVersion         int
	HistoryRetentionSeconds int
	ImportProjectID         string
}

// Project is the provisioned Neon Project cluster handle consumed by per-environment neon.Database branches.
type Project struct {
	pulumi.ResourceState
	core.StatefulMeta

	Project         *neonprov.Project
	ProjectID       pulumi.StringOutput
	DefaultBranchID pulumi.StringOutput
	RegionID        pulumi.StringOutput
	apiKey          pulumi.StringInput
	apiBaseURL      string
}

// ProjectRef provides typed cross-stack access to a shared neon.Project exported in infra/shared.
type ProjectRef struct {
	ProjectID       pulumi.StringOutput
	DefaultBranchID pulumi.StringOutput
	RegionID        pulumi.StringOutput
}

// NewProject provisions a shared Neon Project cluster on the shared stack (omitting env from canonical naming).
func NewProject(ctx *pulumi.Context, name string, args *ProjectArgs, opts ...pulumi.ResourceOption) (*Project, error) {
	if err := core.RequireSharedStack(ctx); err != nil {
		return nil, err
	}
	if args == nil {
		args = &ProjectArgs{}
	}
	name = defaultIdentifier(name, "neon")
	res := &Project{}
	meta, err := core.InitStateful(ctx, "openbase:neon:Project", "neon-project", res, &res.StatefulMeta, core.StatefulSpec{
		Name:        name,
		Project:     args.ProjectName,
		Repo:        args.Repo,
		Environment: string(core.EnvShared),
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
	res.DefaultBranchID = proj.Branch.Id().Elem()
	res.RegionID = proj.RegionId
	populateProjectAPISettings(args, res)
	res.PublishLink(ctx, core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"neonProjectId":       res.ProjectID,
			"neonDefaultBranchId": res.DefaultBranchID,
			"regionId":            res.RegionID,
		},
		StackExports: map[string]pulumi.Input{
			"neonProjectId":       res.ProjectID,
			"neonDefaultBranchId": res.DefaultBranchID,
			"regionId":            res.RegionID,
		},
	})
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"projectId":           res.ProjectID,
		"neonProjectId":       res.ProjectID,
		"defaultBranchId":     res.DefaultBranchID,
		"neonDefaultBranchId": res.DefaultBranchID,
		"regionId":            res.RegionID,
	})
}

// LookupProject resolves a shared neon.Project by alias name from an infra/shared ScopeRef.
func LookupProject(shared *core.ScopeRef, name string) *ProjectRef {
	if shared == nil {
		return nil
	}
	alias := []string{name}
	return &ProjectRef{
		ProjectID:       shared.ManifestAttr("neon-project", alias, "neonProjectId"),
		DefaultBranchID: shared.ManifestAttr("neon-project", alias, "neonDefaultBranchId"),
		RegionID:        shared.ManifestAttr("neon-project", alias, "regionId"),
	}
}

// Private helpers ------------------------------------------------------------

type neonProjectData struct {
	projectID       pulumi.StringOutput
	defaultBranchID pulumi.StringOutput
	apiKey          pulumi.StringInput
	apiBaseURL      string
}

func (p *Project) neonProjectValues() neonProjectData {
	if p == nil {
		return neonProjectData{}
	}
	return neonProjectData{
		projectID:       p.ProjectID,
		defaultBranchID: p.DefaultBranchID,
		apiKey:          p.apiKey,
		apiBaseURL:      p.apiBaseURL,
	}
}

func (r *ProjectRef) neonProjectValues() neonProjectData {
	if r == nil {
		return neonProjectData{}
	}
	return neonProjectData{
		projectID:       r.ProjectID,
		defaultBranchID: r.DefaultBranchID,
	}
}

func createNeonProject(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args *ProjectArgs,
	parent pulumi.Resource,
) (*neonprov.Project, error) {
	orgOut, regionOut := resolveNeonProjectAccountInputs(args)
	pgVer := defaultNeonInt(args.PostgresVersion, 17)
	retention := defaultNeonInt(args.HistoryRetentionSeconds, 86400)
	projArgs := &neonprov.ProjectArgs{
		Name:                    pulumi.String(meta.CanonicalName),
		RegionId:                regionOut,
		PgVersion:               pulumi.Float64(float64(pgVer)),
		HistoryRetentionSeconds: pulumi.Float64(float64(retention)),
		StorePassword:           pulumi.Bool(true),
		OrgId:                   nonEmptyNeonPtrOutput(orgOut),
	}
	projOpts := []pulumi.ResourceOption{pulumi.Parent(parent)}
	if args.ImportProjectID != "" {
		projOpts = append(projOpts, pulumi.Import(pulumi.ID(args.ImportProjectID)))
	}
	return neonprov.NewProject(ctx, name+"-project", projArgs, projOpts...)
}

func populateProjectAPISettings(args *ProjectArgs, res *Project) {
	res.apiKey = args.APIKey
	res.apiBaseURL = args.APIBaseURL
	if args.Account != nil {
		acc := args.Account.neonAccountValues()
		if res.apiKey == nil {
			res.apiKey = acc.apiKey
		}
		if res.apiBaseURL == "" {
			res.apiBaseURL = acc.apiBaseURL
		}
	}
}

func resolveNeonProjectAccountInputs(args *ProjectArgs) (pulumi.StringOutput, pulumi.StringOutput) {
	orgOut := pulumi.String(args.OrgID).ToStringOutput()
	regionOut := pulumi.String(defaultIdentifier(args.RegionID, "aws-us-east-1")).ToStringOutput()
	if args.Account == nil {
		return orgOut, regionOut
	}
	acc := args.Account.neonAccountValues()
	if args.OrgID == "" && isSet(acc.orgID) {
		orgOut = acc.orgID
	}
	if args.RegionID == "" && isSet(acc.defaultRegionID) {
		regionOut = defaultOutputStr(acc.defaultRegionID, "aws-us-east-1")
	}
	return orgOut, regionOut
}

func defaultOutputStr(out pulumi.StringOutput, fallback string) pulumi.StringOutput {
	return out.ApplyT(func(s string) string {
		if s != "" {
			return s
		}
		return fallback
	}).(pulumi.StringOutput)
}

func nonEmptyNeonPtrOutput(out pulumi.StringOutput) pulumi.StringPtrOutput {
	return out.ApplyT(func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}).(pulumi.StringPtrOutput)
}

func defaultNeonInt(explicit, fallback int) int {
	if explicit != 0 {
		return explicit
	}
	return fallback
}
