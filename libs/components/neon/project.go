package neon

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-neon-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatefulResource = (*Project)(nil)

// ProjectArgs configures an application's shared Neon Project cluster in Premise's infra/shared stack.
type ProjectArgs struct {
	Project                 string
	Repo                    string
	Platform                *Platform
	PlatformStack           *core.ScopeRef
	PlatformStackRef        string
	OrgID                   string
	RegionID                string
	APIKey                  pulumi.StringInput
	APIBaseURL              string
	PostgresVersion         int
	HistoryRetentionSeconds int
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
		Project:     args.Project,
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

// Private helpers ------------------------------------------------------------

func createNeonProject(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args *ProjectArgs,
	parent pulumi.Resource,
) (*neonprov.Project, error) {
	orgOut, regionOut, err := resolveNeonProjectPlatformInputs(ctx, name, args)
	if err != nil {
		return nil, err
	}
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
	return neonprov.NewProject(ctx, name+"-project", projArgs, pulumi.Parent(parent))
}

func populateProjectAPISettings(args *ProjectArgs, res *Project) {
	res.apiKey = args.APIKey
	res.apiBaseURL = args.APIBaseURL
	if args.Platform != nil {
		if res.apiKey == nil {
			res.apiKey = args.Platform.apiKey
		}
		if res.apiBaseURL == "" {
			res.apiBaseURL = args.Platform.apiBaseURL
		}
	}
}

func resolveNeonProjectPlatformInputs(
	ctx *pulumi.Context,
	name string,
	args *ProjectArgs,
) (pulumi.StringOutput, pulumi.StringOutput, error) {
	orgOut := pulumi.String(args.OrgID).ToStringOutput()
	regionOut := pulumi.String(defaultIdentifier(args.RegionID, "aws-us-east-1")).ToStringOutput()
	if args.Platform != nil {
		if args.OrgID == "" {
			orgOut = args.Platform.OrgID
		}
		if args.RegionID == "" {
			regionOut = args.Platform.DefaultRegionID
		}
		return orgOut, regionOut, nil
	}
	sr, err := core.ResolveStackReference(ctx, name+"-platform-ref", args.PlatformStack, args.PlatformStackRef)
	if err != nil || sr == nil {
		return orgOut, regionOut, err
	}
	if args.OrgID == "" {
		orgOut = optionalNeonStackStr(sr, "neonOrgId", "")
	}
	if args.RegionID == "" {
		regionOut = optionalNeonStackStr(sr, "neonDefaultRegionId", "aws-us-east-1")
	}
	return orgOut, regionOut, nil
}

func optionalNeonStackStr(sr *pulumi.StackReference, key, fallback string) pulumi.StringOutput {
	return sr.GetOutput(pulumi.String(key)).ApplyT(func(v any) string {
		if s, ok := v.(string); ok && s != "" {
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
