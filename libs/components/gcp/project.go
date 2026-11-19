package gcp

import (
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/artifactregistry"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/iam"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/kms"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/projects"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.ProjectResource = (*ProjectResource)(nil)

// Project declares an application's shared GCP project assets (Docker Artifact Registry repository, optionally linked to gcp.Platform) in Premise's infra/shared stack.
type Project struct {
	Name             string
	Project          string
	Repo             string
	Platform         *PlatformResource
	PlatformStackRef string
	GCPProject       pulumi.StringPtrInput
	Location         string
	BaseDomain       string
	ExtraLabels      map[string]string
}

// ProjectResource is the provisioned handle for an application's Artifact Registry repository and inherited GCP Platform references.
type ProjectResource struct {
	pulumi.ResourceState
	core.ProjectMeta

	EnabledServices          []*projects.Service
	Repository               *artifactregistry.Repository
	WorkloadIdentityPool     *iam.WorkloadIdentityPool
	GitHubOIDCProvider       *iam.WorkloadIdentityPoolProvider
	VercelOIDCProvider       *iam.WorkloadIdentityPoolProvider
	KeyRing                  *kms.KeyRing
	LoadBalancer             *CloudLoadBalancerResource
	BaseDomain               pulumi.StringOutput
	RepositoryID             pulumi.StringOutput
	RepositoryURL            pulumi.StringOutput
	WorkloadIdentityPoolName pulumi.StringOutput
	VercelOIDCProviderName   pulumi.StringOutput
	KeyRingID                pulumi.StringOutput
}

// Register provisions per-application shared GCP resources in Premise's infra/shared stack.
func (p Project) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*ProjectResource, error) {
	p.Name = defaultStr(p.Name, "gcp")
	res := &ProjectResource{}
	info, err := core.InitProject(ctx, "openbase:gcp:Project", "gcp-project", res, &res.ProjectMeta, core.ProjectSpec{
		Name:    p.Name,
		Project: p.Project,
		Repo:    p.Repo,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionGCPProject(ctx, p.Name, info, p, res); err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: info.Name,
		Kind: info.Kind,
		Env: map[string]pulumi.StringOutput{
			"GCP_ARTIFACT_REGISTRY_URL":  res.RepositoryURL,
			"GCP_WORKLOAD_IDENTITY_POOL": res.WorkloadIdentityPoolName,
		},
	})
	return res, registerGCPProjectOutputs(ctx, res)
}

// Private helpers ------------------------------------------------------------

func provisionGCPProject(ctx *pulumi.Context, name string, info core.ProjectInfo, spec Project, res *ProjectResource) error {
	location := defaultStr(spec.Location, "us-central1")
	if err := bindPlatformRefsToProject(ctx, name, spec, res); err != nil {
		return err
	}
	repo, err := createArtifactRepository(ctx, name, location, info, spec, res)
	if err != nil {
		return err
	}
	res.Repository = repo
	res.RepositoryID = repo.RepositoryId
	res.RepositoryURL = pulumi.Sprintf("%s-docker.pkg.dev/%s/%s", location, repo.Project, repo.RepositoryId)
	return nil
}

func bindPlatformRefsToProject(
	ctx *pulumi.Context,
	name string,
	spec Project,
	res *ProjectResource,
) error {
	if spec.Platform != nil {
		copyPlatformOutputsToProject(spec.Platform, spec.BaseDomain, res)
		return nil
	}
	if spec.PlatformStackRef != "" {
		return loadPlatformStackRefIntoProject(ctx, name, spec, res)
	}
	res.WorkloadIdentityPoolName = pulumi.String("").ToStringOutput()
	res.VercelOIDCProviderName = pulumi.String("").ToStringOutput()
	res.KeyRingID = pulumi.String("").ToStringOutput()
	res.BaseDomain = pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()
	return nil
}

func copyPlatformOutputsToProject(plat *PlatformResource, explicitBase string, res *ProjectResource) {
	res.EnabledServices = plat.EnabledServices
	res.WorkloadIdentityPool = plat.WorkloadIdentityPool
	res.GitHubOIDCProvider = plat.GitHubOIDCProvider
	res.VercelOIDCProvider = plat.VercelOIDCProvider
	res.KeyRing = plat.KeyRing
	res.LoadBalancer = plat.LoadBalancer
	res.WorkloadIdentityPoolName = plat.WorkloadIdentityPoolName
	res.VercelOIDCProviderName = plat.VercelOIDCProviderName
	res.KeyRingID = plat.KeyRingID
	res.BaseDomain = plat.BaseDomain
	if core.NormalizeDomain(explicitBase) != "" {
		res.BaseDomain = pulumi.String(core.NormalizeDomain(explicitBase)).ToStringOutput()
	}
}

func loadPlatformStackRefIntoProject(ctx *pulumi.Context, name string, spec Project, res *ProjectResource) error {
	sr, err := pulumi.NewStackReference(ctx, name+"-platform-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(spec.PlatformStackRef)})
	if err != nil {
		return err
	}
	res.WorkloadIdentityPoolName = optionalGCPStackStr(sr, "workloadIdentityPoolName")
	res.VercelOIDCProviderName = optionalGCPStackStr(sr, "vercelOidcProviderName")
	res.KeyRingID = optionalGCPStackStr(sr, "keyRingId")
	if core.NormalizeDomain(spec.BaseDomain) != "" {
		res.BaseDomain = pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()
	} else {
		res.BaseDomain = preferGCPStackStr(sr, "gcpBaseDomain", "baseDomain")
	}
	return nil
}

func optionalGCPStackStr(sr *pulumi.StackReference, key string) pulumi.StringOutput {
	return sr.GetOutput(pulumi.String(key)).ApplyT(func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}).(pulumi.StringOutput)
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

func createArtifactRepository(
	ctx *pulumi.Context,
	name, location string,
	info core.ProjectInfo,
	spec Project,
	parent *ProjectResource,
) (*artifactregistry.Repository, error) {
	var deps []pulumi.Resource
	for _, s := range parent.EnabledServices {
		deps = append(deps, s)
	}
	return artifactregistry.NewRepository(ctx, name+"-ar", &artifactregistry.RepositoryArgs{
		Project:      spec.GCPProject,
		Location:     pulumi.String(location),
		RepositoryId: pulumi.String(core.BoundedName(info.CanonicalName, 63)),
		Format:       pulumi.String("DOCKER"),
		Labels:       core.GCPProjectLabels(info, spec.ExtraLabels),
	}, pulumi.Parent(parent), pulumi.DependsOn(deps))
}

func registerGCPProjectOutputs(ctx *pulumi.Context, res *ProjectResource) error {
	outs := pulumi.Map{
		"baseDomain":               res.BaseDomain,
		"repositoryId":             res.RepositoryID,
		"repositoryUrl":            res.RepositoryURL,
		"workloadIdentityPoolName": res.WorkloadIdentityPoolName,
		"vercelOidcProviderName":   res.VercelOIDCProviderName,
		"keyRingId":                res.KeyRingID,
	}
	if res.LoadBalancer != nil {
		outs["loadBalancerIp"] = res.LoadBalancer.IPAddress
		outs["dnsAuthRecordName"] = res.LoadBalancer.DnsAuthRecordName
		outs["dnsAuthRecordData"] = res.LoadBalancer.DnsAuthRecordData
	}
	return ctx.RegisterResourceOutputs(res, outs)
}
