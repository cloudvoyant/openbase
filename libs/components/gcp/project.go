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

var _ core.StatelessResource = (*ProjectResource)(nil)

// Project declares an application's shared GCP assets (Docker Artifact Registry repository, reusing gcp.Platform WIF/APIs/LB when supplied) in Premise's infra/shared stack.
type Project struct {
	Name               string
	Project            string
	Repo               string
	Environment        string
	Platform           *PlatformResource
	PlatformStackRef   string
	GCPProject         pulumi.StringPtrInput
	Location           string
	PoolSuffix         string
	GitHubOrg          string
	GitHubRepo         string
	VercelTeamSlug     string
	VercelIssuerMode   string
	BaseDomain         string
	EnableLoadBalancer bool
	Services           []string
	SkipKeyRing        bool
	ExtraLabels        map[string]string
}

// ProjectResource is the provisioned handle for an application's Artifact Registry repository and inherited or inline GCP Platform roots.
type ProjectResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	InlinePlatform           *PlatformResource
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
	env := defaultStr(p.Environment, string(core.EnvShared))
	res := &ProjectResource{}
	meta, err := core.InitStateless(ctx, "openbase:gcp:Project", "gcp-project", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        p.Name,
		Project:     p.Project,
		Repo:        p.Repo,
		Environment: env,
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
		Env: map[string]pulumi.StringOutput{
			"GCP_ARTIFACT_REGISTRY_URL":  res.RepositoryURL,
			"GCP_WORKLOAD_IDENTITY_POOL": res.WorkloadIdentityPoolName,
		},
	})
	return res, registerGCPProjectOutputs(ctx, res)
}

// Private helpers ------------------------------------------------------------

func provisionGCPProject(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec Project, res *ProjectResource) error {
	location := defaultStr(spec.Location, "us-central1")
	if err := attachOrProvisionPlatform(ctx, name, location, meta, spec, res); err != nil {
		return err
	}
	repo, err := createArtifactRepository(ctx, name, location, meta, spec, res)
	if err != nil {
		return err
	}
	res.Repository = repo
	res.RepositoryID = repo.RepositoryId
	res.RepositoryURL = pulumi.Sprintf("%s-docker.pkg.dev/%s/%s", location, repo.Project, repo.RepositoryId)
	return nil
}

func attachOrProvisionPlatform(
	ctx *pulumi.Context,
	name, location string,
	meta core.ResourceMeta,
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
	plat, err := Platform{
		Name:               name + "-platform",
		Project:            meta.Project,
		Repo:               meta.Repo,
		Environment:        string(meta.Environment),
		GCPProject:         spec.GCPProject,
		Location:           location,
		PoolSuffix:         spec.PoolSuffix,
		GitHubOrg:          spec.GitHubOrg,
		GitHubRepo:         spec.GitHubRepo,
		VercelTeamSlug:     spec.VercelTeamSlug,
		VercelIssuerMode:   spec.VercelIssuerMode,
		BaseDomain:         spec.BaseDomain,
		EnableLoadBalancer: spec.EnableLoadBalancer,
		Services:           spec.Services,
		SkipKeyRing:        spec.SkipKeyRing,
		ExtraLabels:        spec.ExtraLabels,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.InlinePlatform = plat
	copyPlatformOutputsToProject(plat, spec.BaseDomain, res)
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
	meta core.ResourceMeta,
	spec Project,
	parent pulumi.Resource,
) (*artifactregistry.Repository, error) {
	var deps []pulumi.Resource
	for _, s := range parent.(*ProjectResource).EnabledServices {
		deps = append(deps, s)
	}
	return artifactregistry.NewRepository(ctx, name+"-ar", &artifactregistry.RepositoryArgs{
		Project:      spec.GCPProject,
		Location:     pulumi.String(location),
		RepositoryId: pulumi.String(core.BoundedName(meta.CanonicalName, 63)),
		Format:       pulumi.String("DOCKER"),
		Labels:       core.GCPLabels(meta, spec.ExtraLabels),
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
