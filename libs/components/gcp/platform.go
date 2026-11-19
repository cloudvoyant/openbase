// Package gcp provides OpenBase Pulumi components for Google Cloud Platform across Platform, Project, CloudLoadBalancer, CloudRunService, and Bucket tiers.
package gcp

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/iam"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/kms"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/projects"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.PlatformResource = (*PlatformResource)(nil)

// Platform declares organization-level GCP prerequisites (APIs, Workload Identity Federation, KMS, and wildcard Load Balancer) provisioned once per org.
type Platform struct {
	Name               string
	Org                string
	GCPProject         pulumi.StringPtrInput
	GCPProjectNumber   string
	Location           string
	PoolSuffix         string
	GitHubOrg          string
	VercelTeamSlug     string
	VercelIssuerMode   string
	BaseDomain         string
	EnableLoadBalancer bool
	Services           []string
	SkipKeyRing        bool
	ExtraLabels        map[string]string
}

// PlatformResource holds org-level enabled GCP APIs, Workload Identity Pool/Providers, KMS KeyRing, and optional wildcard LoadBalancer.
type PlatformResource struct {
	pulumi.ResourceState
	core.PlatformMeta

	EnabledServices          []*projects.Service
	WorkloadIdentityPool     *iam.WorkloadIdentityPool
	GitHubOIDCProvider       *iam.WorkloadIdentityPoolProvider
	VercelOIDCProvider       *iam.WorkloadIdentityPoolProvider
	KeyRing                  *kms.KeyRing
	LoadBalancer             *CloudLoadBalancerResource
	BaseDomain               pulumi.StringOutput
	Location                 pulumi.StringOutput
	WorkloadIdentityPoolName pulumi.StringOutput
	VercelOIDCProviderName   pulumi.StringOutput
	KeyRingID                pulumi.StringOutput
}

// Register provisions org-level GCP prerequisites for consumption by per-repo gcp.Project stacks.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	p.Name = defaultStr(p.Name, "gcp-platform")
	orgScope := core.OrgScope(p.Org, p.GitHubOrg, p.VercelTeamSlug, ctx.Organization())
	res := &PlatformResource{}
	info, err := core.InitPlatform(ctx, "openbase:gcp:Platform", "gcp-platform", res, &res.PlatformMeta, core.PlatformSpec{
		Name: p.Name,
		Org:  orgScope,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionGCPPlatform(ctx, p.Name, orgScope, info, p, res); err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: info.Name,
		Kind: info.Kind,
		Env: map[string]pulumi.StringOutput{
			"GCP_WORKLOAD_IDENTITY_POOL": res.WorkloadIdentityPoolName,
			"GCP_BASE_DOMAIN":            res.BaseDomain,
		},
	})
	return res, registerPlatformOutputs(ctx, p, res)
}

// Private helpers ------------------------------------------------------------

var defaultPlatformServices = []string{
	"run.googleapis.com",
	"artifactregistry.googleapis.com",
	"iam.googleapis.com",
	"iamcredentials.googleapis.com",
	"sts.googleapis.com",
	"storage.googleapis.com",
	"secretmanager.googleapis.com",
	"cloudkms.googleapis.com",
}

func provisionGCPPlatform(
	ctx *pulumi.Context,
	name, orgScope string,
	info core.PlatformInfo,
	spec Platform,
	res *PlatformResource,
) error {
	location := defaultStr(spec.Location, "us-central1")
	res.Location = pulumi.String(location).ToStringOutput()
	res.BaseDomain = pulumi.String(core.NormalizeDomain(spec.BaseDomain)).ToStringOutput()

	svcs, err := enableProjectServices(ctx, name, spec, res)
	if err != nil {
		return err
	}
	res.EnabledServices = svcs
	if err := createIdentityFederation(ctx, name, orgScope, spec, res); err != nil {
		return err
	}
	if err := createOptionalKeyRing(ctx, name, location, info, spec, res); err != nil {
		return err
	}
	return createOptionalPlatformLB(ctx, name, orgScope, location, spec, res)
}

func enableProjectServices(ctx *pulumi.Context, name string, spec Platform, parent pulumi.Resource) ([]*projects.Service, error) {
	list := spec.Services
	if len(list) == 0 {
		list = defaultPlatformServices
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

func createIdentityFederation(
	ctx *pulumi.Context,
	name, orgScope string,
	spec Platform,
	res *PlatformResource,
) error {
	rawPool := "ob-" + orgScope + "-pool"
	if spec.PoolSuffix != "" {
		rawPool = fmt.Sprintf("ob-%s-%s", orgScope, spec.PoolSuffix)
	}
	poolID := core.BoundedName(rawPool, 32)
	pool, err := iam.NewWorkloadIdentityPool(ctx, name+"-wif-pool", &iam.WorkloadIdentityPoolArgs{
		Project:                spec.GCPProject,
		WorkloadIdentityPoolId: pulumi.String(poolID),
		DisplayName:            pulumi.String(core.BoundedName(orgScope+" OIDC Pool", 32)),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.WorkloadIdentityPool = pool
	res.WorkloadIdentityPoolName = pool.Name
	if err := attachGitHubWIFProvider(ctx, name, spec, pool, res); err != nil {
		return err
	}
	return attachVercelWIFProvider(ctx, name, spec, pool, res)
}

func attachGitHubWIFProvider(
	ctx *pulumi.Context,
	name string,
	spec Platform,
	pool *iam.WorkloadIdentityPool,
	res *PlatformResource,
) error {
	if spec.GitHubOrg == "" {
		return nil
	}
	prov, err := iam.NewWorkloadIdentityPoolProvider(ctx, name+"-github-oidc", &iam.WorkloadIdentityPoolProviderArgs{
		Project:                        spec.GCPProject,
		WorkloadIdentityPoolId:         pool.WorkloadIdentityPoolId,
		WorkloadIdentityPoolProviderId: pulumi.String("github-oidc"),
		DisplayName:                    pulumi.String("GitHub Actions OIDC"),
		AttributeCondition:             pulumi.String(fmt.Sprintf("assertion.repository_owner == '%s'", spec.GitHubOrg)),
		AttributeMapping: pulumi.StringMap{
			"google.subject":       pulumi.String("assertion.sub"),
			"attribute.actor":      pulumi.String("assertion.actor"),
			"attribute.repository": pulumi.String("assertion.repository"),
			"attribute.ref":        pulumi.String("assertion.ref"),
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

func attachVercelWIFProvider(
	ctx *pulumi.Context,
	name string,
	spec Platform,
	pool *iam.WorkloadIdentityPool,
	res *PlatformResource,
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
	info core.PlatformInfo,
	spec Platform,
	res *PlatformResource,
) error {
	if spec.SkipKeyRing {
		res.KeyRingID = pulumi.String("").ToStringOutput()
		return nil
	}
	ringName := core.BoundedName(info.CanonicalName+"-kr", 63)
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

func createOptionalPlatformLB(
	ctx *pulumi.Context,
	name, orgScope, location string,
	spec Platform,
	res *PlatformResource,
) error {
	if !spec.EnableLoadBalancer || core.NormalizeDomain(spec.BaseDomain) == "" {
		return nil
	}
	lb, err := CloudLoadBalancer{
		Name:        name + "-lb",
		Project:     orgScope,
		Environment: string(core.EnvShared),
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

func registerPlatformOutputs(ctx *pulumi.Context, spec Platform, res *PlatformResource) error {
	outs := pulumi.Map{
		"gcpProjectNumber":         pulumi.String(spec.GCPProjectNumber),
		"location":                 res.Location,
		"baseDomain":               res.BaseDomain,
		"gcpBaseDomain":            res.BaseDomain,
		"workloadIdentityPoolName": res.WorkloadIdentityPoolName,
		"vercelOidcProviderName":   res.VercelOIDCProviderName,
		"keyRingId":                res.KeyRingID,
	}
	if spec.GCPProject != nil {
		outs["gcpProject"] = spec.GCPProject
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
