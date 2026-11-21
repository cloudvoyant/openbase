// Package gcp provides OpenBase Pulumi components for Google Cloud Platform across Account, Project, CloudLoadBalancer, CloudRunService, and Bucket tiers.
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
	_ core.AccountResource = (*Account)(nil)
	_ AccountInput         = (*Account)(nil)
	_ AccountInput         = (*AccountRef)(nil)
)

// AccountInput is satisfied by in-memory *Account handles and cross-stack *AccountRef lookups.
type AccountInput interface {
	gcpAccountValues() gcpAccountData
}

// AccountArgs configures account-level GCP prerequisites (APIs, Workload Identity Pool, linked OIDC issuers, KMS, and wildcard Load Balancer).
type AccountArgs struct {
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

// Account holds account-level enabled GCP APIs, Workload Identity Pool, linked OIDC providers, KMS KeyRing, and optional LoadBalancer.
type Account struct {
	pulumi.ResourceState
	core.AccountMeta

	EnabledServices          []*projects.Service
	WorkloadIdentityPool     *iam.WorkloadIdentityPool
	OIDCProviders            map[string]*iam.WorkloadIdentityPoolProvider
	OIDCProviderNames        map[string]pulumi.StringOutput
	KeyRing                  *kms.KeyRing
	LoadBalancer             *CloudLoadBalancer
	GCPProject               pulumi.StringOutput
	GCPProjectNumber         pulumi.StringOutput
	BaseDomain               pulumi.StringOutput
	Location                 pulumi.StringOutput
	WorkloadIdentityPoolName pulumi.StringOutput
	KeyRingID                pulumi.StringOutput
}

// AccountRef provides typed cross-stack access to a gcp.Account exported in infra/platform.
type AccountRef struct {
	GCPProject               pulumi.StringOutput
	GCPProjectNumber         pulumi.StringOutput
	Location                 pulumi.StringOutput
	BaseDomain               pulumi.StringOutput
	WorkloadIdentityPoolName pulumi.StringOutput
	KeyRingID                pulumi.StringOutput

	plat *core.ScopeRef
	name []string
}

// NewAccount provisions account-level GCP prerequisites and attaches any linked OIDC issuers.
func NewAccount(ctx *pulumi.Context, name string, args *AccountArgs, opts ...pulumi.ResourceOption) (*Account, error) {
	if args == nil {
		args = &AccountArgs{}
	}
	name = defaultStr(name, "gcp")
	orgScope := core.OrgScope(args.Org, ctx.Organization())
	res := &Account{
		OIDCProviders:     map[string]*iam.WorkloadIdentityPoolProvider{},
		OIDCProviderNames: map[string]pulumi.StringOutput{},
		GCPProject:        ptrInputToStringOutput(args.GCPProject),
		GCPProjectNumber:  pulumi.String(args.GCPProjectNumber).ToStringOutput(),
	}
	info, err := core.InitAccount(ctx, "openbase:gcp:Account", "gcp-account", res, &res.AccountMeta, core.AccountSpec{
		Name: name,
		Org:  orgScope,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionGCPAccount(ctx, name, orgScope, info, *args, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, buildGCPAccountLink(info, *args, res))
	return res, registerAccountOutputs(ctx, *args, res)
}

// LookupAccount resolves a gcp.Account from an infra/platform ScopeRef (by unique kind or explicit name).
func LookupAccount(plat *core.ScopeRef, name ...string) *AccountRef {
	if plat == nil {
		return nil
	}
	return &AccountRef{
		GCPProject:               plat.ManifestAttr("gcp-account", name, "gcpProject"),
		GCPProjectNumber:         plat.ManifestAttr("gcp-account", name, "gcpProjectNumber"),
		Location:                 plat.ManifestAttr("gcp-account", name, "location"),
		BaseDomain:               plat.ManifestAttr("gcp-account", name, "baseDomain", "gcpBaseDomain"),
		WorkloadIdentityPoolName: plat.ManifestAttr("gcp-account", name, "workloadIdentityPoolName"),
		KeyRingID:                plat.ManifestAttr("gcp-account", name, "keyRingId"),
		plat:                     plat,
		name:                     name,
	}
}

// OIDCProviderName returns the full GCP resource name of a linked OIDC provider by issuer ID (or "" when absent).
func (r *Account) OIDCProviderName(id string) pulumi.StringOutput {
	if r == nil {
		return pulumi.String("").ToStringOutput()
	}
	if out, ok := r.OIDCProviderNames[id]; ok {
		return out
	}
	return pulumi.String("").ToStringOutput()
}

// OIDCProviderName returns the full GCP resource name of a linked OIDC provider by issuer ID from the platform manifest.
func (r *AccountRef) OIDCProviderName(id string) pulumi.StringOutput {
	if r == nil || r.plat == nil || id == "" {
		return pulumi.String("").ToStringOutput()
	}
	key := id + "OidcProviderName"
	return r.plat.ManifestAttr("gcp-account", r.name, key, key)
}

// WIFBroker returns a core.IdentityBroker backed by this GCP Account's Workload Identity Pool and linked OIDC providers.
func (r *Account) WIFBroker(gcpProject ...pulumi.StringPtrInput) WIFBroker {
	providers := map[string]pulumi.StringInput{}
	for id, out := range r.OIDCProviderNames {
		providers[id] = out
	}
	proj := nonEmptyStrPtrOutput(r.GCPProject)
	if len(gcpProject) > 0 && gcpProject[0] != nil {
		proj = gcpProject[0].ToStringPtrOutput()
	}
	return WIFBroker{
		GCPProject: proj,
		PoolName:   r.WorkloadIdentityPoolName,
		Providers:  providers,
	}
}

// WIFBroker returns a core.IdentityBroker backed by this looked-up GCP AccountRef.
func (r *AccountRef) WIFBroker(sourceProviders ...string) WIFBroker {
	providers := map[string]pulumi.StringInput{}
	for _, id := range sourceProviders {
		providers[id] = r.OIDCProviderName(id)
	}
	return WIFBroker{
		Account:   r,
		Providers: providers,
	}
}

// Private helpers ------------------------------------------------------------

type gcpAccountData struct {
	local        *Account
	gcpProject   pulumi.StringOutput
	baseDomain   pulumi.StringOutput
	poolName     pulumi.StringOutput
	oidcProvider func(id string) pulumi.StringOutput
}

func (a *Account) gcpAccountValues() gcpAccountData {
	if a == nil {
		return gcpAccountData{}
	}
	return gcpAccountData{
		local:        a,
		gcpProject:   a.GCPProject,
		baseDomain:   a.BaseDomain,
		poolName:     a.WorkloadIdentityPoolName,
		oidcProvider: a.OIDCProviderName,
	}
}

func (r *AccountRef) gcpAccountValues() gcpAccountData {
	if r == nil {
		return gcpAccountData{}
	}
	return gcpAccountData{
		gcpProject:   r.GCPProject,
		baseDomain:   r.BaseDomain,
		poolName:     r.WorkloadIdentityPoolName,
		oidcProvider: r.OIDCProviderName,
	}
}

var defaultAccountServices = []string{
	"run.googleapis.com",
	"artifactregistry.googleapis.com",
	"iam.googleapis.com",
	"iamcredentials.googleapis.com",
	"sts.googleapis.com",
	"storage.googleapis.com",
	"secretmanager.googleapis.com",
	"cloudkms.googleapis.com",
}

func ptrInputToStringOutput(in pulumi.StringPtrInput) pulumi.StringOutput {
	if in == nil {
		return pulumi.String("").ToStringOutput()
	}
	return in.ToStringPtrOutput().ApplyT(func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}).(pulumi.StringOutput)
}

func provisionGCPAccount(
	ctx *pulumi.Context,
	name, orgScope string,
	info core.AccountInfo,
	spec AccountArgs,
	res *Account,
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
	return createOptionalAccountLB(ctx, name, orgScope, location, spec, res)
}

func enableProjectServices(ctx *pulumi.Context, name string, spec AccountArgs, parent pulumi.Resource) ([]*projects.Service, error) {
	list := spec.Services
	if len(list) == 0 {
		list = defaultAccountServices
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
	spec AccountArgs,
	res *Account,
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
	res *Account,
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
	info core.AccountInfo,
	spec AccountArgs,
	res *Account,
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

func createOptionalAccountLB(
	ctx *pulumi.Context,
	name, orgScope, location string,
	spec AccountArgs,
	res *Account,
) error {
	if !spec.EnableLoadBalancer || core.NormalizeDomain(spec.BaseDomain) == "" {
		return nil
	}
	lb, err := NewCloudLoadBalancer(ctx, name+"-lb", &CloudLoadBalancerArgs{
		ProjectName: orgScope,
		Environment: string(core.EnvShared),
		GCPProject:  spec.GCPProject,
		Location:    location,
		BaseDomain:  spec.BaseDomain,
		ExtraLabels: spec.ExtraLabels,
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.LoadBalancer = lb
	return nil
}

func buildGCPAccountLink(info core.AccountInfo, spec AccountArgs, res *Account) core.LinkDescriptor {
	attrs := map[string]pulumi.StringOutput{
		"gcpProject":               res.GCPProject,
		"gcpProjectNumber":         res.GCPProjectNumber,
		"location":                 res.Location,
		"baseDomain":               res.BaseDomain,
		"workloadIdentityPoolName": res.WorkloadIdentityPoolName,
		"keyRingId":                res.KeyRingID,
	}
	exports := collectGCPAccountExports(spec, res)
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

func collectGCPAccountExports(spec AccountArgs, res *Account) map[string]pulumi.Input {
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

func registerAccountOutputs(ctx *pulumi.Context, spec AccountArgs, res *Account) error {
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
