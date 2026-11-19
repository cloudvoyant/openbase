package gcp

import (
	"errors"
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/certificatemanager"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/compute"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/projects"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatelessResource                        = (*CloudLoadBalancerResource)(nil)
	_ core.ScopedSpecOf[*CloudLoadBalancerResource] = CloudLoadBalancer{}
)

// ErrBaseDomainRequired reports a missing BaseDomain on CloudLoadBalancer.
var ErrBaseDomainRequired = errors.New("gcp: baseDomain is required for CloudLoadBalancer")

// CloudLoadBalancer declares a GCP Global External Application Load Balancer routing *.{BaseDomain} to Cloud Run via Serverless NEG URL mask (org, shared, or environment scoped).
type CloudLoadBalancer struct {
	Name        string
	Project     string
	Repo        string
	Environment string
	GCPProject  pulumi.StringPtrInput
	Location    string
	BaseDomain  string
	ExtraLabels map[string]string
}

// CloudLoadBalancerResource holds the Global IPv4, wildcard Certificate Manager TLS resources, and Serverless NEG URL-mask routing resources.
type CloudLoadBalancerResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	Address           *compute.GlobalAddress
	DnsAuthorization  *certificatemanager.DnsAuthorization
	Certificate       *certificatemanager.Certificate
	CertificateMap    *certificatemanager.CertificateMapResource
	WildcardMapEntry  *certificatemanager.CertificateMapEntry
	ServerlessNEG     *compute.RegionNetworkEndpointGroup
	BackendService    *compute.BackendService
	URLMap            *compute.URLMap
	TargetHTTPSProxy  *compute.TargetHttpsProxy
	ForwardingRule    *compute.GlobalForwardingRule
	BaseDomain        string
	IPAddress         pulumi.StringOutput
	DnsAuthRecordName pulumi.StringOutput
	DnsAuthRecordData pulumi.StringOutput
	URLMask           string
}

// Register provisions a shared GCP Global HTTPS Load Balancer for *.{BaseDomain}.
func (lb CloudLoadBalancer) Register(
	ctx *pulumi.Context,
	opts ...pulumi.ResourceOption,
) (*CloudLoadBalancerResource, error) {
	base := core.NormalizeDomain(lb.BaseDomain)
	if base == "" {
		return nil, ErrBaseDomainRequired
	}
	lb.Name = defaultStr(lb.Name, "lb")
	lb.Location = defaultStr(lb.Location, "us-central1")
	res := &CloudLoadBalancerResource{BaseDomain: base, URLMask: "<service>." + base}
	meta, err := core.InitStateless(ctx, "openbase:gcp:CloudLoadBalancer", "gcp-cloud-load-balancer", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        lb.Name,
		Project:     lb.Project,
		Repo:        lb.Repo,
		Environment: lb.Environment,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionWildcardLoadBalancer(ctx, lb.Name, base, meta, lb, res); err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"ipAddress":         res.IPAddress,
			"baseDomain":        pulumi.String(base).ToStringOutput(),
			"dnsAuthRecordName": res.DnsAuthRecordName,
			"dnsAuthRecordData": res.DnsAuthRecordData,
		},
		StackExports: map[string]pulumi.Input{
			"loadBalancerIp":    res.IPAddress,
			"baseDomain":        pulumi.String(base),
			"dnsAuthRecordName": res.DnsAuthRecordName,
			"dnsAuthRecordData": res.DnsAuthRecordData,
		},
	})
	return res, registerLoadBalancerOutputs(ctx, base, res)
}

// RegisterInScope adapts gcp.CloudLoadBalancer to openbase.Project and openbase.Environment.
func (lb CloudLoadBalancer) RegisterInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.Resource, error) {
	return lb.RegisterTypedInScope(ctx, scope, opts...)
}

// RegisterTypedInScope registers gcp.CloudLoadBalancer using inherited Scope metadata.
func (lb CloudLoadBalancer) RegisterTypedInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (*CloudLoadBalancerResource, error) {
	if lb.Project == "" {
		lb.Project = scope.Project
	}
	if lb.Repo == "" {
		lb.Repo = scope.Repo
	}
	if lb.Environment == "" && scope.Env != "" {
		lb.Environment = scope.Env.String()
	}
	return lb.Register(ctx, opts...)
}

// Private helpers ------------------------------------------------------------

func provisionWildcardLoadBalancer(
	ctx *pulumi.Context,
	name, base string,
	meta core.ResourceMeta,
	spec CloudLoadBalancer,
	res *CloudLoadBalancerResource,
) error {
	deps, err := enableLoadBalancerAPIs(ctx, name, spec.GCPProject, res)
	if err != nil {
		return err
	}
	addr, err := compute.NewGlobalAddress(ctx, name+"-ip", &compute.GlobalAddressArgs{
		Project:     spec.GCPProject,
		Name:        pulumi.String(core.BoundedName(meta.CanonicalName+"-ip", 63)),
		IpVersion:   pulumi.String("IPV4"),
		AddressType: pulumi.String("EXTERNAL"),
		Labels:      core.GCPLabels(meta, spec.ExtraLabels),
	}, pulumi.Parent(res), pulumi.DependsOn(deps))
	if err != nil {
		return err
	}
	res.Address = addr
	res.IPAddress = addr.Address

	certMapURI, err := createWildcardCertificateMap(ctx, name, base, meta, spec, deps, res)
	if err != nil {
		return err
	}
	return createServerlessMaskFrontend(ctx, name, meta, spec, addr, certMapURI, deps, res)
}

func enableLoadBalancerAPIs(
	ctx *pulumi.Context,
	name string,
	gcpProject pulumi.StringPtrInput,
	parent pulumi.Resource,
) ([]pulumi.Resource, error) {
	apis := []string{"compute.googleapis.com", "certificatemanager.googleapis.com"}
	deps := make([]pulumi.Resource, 0, len(apis))
	for _, api := range apis {
		svc, err := projects.NewService(ctx, fmt.Sprintf("%s-api-%s", name, api), &projects.ServiceArgs{
			Project:                  gcpProject,
			Service:                  pulumi.String(api),
			DisableOnDestroy:         pulumi.Bool(false),
			DisableDependentServices: pulumi.Bool(false),
		}, pulumi.Parent(parent))
		if err != nil {
			return nil, err
		}
		deps = append(deps, svc)
	}
	return deps, nil
}

func createWildcardCertificateMap(
	ctx *pulumi.Context,
	name, base string,
	meta core.ResourceMeta,
	spec CloudLoadBalancer,
	deps []pulumi.Resource,
	res *CloudLoadBalancerResource,
) (pulumi.StringOutput, error) {
	labels := core.GCPLabels(meta, spec.ExtraLabels)
	dnsAuth, err := certificatemanager.NewDnsAuthorization(ctx, name+"-dns-auth", &certificatemanager.DnsAuthorizationArgs{
		Project: spec.GCPProject,
		Name:    pulumi.String(core.BoundedName(meta.CanonicalName+"-dns", 63)),
		Domain:  pulumi.String(base),
		Labels:  labels,
	}, pulumi.Parent(res), pulumi.DependsOn(deps))
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	cert, err := certificatemanager.NewCertificate(ctx, name+"-wildcard-cert", &certificatemanager.CertificateArgs{
		Project: spec.GCPProject,
		Name:    pulumi.String(core.BoundedName(meta.CanonicalName+"-cert", 63)),
		Labels:  labels,
		Managed: &certificatemanager.CertificateManagedArgs{
			Domains:           pulumi.ToStringArray([]string{base, "*." + base}),
			DnsAuthorizations: pulumi.StringArray{dnsAuth.ID()},
		},
	}, pulumi.Parent(res))
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	res.DnsAuthorization = dnsAuth
	res.Certificate = cert
	res.DnsAuthRecordName, res.DnsAuthRecordData = extractDnsAuthRecordOutputs(dnsAuth)
	return attachCertificateToMap(ctx, name, base, meta, spec, cert, labels, res)
}

func extractDnsAuthRecordOutputs(dnsAuth *certificatemanager.DnsAuthorization) (pulumi.StringOutput, pulumi.StringOutput) {
	recName := dnsAuth.DnsResourceRecords.ApplyT(func(recs []certificatemanager.DnsAuthorizationDnsResourceRecord) string {
		if len(recs) > 0 && recs[0].Name != nil {
			return *recs[0].Name
		}
		return ""
	}).(pulumi.StringOutput)
	recData := dnsAuth.DnsResourceRecords.ApplyT(func(recs []certificatemanager.DnsAuthorizationDnsResourceRecord) string {
		if len(recs) > 0 && recs[0].Data != nil {
			return *recs[0].Data
		}
		return ""
	}).(pulumi.StringOutput)
	return recName, recData
}

func attachCertificateToMap(
	ctx *pulumi.Context,
	name, base string,
	meta core.ResourceMeta,
	spec CloudLoadBalancer,
	cert *certificatemanager.Certificate,
	labels pulumi.StringMap,
	res *CloudLoadBalancerResource,
) (pulumi.StringOutput, error) {
	cmap, err := certificatemanager.NewCertificateMapResource(ctx, name+"-cert-map", &certificatemanager.CertificateMapResourceArgs{
		Project: spec.GCPProject,
		Name:    pulumi.String(core.BoundedName(meta.CanonicalName+"-cmap", 63)),
		Labels:  labels,
	}, pulumi.Parent(res))
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	entry, err := certificatemanager.NewCertificateMapEntry(ctx, name+"-wildcard-entry", &certificatemanager.CertificateMapEntryArgs{
		Project:      spec.GCPProject,
		Name:         pulumi.String(core.BoundedName(meta.CanonicalName+"-wild", 63)),
		Map:          cmap.Name,
		Hostname:     pulumi.String("*." + base),
		Certificates: pulumi.StringArray{cert.ID()},
		Labels:       labels,
	}, pulumi.Parent(res))
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	res.CertificateMap = cmap
	res.WildcardMapEntry = entry
	return pulumi.Sprintf("//certificatemanager.googleapis.com/%s", cmap.ID()), nil
}

func createServerlessMaskFrontend(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec CloudLoadBalancer,
	addr *compute.GlobalAddress,
	certMapURI pulumi.StringOutput,
	deps []pulumi.Resource,
	res *CloudLoadBalancerResource,
) error {
	neg, err := compute.NewRegionNetworkEndpointGroup(ctx, name+"-neg", &compute.RegionNetworkEndpointGroupArgs{
		Project:             spec.GCPProject,
		Name:                pulumi.String(core.BoundedName(meta.CanonicalName+"-neg", 63)),
		Region:              pulumi.String(spec.Location),
		NetworkEndpointType: pulumi.String("SERVERLESS"),
		CloudRun: &compute.RegionNetworkEndpointGroupCloudRunArgs{
			UrlMask: pulumi.String(res.URLMask),
		},
	}, pulumi.Parent(res), pulumi.DependsOn(deps))
	if err != nil {
		return err
	}
	backend, err := compute.NewBackendService(ctx, name+"-backend", &compute.BackendServiceArgs{
		Project:             spec.GCPProject,
		Name:                pulumi.String(core.BoundedName(meta.CanonicalName+"-bs", 63)),
		LoadBalancingScheme: pulumi.String("EXTERNAL_MANAGED"),
		Protocol:            pulumi.String("HTTP"),
		Backends: compute.BackendServiceBackendArray{
			&compute.BackendServiceBackendArgs{Group: neg.ID()},
		},
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.ServerlessNEG = neg
	res.BackendService = backend
	return wireHTTPSForwardingRule(ctx, name, meta, spec, addr, backend, certMapURI, res)
}

func wireHTTPSForwardingRule(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec CloudLoadBalancer,
	addr *compute.GlobalAddress,
	backend *compute.BackendService,
	certMapURI pulumi.StringOutput,
	res *CloudLoadBalancerResource,
) error {
	umap, err := compute.NewURLMap(ctx, name+"-urlmap", &compute.URLMapArgs{
		Project:        spec.GCPProject,
		Name:           pulumi.String(core.BoundedName(meta.CanonicalName+"-umap", 63)),
		DefaultService: backend.ID(),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	proxy, err := compute.NewTargetHttpsProxy(ctx, name+"-https-proxy", &compute.TargetHttpsProxyArgs{
		Project:        spec.GCPProject,
		Name:           pulumi.String(core.BoundedName(meta.CanonicalName+"-proxy", 63)),
		UrlMap:         umap.ID(),
		CertificateMap: certMapURI,
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	fwd, err := compute.NewGlobalForwardingRule(ctx, name+"-https-fwd", &compute.GlobalForwardingRuleArgs{
		Project:             spec.GCPProject,
		Name:                pulumi.String(core.BoundedName(meta.CanonicalName+"-fwd", 63)),
		IpAddress:           addr.Address,
		IpProtocol:          pulumi.String("TCP"),
		PortRange:           pulumi.String("443"),
		LoadBalancingScheme: pulumi.String("EXTERNAL_MANAGED"),
		Target:              proxy.ID(),
		Labels:              core.GCPLabels(meta, spec.ExtraLabels),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.URLMap = umap
	res.TargetHTTPSProxy = proxy
	res.ForwardingRule = fwd
	return nil
}

func registerLoadBalancerOutputs(ctx *pulumi.Context, base string, res *CloudLoadBalancerResource) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"baseDomain":        pulumi.String(base),
		"urlMask":           pulumi.String(res.URLMask),
		"ipAddress":         res.IPAddress,
		"dnsAuthRecordName": res.DnsAuthRecordName,
		"dnsAuthRecordData": res.DnsAuthRecordData,
	})
}
