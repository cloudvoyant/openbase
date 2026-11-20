package gcp

import (
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/artifactregistry"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatelessResource              = (*ProjectResource)(nil)
	_ core.ScopedSpecOf[*ProjectResource] = Project{}
)

// Project declares an application's shared Docker Artifact Registry repository in Premise's infra/shared stack.
type Project struct {
	Name             string
	Project          string
	Repo             string
	Platform         *PlatformResource
	PlatformStackRef string
	PlatformResource string
	GCPProject       pulumi.StringPtrInput
	Location         string
	BaseDomain       string
	ExtraLabels      map[string]string
}

// ProjectResource is the provisioned handle for an application's shared Docker Artifact Registry repository.
type ProjectResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	Repository    *artifactregistry.Repository
	BaseDomain    pulumi.StringOutput
	RepositoryID  pulumi.StringOutput
	RepositoryURL pulumi.StringOutput
}

// Register provisions per-application shared GCP Artifact Registry resources in Premise's infra/shared stack.
func (p Project) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	p.Name = defaultStr(p.Name, "gcp")
	res := &ProjectResource{}
	meta, err := core.InitStateless(ctx, "openbase:gcp:Project", "gcp-project", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        p.Name,
		Project:     p.Project,
		Repo:        p.Repo,
		Environment: string(core.EnvShared),
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionGCPProject(ctx, p.Name, meta, p, res); err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"baseDomain":    res.BaseDomain,
			"repositoryId":  res.RepositoryID,
			"repositoryUrl": res.RepositoryURL,
		},
		StackExports: map[string]pulumi.Input{
			"baseDomain":    res.BaseDomain,
			"repositoryId":  res.RepositoryID,
			"repositoryUrl": res.RepositoryURL,
		},
	})
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"baseDomain":    res.BaseDomain,
		"repositoryId":  res.RepositoryID,
		"repositoryUrl": res.RepositoryURL,
	})
}

// RegisterInScope adapts gcp.Project to openbase.Project.
func (p Project) RegisterInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.Resource, error) {
	return p.RegisterTypedInScope(ctx, scope, opts...)
}

// RegisterTypedInScope registers gcp.Project with inherited Project and PlatformRef defaults.
func (p Project) RegisterTypedInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	if p.Project == "" {
		p.Project = scope.Project
	}
	if p.Repo == "" {
		p.Repo = scope.Repo
	}
	if p.Platform == nil && scope.Platform != nil {
		p.Platform = resolveInMemoryGCPPlatform(scope.Platform, p.PlatformResource)
	}
	if p.PlatformStackRef == "" {
		p.PlatformStackRef = scope.PlatformStackName()
	}
	return p.Register(ctx, opts...)
}

// Private helpers ------------------------------------------------------------

func resolveInMemoryGCPPlatform(ref *core.PlatformRef, resName string) *PlatformResource {
	if resName != "" {
		if plat, ok := ref.PlatformHandleByName(resName).(*PlatformResource); ok {
			return plat
		}
		return nil
	}
	plat, _ := ref.PlatformHandle("gcp-platform").(*PlatformResource)
	return plat
}

func provisionGCPProject(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec Project, res *ProjectResource) error {
	location := defaultStr(spec.Location, "us-central1")
	baseDomain, gcpProj, deps, err := resolveProjectPlatformInputs(ctx, name, spec)
	if err != nil {
		return err
	}
	res.BaseDomain = baseDomain
	repo, err := artifactregistry.NewRepository(ctx, name+"-ar", &artifactregistry.RepositoryArgs{
		Project:      gcpProj,
		Location:     pulumi.String(location),
		RepositoryId: pulumi.String(core.BoundedName(meta.CanonicalName, 63)),
		Format:       pulumi.String("DOCKER"),
		Labels:       core.GCPLabels(meta, spec.ExtraLabels),
	}, pulumi.Parent(res), pulumi.DependsOn(deps))
	if err != nil {
		return err
	}
	res.Repository = repo
	res.RepositoryID = repo.RepositoryId
	res.RepositoryURL = pulumi.Sprintf("%s-docker.pkg.dev/%s/%s", location, repo.Project, repo.RepositoryId)
	return nil
}

func resolveProjectPlatformInputs(
	ctx *pulumi.Context,
	name string,
	spec Project,
) (pulumi.StringOutput, pulumi.StringPtrInput, []pulumi.Resource, error) {
	if spec.Platform != nil {
		return resolveDirectPlatformInputs(spec)
	}
	if spec.PlatformStackRef == "" {
		return pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput(), spec.GCPProject, nil, nil
	}
	return resolveRemotePlatformInputs(ctx, name, spec)
}

func resolveDirectPlatformInputs(spec Project) (pulumi.StringOutput, pulumi.StringPtrInput, []pulumi.Resource, error) {
	deps := make([]pulumi.Resource, 0, len(spec.Platform.EnabledServices))
	for _, svc := range spec.Platform.EnabledServices {
		deps = append(deps, svc)
	}
	gcpProj := spec.GCPProject
	if gcpProj == nil {
		if out, ok := spec.Platform.Link().Attributes["gcpProject"]; ok {
			gcpProj = nonEmptyStrPtrOutput(out)
		}
	}
	if core.NormalizeDomain(spec.BaseDomain) == "" {
		return spec.Platform.BaseDomain, gcpProj, deps, nil
	}
	return pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput(), gcpProj, deps, nil
}

func resolveRemotePlatformInputs(
	ctx *pulumi.Context,
	name string,
	spec Project,
) (pulumi.StringOutput, pulumi.StringPtrInput, []pulumi.Resource, error) {
	sr, err := pulumi.NewStackReference(ctx, name+"-platform-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(spec.PlatformStackRef)})
	if err != nil {
		return pulumi.StringOutput{}, nil, nil, err
	}
	gcpProj := spec.GCPProject
	if gcpProj == nil {
		gcpProj = resolveStackGCPProjectPtr(sr, spec.PlatformResource)
	}
	if core.NormalizeDomain(spec.BaseDomain) != "" {
		return pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput(), gcpProj, nil, nil
	}
	if spec.PlatformResource != "" {
		return core.LookupManifestAttr(sr, spec.PlatformResource, "baseDomain"), gcpProj, nil, nil
	}
	return preferGCPStackStr(sr, "gcpBaseDomain", "baseDomain"), gcpProj, nil, nil
}

func resolveStackGCPProjectPtr(sr *pulumi.StackReference, platResource string) pulumi.StringPtrOutput {
	if platResource != "" {
		return nonEmptyStrPtrOutput(core.LookupManifestAttr(sr, platResource, "gcpProject"))
	}
	return nonEmptyGCPPtr(sr, "gcpProject")
}

func nonEmptyStrPtrOutput(out pulumi.StringOutput) pulumi.StringPtrOutput {
	return out.ApplyT(func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}).(pulumi.StringPtrOutput)
}

func nonEmptyGCPPtr(sr *pulumi.StackReference, key string) pulumi.StringPtrOutput {
	return sr.GetOutput(pulumi.String(key)).ApplyT(func(v any) *string {
		if s, ok := v.(string); ok && s != "" {
			return &s
		}
		return nil
	}).(pulumi.StringPtrOutput)
}

func preferGCPStackStr(sr *pulumi.StackReference, primaryKey, fallbackKey string) pulumi.StringOutput {
	return pulumi.All(
		sr.GetOutput(pulumi.String(primaryKey)),
		sr.GetOutput(pulumi.String(fallbackKey)),
	).ApplyT(func(vs []any) string {
		if first, ok := vs[0].(string); ok && first != "" {
			return first
		}
		if second, ok := vs[1].(string); ok {
			return second
		}
		return ""
	}).(pulumi.StringOutput)
}
