package neon

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-neon-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatefulResource               = (*ProjectResource)(nil)
	_ core.ScopedSpecOf[*ProjectResource] = Project{}
)

// Project declares an application's shared Neon Project cluster in Premise's infra/shared stack.
type Project struct {
	Name                    string
	Project                 string
	Repo                    string
	Platform                *PlatformResource
	PlatformStackRef        string
	OrgID                   string
	RegionID                string
	APIKey                  pulumi.StringInput
	APIBaseURL              string
	PostgresVersion         int
	HistoryRetentionSeconds int
}

// ProjectResource is the provisioned Neon Project cluster handle consumed by per-environment neon.Database branches.
type ProjectResource struct {
	pulumi.ResourceState
	core.StatefulMeta

	Project         *neonprov.Project
	ProjectID       pulumi.StringOutput
	DefaultBranchID pulumi.StringOutput
	RegionID        pulumi.StringOutput
	apiKey          pulumi.StringInput
	apiBaseURL      string
}

// Register provisions a shared Neon Project cluster (Environment: shared, omitting env from canonical naming).
func (p Project) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	p.Name = defaultIdentifier(p.Name, "neon")
	res := &ProjectResource{}
	meta, err := core.InitStateful(ctx, "openbase:neon:Project", "neon-project", res, &res.StatefulMeta, core.StatefulSpec{
		Name:        p.Name,
		Project:     p.Project,
		Repo:        p.Repo,
		Environment: string(core.EnvShared),
	}, opts...)
	if err != nil {
		return nil, err
	}
	proj, err := createNeonProject(ctx, p.Name, meta, p, res)
	if err != nil {
		return nil, err
	}
	res.Project = proj
	res.ProjectID = proj.ID().ToStringOutput()
	res.DefaultBranchID = proj.Branch.Id().Elem()
	res.RegionID = proj.RegionId
	populateProjectAPISettings(p, res)
	res.SetLink(core.LinkDescriptor{
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

// RegisterInScope adapts neon.Project to openbase.Project.
func (p Project) RegisterInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.Resource, error) {
	return p.RegisterTypedInScope(ctx, scope, opts...)
}

// RegisterTypedInScope registers neon.Project with inherited Project and PlatformRef defaults.
func (p Project) RegisterTypedInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	if p.Project == "" {
		p.Project = scope.Project
	}
	if p.Repo == "" {
		p.Repo = scope.Repo
	}
	if p.Platform == nil && scope.Platform != nil {
		if plat, ok := scope.Platform.PlatformHandle("neon-platform").(*PlatformResource); ok {
			p.Platform = plat
		}
	}
	if p.PlatformStackRef == "" {
		p.PlatformStackRef = scope.PlatformStackName()
	}
	return p.Register(ctx, opts...)
}

// Private helpers ------------------------------------------------------------

func createNeonProject(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Project,
	parent pulumi.Resource,
) (*neonprov.Project, error) {
	orgOut, regionOut, err := resolveNeonProjectPlatformInputs(ctx, name, spec)
	if err != nil {
		return nil, err
	}
	pgVer := defaultNeonInt(spec.PostgresVersion, 17)
	retention := defaultNeonInt(spec.HistoryRetentionSeconds, 86400)
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

func populateProjectAPISettings(spec Project, res *ProjectResource) {
	res.apiKey = spec.APIKey
	res.apiBaseURL = spec.APIBaseURL
	if spec.Platform != nil {
		if res.apiKey == nil {
			res.apiKey = spec.Platform.apiKey
		}
		if res.apiBaseURL == "" {
			res.apiBaseURL = spec.Platform.apiBaseURL
		}
	}
}

func resolveNeonProjectPlatformInputs(
	ctx *pulumi.Context,
	name string,
	spec Project,
) (pulumi.StringOutput, pulumi.StringOutput, error) {
	orgOut := pulumi.String(spec.OrgID).ToStringOutput()
	regionOut := pulumi.String(defaultIdentifier(spec.RegionID, "aws-us-east-1")).ToStringOutput()
	if spec.Platform != nil {
		if spec.OrgID == "" {
			orgOut = spec.Platform.OrgID
		}
		if spec.RegionID == "" {
			regionOut = spec.Platform.DefaultRegionID
		}
	}
	if spec.PlatformStackRef == "" {
		return orgOut, regionOut, nil
	}
	sr, err := pulumi.NewStackReference(ctx, name+"-platform-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(spec.PlatformStackRef)})
	if err != nil {
		return pulumi.StringOutput{}, pulumi.StringOutput{}, err
	}
	if spec.OrgID == "" && spec.Platform == nil {
		orgOut = optionalNeonStackStr(sr, "neonOrgId", "")
	}
	if spec.RegionID == "" && spec.Platform == nil {
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
