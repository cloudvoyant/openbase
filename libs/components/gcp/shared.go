// Package gcp provides OpenBase Pulumi components for Google Cloud Platform.
package gcp

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/artifactregistry"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/iam"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/kms"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/projects"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*SharedResource)(nil)

// Shared declares cross-environment GCP prerequisites in the "shared" stack.
type Shared struct {
	Name               string
	Project            string
	Repo               string
	Environment        string
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

// SharedResource is the provisioned handle for Artifact Registry, WIF, KMS, and required GCP APIs.
type SharedResource struct {
	pulumi.ResourceState
	core.StatelessMeta

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

// Register provisions cross-environment GCP prerequisites for consumption via StackReference.
func (s Shared) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*SharedResource, error) {
	s.Name = defaultStr(s.Name, "shared")
	env := defaultStr(s.Environment, string(core.EnvShared))
	res := &SharedResource{}
	meta, err := core.InitStateless(ctx, "openbase:gcp:Shared", "gcp-shared", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        s.Name,
		Project:     s.Project,
		Repo:        s.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionSharedResources(ctx, s.Name, meta, s, res); err != nil {
		return nil, err
	}
	res.SetLink(buildSharedLink(meta, res))
	if err := registerSharedOutputs(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

var defaultSharedServices = []string{
	"run.googleapis.com",
	"artifactregistry.googleapis.com",
	"iam.googleapis.com",
	"iamcredentials.googleapis.com",
	"sts.googleapis.com",
	"storage.googleapis.com",
	"secretmanager.googleapis.com",
	"cloudkms.googleapis.com",
}

func provisionSharedResources(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec Shared, res *SharedResource) error {
	location := defaultStr(spec.Location, "us-central1")
	svcs, err := enableProjectServices(ctx, name, spec, res)
	if err != nil {
		return err
	}
	res.EnabledServices = svcs

	repo, err := createArtifactRepository(ctx, name, location, meta, spec, res)
	if err != nil {
		return err
	}
	res.Repository = repo
	res.RepositoryID = repo.RepositoryId
	res.RepositoryURL = pulumi.Sprintf("%s-docker.pkg.dev/%s/%s", location, repo.Project, repo.RepositoryId)

	if err := createIdentityFederation(ctx, name, meta, spec, res); err != nil {
		return err
	}
	if err := createOptionalKeyRing(ctx, name, location, meta, spec, res); err != nil {
		return err
	}
	res.BaseDomain = pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()
	if !spec.EnableLoadBalancer || core.NormalizeDomain(spec.BaseDomain) == "" {
		return nil
	}
	lb, err := CloudLoadBalancer{
		Name:        name + "-lb",
		Project:     meta.Project,
		Repo:        meta.Repo,
		Environment: string(meta.Environment),
		GCPProject:  spec.GCPProject,
		Location:    location,
		BaseDomain:  spec.BaseDomain,
		ExtraLabels: spec.ExtraLabels,
	}.Register(ctx, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.LoadBalancer = lb
	return nil
}

func enableProjectServices(ctx *pulumi.Context, name string, spec Shared, parent pulumi.Resource) ([]*projects.Service, error) {
	list := spec.Services
	if len(list) == 0 {
		list = defaultSharedServices
	}
	out := make([]*projects.Service, 0, len(list))
	for _, api := range list {
		short := strings.TrimSuffix(api, ".googleapis.com")
		svc, err := projects.NewService(ctx, fmt.Sprintf("%s-api-%s", name, short), &projects.ServiceArgs{
			Project:                  spec.GCPProject,
			Service:                  pulumi.String(api),
			DisableOnDestroy:         pulumi.Bool(false),
			DisableDependentServices: pulumi.Bool(false),
		}, pulumi.Parent(parent))
		if err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	return out, nil
}

func createArtifactRepository(
	ctx *pulumi.Context,
	name, location string,
	meta core.ResourceMeta,
	spec Shared,
	parent pulumi.Resource,
) (*artifactregistry.Repository, error) {
	repoID := core.BoundedName(meta.CanonicalName, 63)
	return artifactregistry.NewRepository(ctx, name+"-ar", &artifactregistry.RepositoryArgs{
		Project:      spec.GCPProject,
		Location:     pulumi.String(location),
		RepositoryId: pulumi.String(repoID),
		Format:       pulumi.String("DOCKER"),
		Description:  pulumi.String("OpenBase shared container registry for " + meta.Project),
		Labels:       core.GCPLabels(meta, spec.ExtraLabels),
	}, pulumi.Parent(parent))
}

func createIdentityFederation(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec Shared, res *SharedResource) error {
	rawPool := "ob-" + meta.Project + "-pool"
	if spec.PoolSuffix != "" {
		rawPool += "-" + spec.PoolSuffix
	}
	poolID := core.BoundedName(rawPool, 32)
	pool, err := iam.NewWorkloadIdentityPool(ctx, name+"-wif", &iam.WorkloadIdentityPoolArgs{
		Project:                spec.GCPProject,
		WorkloadIdentityPoolId: pulumi.String(poolID),
		DisplayName:            pulumi.String(core.BoundedName(meta.Project+"-wif", 32)),
		Description:            pulumi.String("OpenBase OIDC pool for " + meta.Project),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.WorkloadIdentityPool = pool
	res.WorkloadIdentityPoolName = pool.Name

	if err := createGitHubProvider(ctx, name, spec, pool, res); err != nil {
		return err
	}
	return createVercelProvider(ctx, name, spec, pool, res)
}

func createGitHubProvider(
	ctx *pulumi.Context,
	name string,
	spec Shared,
	pool *iam.WorkloadIdentityPool,
	res *SharedResource,
) error {
	if spec.GitHubOrg == "" && spec.GitHubRepo == "" {
		return nil
	}
	cond := fmt.Sprintf("assertion.repository_owner == '%s'", spec.GitHubOrg)
	if spec.GitHubRepo != "" {
		cond = fmt.Sprintf("assertion.repository == '%s'", spec.GitHubRepo)
	}
	prov, err := iam.NewWorkloadIdentityPoolProvider(ctx, name+"-github-oidc", &iam.WorkloadIdentityPoolProviderArgs{
		Project:                        spec.GCPProject,
		WorkloadIdentityPoolId:         pool.WorkloadIdentityPoolId,
		WorkloadIdentityPoolProviderId: pulumi.String("github-oidc"),
		DisplayName:                    pulumi.String("GitHub Actions OIDC"),
		AttributeCondition:             pulumi.String(cond),
		AttributeMapping: pulumi.StringMap{
			"google.subject":             pulumi.String("assertion.sub"),
			"attribute.repository":       pulumi.String("assertion.repository"),
			"attribute.repository_owner": pulumi.String("assertion.repository_owner"),
		},
		Oidc: &iam.WorkloadIdentityPoolProviderOidcArgs{
			IssuerUri: pulumi.String("https://token.actions.githubusercontent.com"),
		},
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.GitHubOIDCProvider = prov
	return nil
}

func createVercelProvider(
	ctx *pulumi.Context,
	name string,
	spec Shared,
	pool *iam.WorkloadIdentityPool,
	res *SharedResource,
) error {
	if spec.VercelTeamSlug == "" {
		res.VercelOIDCProviderName = pulumi.String("").ToStringOutput()
		return nil
	}
	issuer := "https://oidc.vercel.com/" + spec.VercelTeamSlug
	if spec.VercelIssuerMode == "global" {
		issuer = "https://oidc.vercel.com"
	}
	prov, err := iam.NewWorkloadIdentityPoolProvider(ctx, name+"-vercel-oidc", &iam.WorkloadIdentityPoolProviderArgs{
		Project:                        spec.GCPProject,
		WorkloadIdentityPoolId:         pool.WorkloadIdentityPoolId,
		WorkloadIdentityPoolProviderId: pulumi.String("vercel-oidc"),
		DisplayName:                    pulumi.String("Vercel OIDC"),
		AttributeCondition:             pulumi.String(fmt.Sprintf("assertion.owner == '%s'", spec.VercelTeamSlug)),
		AttributeMapping: pulumi.StringMap{
			"google.subject":        pulumi.String("assertion.sub"),
			"attribute.project_id":  pulumi.String("assertion.project_id"),
			"attribute.project":     pulumi.String("assertion.project"),
			"attribute.owner":       pulumi.String("assertion.owner"),
			"attribute.environment": pulumi.String("assertion.environment"),
		},
		Oidc: &iam.WorkloadIdentityPoolProviderOidcArgs{
			IssuerUri:        pulumi.String(issuer),
			AllowedAudiences: pulumi.ToStringArray([]string{"https://vercel.com/" + spec.VercelTeamSlug}),
		},
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.VercelOIDCProvider = prov
	res.VercelOIDCProviderName = prov.Name
	return nil
}

func createOptionalKeyRing(
	ctx *pulumi.Context,
	name, location string,
	meta core.ResourceMeta,
	spec Shared,
	res *SharedResource,
) error {
	if spec.SkipKeyRing {
		res.KeyRingID = pulumi.String("").ToStringOutput()
		return nil
	}
	ringName := core.BoundedName(meta.CanonicalName+"-kr", 63)
	kr, err := kms.NewKeyRing(ctx, name+"-kr", &kms.KeyRingArgs{
		Project:  spec.GCPProject,
		Location: pulumi.String(location),
		Name:     pulumi.String(ringName),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.KeyRing = kr
	res.KeyRingID = kr.ID().ToStringOutput()
	return nil
}

func buildSharedLink(meta core.ResourceMeta, res *SharedResource) core.LinkDescriptor {
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			"GCP_ARTIFACT_REGISTRY_URL":  res.RepositoryURL,
			"GCP_WORKLOAD_IDENTITY_POOL": res.WorkloadIdentityPoolName,
		},
	}
}

func registerSharedOutputs(ctx *pulumi.Context, res *SharedResource) error {
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

func defaultStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}
