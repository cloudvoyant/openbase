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

var (
	_ core.PlatformResource                      = (*PlatformResource)(nil)
	_ core.PlatformSpecItemOf[*PlatformResource] = Platform{}
)

// Platform declares account-level GCP prerequisites (APIs, Workload Identity Pool, linked OIDC issuers, KMS, and wildcard Load Balancer).
type Platform struct {
	Name               string
	Org                string
	GCPProject         pulumi.StringPtrInput
	GCPProjectNumber   string
	Location           string
	PoolSuffix         string
	Links              []core.OIDCProviderSource
	BaseDomain         string
	EnableLoadBalancer bool
	Services           []string
	SkipKeyRing        bool
	ExtraLabels        map[string]string
}

// PlatformResource holds account-level enabled GCP APIs, Workload Identity Pool, linked OIDC providers, KMS KeyRing, and optional LoadBalancer.
type PlatformResource struct {
	pulumi.ResourceState
	core.PlatformMeta

	EnabledServices          []*projects.Service
	WorkloadIdentityPool     *iam.WorkloadIdentityPool
	OIDCProviders            map[string]*iam.WorkloadIdentityPoolProvider
	OIDCProviderNames        map[string]pulumi.StringOutput
	KeyRing                  *kms.KeyRing
	LoadBalancer             *CloudLoadBalancerResource
	BaseDomain               pulumi.StringOutput
	Location                 pulumi.StringOutput
	WorkloadIdentityPoolName pulumi.StringOutput
	KeyRingID                pulumi.StringOutput
}

// Register provisions account-level GCP prerequisites and attaches any linked OIDC issuers.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	p.Name = defaultStr(p.Name, "gcp-platform")
	orgScope := core.OrgScope(p.Org, ctx.Organization())
	res := &PlatformResource{
		OIDCProviders:     map[string]*iam.WorkloadIdentityPoolProvider{},
		OIDCProviderNames: map[string]pulumi.StringOutput{},
	}
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
	res.SetLink(buildGCPPlatformLink(info, p, res))
	return res, registerPlatformOutputs(ctx, p, res)
}

// RegisterPlatformInScope adapts gcp.Platform to openbase.Platform.
func (p Platform) RegisterPlatformInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.PlatformResource, error) {
	return p.RegisterTypedPlatformInScope(ctx, scope, opts...)
}

// RegisterTypedPlatformInScope registers gcp.Platform with inherited Org defaults and explicit ResourceOptions.
func (p Platform) RegisterTypedPlatformInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	if p.Org == "" {
		p.Org = scope.Org
	}
	return p.Register(ctx, opts...)
}

// OIDCProviderName returns the full GCP resource name of a linked OIDC provider by issuer ID (or "" when absent).
func (r *PlatformResource) OIDCProviderName(id string) pulumi.StringOutput {
	if r == nil {
		return pulumi.String("").ToStringOutput()
	}
	if out, ok := r.OIDCProviderNames[id]; ok {
		return out
	}
	return pulumi.String("").ToStringOutput()
}

// WIFBroker returns a core.IdentityBroker backed by this GCP Platform's Workload Identity Pool and linked OIDC providers.
func (r *PlatformResource) WIFBroker(gcpProject pulumi.StringPtrInput) WIFBroker {
	providers := map[string]pulumi.StringInput{}
	for id, out := range r.OIDCProviderNames {
		providers[id] = out
	}
	return WIFBroker{
		GCPProject: gcpProject,
		PoolName:   r.WorkloadIdentityPoolName,
		Providers:  providers,
	}
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
	for _, src := range spec.Links {
		if err := attachLinkedOIDCProvider(ctx, name, spec.GCPProject, pool, src, res); err != nil {
			return err
		}
	}
	return nil
}

func attachLinkedOIDCProvider(
	ctx *pulumi.Context,
	name string,
	gcpProject pulumi.StringPtrInput,
	pool *iam.WorkloadIdentityPool,
	src core.OIDCProviderSource,
	res *PlatformResource,
) error {
	if src == nil {
		return nil
	}
	issuer, ok := src.OIDCIssuerSpec()
	if !ok {
		return nil
	}
	provID := issuer.ID
	if !strings.HasSuffix(provID, "-oidc") {
		provID += "-oidc"
	}
	prov, err := iam.NewWorkloadIdentityPoolProvider(ctx, name+"-"+provID, &iam.WorkloadIdentityPoolProviderArgs{
		Project:                        gcpProject,
		WorkloadIdentityPoolId:         pool.WorkloadIdentityPoolId,
		WorkloadIdentityPoolProviderId: pulumi.String(core.BoundedName(provID, 32)),
		DisplayName:                    pulumi.String(core.BoundedName(defaultStr(issuer.DisplayName, issuer.ID), 32)),
		AttributeCondition:             optionalCondition(issuer.AttributeCondition),
		AttributeMapping:               buildGCPAttributeMapping(issuer),
		Oidc: &iam.WorkloadIdentityPoolProviderOidcArgs{
			IssuerUri:        pulumi.String(issuer.IssuerURI),
			AllowedAudiences: toStringArrayOrNil(issuer.AllowedAudiences),
		},
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.OIDCProviders[issuer.ID] = prov
	res.OIDCProviderNames[issuer.ID] = prov.Name
	return nil
}

func buildGCPAttributeMapping(issuer core.OIDCIssuer) pulumi.StringMap {
	mapping := pulumi.StringMap{
		"google.subject": pulumi.String(defaultStr(issuer.SubjectExpr, "assertion.sub")),
	}
	for claim, expr := range issuer.Claims {
		mapping["attribute."+claim] = pulumi.String(expr)
	}
	return mapping
}

func optionalCondition(cond string) pulumi.StringPtrInput {
	if strings.TrimSpace(cond) == "" {
		return nil
	}
	return pulumi.String(cond)
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

func buildGCPPlatformLink(info core.PlatformInfo, spec Platform, res *PlatformResource) core.LinkDescriptor {
	attrs := map[string]pulumi.StringOutput{
		"gcpProjectNumber":         pulumi.String(spec.GCPProjectNumber).ToStringOutput(),
		"location":                 res.Location,
		"baseDomain":               res.BaseDomain,
		"workloadIdentityPoolName": res.WorkloadIdentityPoolName,
		"keyRingId":                res.KeyRingID,
	}
	if spec.GCPProject != nil {
		attrs["gcpProject"] = spec.GCPProject.ToStringPtrOutput().ApplyT(func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		}).(pulumi.StringOutput)
	}
	exports := collectGCPPlatformExports(spec, res)
	for id, provName := range res.OIDCProviderNames {
		key := id + "OidcProviderName"
		attrs[key] = provName
		exports[key] = provName
	}
	return core.LinkDescriptor{
		Name:         info.Name,
		Kind:         info.Kind,
		Attributes:   attrs,
		StackExports: exports,
	}
}

func collectGCPPlatformExports(spec Platform, res *PlatformResource) map[string]pulumi.Input {
	exports := map[string]pulumi.Input{
		"location":                 res.Location,
		"gcpBaseDomain":            res.BaseDomain,
		"workloadIdentityPoolName": res.WorkloadIdentityPoolName,
		"keyRingId":                res.KeyRingID,
	}
	if spec.GCPProject != nil {
		exports["gcpProject"] = spec.GCPProject
	}
	if spec.GCPProjectNumber != "" {
		exports["gcpProjectNumber"] = pulumi.String(spec.GCPProjectNumber)
	}
	if res.LoadBalancer != nil {
		exports["loadBalancerIp"] = res.LoadBalancer.IPAddress
		exports["dnsAuthRecordName"] = res.LoadBalancer.DnsAuthRecordName
		exports["dnsAuthRecordData"] = res.LoadBalancer.DnsAuthRecordData
	}
	return exports
}

func registerPlatformOutputs(ctx *pulumi.Context, spec Platform, res *PlatformResource) error {
	outs := pulumi.Map{
		"gcpProjectNumber":         pulumi.String(spec.GCPProjectNumber),
		"location":                 res.Location,
		"baseDomain":               res.BaseDomain,
		"gcpBaseDomain":            res.BaseDomain,
		"workloadIdentityPoolName": res.WorkloadIdentityPoolName,
		"keyRingId":                res.KeyRingID,
	}
	for id, provName := range res.OIDCProviderNames {
		outs[id+"OidcProviderName"] = provName
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
