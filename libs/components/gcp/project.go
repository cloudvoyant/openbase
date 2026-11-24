package gcp

import (
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/artifactregistry"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatelessResource = (*Project)(nil)
	_ ProjectInput           = (*Project)(nil)
	_ ProjectInput           = (*ProjectRef)(nil)
)

// ProjectInput is satisfied by in-memory *Project handles and cross-stack *ProjectRef lookups.
type ProjectInput interface {
	gcpProjectValues() gcpProjectData
}

// ProjectArgs configures an application's shared Docker Artifact Registry repository in Premise's infra/shared stack.
type ProjectArgs struct {
	ProjectName        string
	Repo               string
	Account            AccountInput
	GCPProject         pulumi.StringPtrInput
	Location           string
	BaseDomain         string
	ImportRepositoryID string
	ExtraLabels        map[string]string
}

// Project is the provisioned handle for an application's shared Docker Artifact Registry repository.
type Project struct {
	pulumi.ResourceState
	core.StatelessMeta

	Repository    *artifactregistry.Repository
	BaseDomain    pulumi.StringOutput
	RepositoryID  pulumi.StringOutput
	RepositoryURL pulumi.StringOutput
}

// ProjectRef provides typed cross-stack access to a shared gcp.Project exported in infra/shared.
type ProjectRef struct {
	BaseDomain    pulumi.StringOutput
	RepositoryID  pulumi.StringOutput
	RepositoryURL pulumi.StringOutput
}

// NewProject provisions per-application shared GCP Artifact Registry resources on the shared stack.
func NewProject(ctx *pulumi.Context, name string, args *ProjectArgs, opts ...pulumi.ResourceOption) (*Project, error) {
	if err := core.RequireSharedStack(ctx); err != nil {
		return nil, err
	}
	if args == nil {
		args = &ProjectArgs{}
	}
	name = defaultStr(name, "gcp")
	res := &Project{}
	meta, err := core.InitStateless(ctx, "openbase:gcp:Project", "gcp-project", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        name,
		Project:     args.ProjectName,
		Repo:        args.Repo,
		Environment: string(core.EnvShared),
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionGCPProject(ctx, name, meta, *args, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, core.LinkDescriptor{
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

// LookupProject resolves a shared gcp.Project by alias name from an infra/shared ScopeRef.
func LookupProject(shared *core.ScopeRef, name string) *ProjectRef {
	if shared == nil {
		return nil
	}
	alias := []string{name}
	return &ProjectRef{
		BaseDomain:    shared.ManifestAttr("gcp-project", alias, "baseDomain"),
		RepositoryID:  shared.ManifestAttr("gcp-project", alias, "repositoryId"),
		RepositoryURL: shared.ManifestAttr("gcp-project", alias, "repositoryUrl"),
	}
}

// Private helpers ------------------------------------------------------------

type gcpProjectData struct {
	baseDomain   pulumi.StringOutput
	repositoryID pulumi.StringOutput
}

func (p *Project) gcpProjectValues() gcpProjectData {
	if p == nil {
		return gcpProjectData{}
	}
	return gcpProjectData{
		baseDomain:   p.BaseDomain,
		repositoryID: p.RepositoryID,
	}
}

func (r *ProjectRef) gcpProjectValues() gcpProjectData {
	if r == nil {
		return gcpProjectData{}
	}
	return gcpProjectData{
		baseDomain:   r.BaseDomain,
		repositoryID: r.RepositoryID,
	}
}

func provisionGCPProject(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec ProjectArgs, res *Project) error {
	location := defaultStr(spec.Location, "us-central1")
	baseDomain, gcpProj, deps := resolveProjectAccountInputs(spec)
	res.BaseDomain = baseDomain
	repoOpts := []pulumi.ResourceOption{pulumi.Parent(res), pulumi.DependsOn(deps)}
	if spec.ImportRepositoryID != "" {
		repoOpts = append(repoOpts, pulumi.Import(pulumi.ID(spec.ImportRepositoryID)))
	}
	repo, err := artifactregistry.NewRepository(ctx, name+"-ar", &artifactregistry.RepositoryArgs{
		Project:      gcpProj,
		Location:     pulumi.String(location),
		RepositoryId: pulumi.String(core.BoundedName(meta.CanonicalName, 63)),
		Format:       pulumi.String("DOCKER"),
		Labels:       core.GCPLabels(meta, spec.ExtraLabels),
	}, repoOpts...)
	if err != nil {
		return err
	}
	res.Repository = repo
	res.RepositoryID = repo.RepositoryId
	res.RepositoryURL = pulumi.Sprintf("%s-docker.pkg.dev/%s/%s", location, repo.Project, repo.RepositoryId)
	return nil
}

func resolveProjectAccountInputs(spec ProjectArgs) (pulumi.StringOutput, pulumi.StringPtrInput, []pulumi.Resource) {
	explicitBase := pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()
	if spec.Account == nil {
		return explicitBase, spec.GCPProject, nil
	}
	acc := spec.Account.gcpAccountValues()
	var deps []pulumi.Resource
	if acc.local != nil {
		for _, svc := range acc.local.EnabledServices {
			deps = append(deps, svc)
		}
	}
	gcpProj := spec.GCPProject
	if gcpProj == nil && isOutputSet(acc.gcpProject) {
		gcpProj = nonEmptyStrPtrOutput(acc.gcpProject)
	}
	if core.NormalizeDomain(spec.BaseDomain) == "" && isOutputSet(acc.baseDomain) {
		return acc.baseDomain, gcpProj, deps
	}
	return explicitBase, gcpProj, deps
}

func nonEmptyStrPtrOutput(out pulumi.StringOutput) pulumi.StringPtrOutput {
	return out.ApplyT(func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}).(pulumi.StringPtrOutput)
}
