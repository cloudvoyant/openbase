package gcp

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/serviceaccount"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.IdentityBroker    = WIFBroker{}
	_ core.IdentityBroker    = (*FederatedIdentity)(nil)
	_ core.StatelessResource = (*FederatedIdentity)(nil)
)

// FederatedIdentityArgs configures an explicit federated GCP Service Account bound to an external OIDC workload claim.
type FederatedIdentityArgs struct {
	ProjectName    string
	Repo           string
	Environment    string
	Account        AccountInput
	GCPProject     pulumi.StringPtrInput
	PoolName       pulumi.StringInput
	OIDCProvider   pulumi.StringInput
	SourceProvider string
	ClaimKey       string
	ClaimValue     pulumi.StringInput
}

// FederatedIdentity holds the provisioned federated GCP Service Account and WIF audience outputs.
type FederatedIdentity struct {
	pulumi.ResourceState
	core.StatelessMeta

	ServiceAccount      *serviceaccount.Account
	ServiceAccountEmail pulumi.StringOutput
	Member              pulumi.StringOutput
	ProviderAudience    pulumi.StringOutput
}

// NewFederatedIdentity provisions a federated GCP Service Account and workloadIdentityUser binding.
func NewFederatedIdentity(ctx *pulumi.Context, name string, args *FederatedIdentityArgs, opts ...pulumi.ResourceOption) (*FederatedIdentity, error) {
	if args == nil {
		args = &FederatedIdentityArgs{}
	}
	name = defaultStr(name, "wif")
	res := &FederatedIdentity{}
	meta, err := core.InitStateless(ctx, "openbase:gcp:FederatedIdentity", "gcp-federated-identity", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        name,
		Project:     args.ProjectName,
		Repo:        args.Repo,
		Environment: args.Environment,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionFederatedIdentity(ctx, meta, *args, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"serviceAccountEmail": res.ServiceAccountEmail,
			"member":              res.Member,
			"providerAudience":    res.ProviderAudience,
		},
	})
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"serviceAccountEmail": res.ServiceAccountEmail,
		"member":              res.Member,
		"providerAudience":    res.ProviderAudience,
	})
}

// BrokerProvider identifies FederatedIdentity as resolving GCP IAM principals.
func (*FederatedIdentity) BrokerProvider() string { return "gcp" }

// ResolvePrincipal returns the pre-created federated GCP Service Account member for Link access grants.
func (r *FederatedIdentity) ResolvePrincipal(_ *pulumi.Context, _ core.ResourceMeta, _ core.Subject) (core.PrincipalGrant, error) {
	if r == nil {
		return core.PrincipalGrant{Provider: "gcp"}, nil
	}
	return core.PrincipalGrant{
		Provider: "gcp",
		Member:   r.Member,
	}, nil
}

// WIFBroker translates external workload OIDC Subjects into federated GCP Service Accounts.
type WIFBroker struct {
	Account        AccountInput
	GCPProject     pulumi.StringPtrInput
	PoolName       pulumi.StringInput
	OIDCProvider   pulumi.StringInput
	Providers      map[string]pulumi.StringInput
	PrincipalClaim string
}

// BrokerProvider identifies this broker as resolving GCP IAM principals.
func (WIFBroker) BrokerProvider() string { return "gcp" }

// ResolvePrincipal mints a federated GCP Service Account and workloadIdentityUser binding for external OIDC callers.
func (b WIFBroker) ResolvePrincipal(ctx *pulumi.Context, consumer core.ResourceMeta, sub core.Subject) (core.PrincipalGrant, error) {
	applyAccountToWIFBroker(&b)
	if sub.Provider == "" || sub.Provider == "gcp" || b.GCPProject == nil || b.PoolName == nil {
		return core.PrincipalGrant{Provider: "gcp"}, nil
	}
	poolOut := b.PoolName.ToStringOutput()
	claimKey, claimOut, ok := selectPrincipalClaim(b.PrincipalClaim, sub.Claims)
	if !ok || !isOutputSet(poolOut) {
		return core.PrincipalGrant{Provider: "gcp"}, nil
	}
	sa, err := createFederatedSA(ctx, b.GCPProject, consumer, sub.Provider, poolOut, claimKey, claimOut, nil)
	if err != nil {
		return core.PrincipalGrant{}, err
	}
	return core.PrincipalGrant{
		Provider: "gcp",
		Member:   pulumi.Sprintf("serviceAccount:%s", sa.Email),
	}, nil
}

// CloudRunCallbackURLSet computes deterministic non-prod wildcard and stage/prod OAuth URLs for a Cloud Run service.
type CloudRunCallbackURLSet struct {
	StageHost             string
	ProdHost              string
	ExtraNonProdRedirects []string
	ExtraNonProdLogouts   []string
	ExtraProdRedirect     []string
	ExtraProdLogout       []string
}

// BuildCloudRunCallbackURLs computes Kinde-compatible dev/preview wildcard and stage/prod callback URLs for Cloud Run.
func BuildCloudRunCallbackURLs(project, serviceName, projectNumber, location, baseDomain, callbackPath string) (CloudRunCallbackURLSet, error) {
	loc := defaultStr(location, "us-central1")
	svc := defaultStr(serviceName, "web")
	cb := defaultStr(callbackPath, "/api/auth/kinde_callback")
	if !strings.HasPrefix(cb, "/") {
		cb = "/" + cb
	}
	stageURL, err := CloudRunRegionalURL(project, core.EnvStage, svc, projectNumber, loc, baseDomain)
	if err != nil {
		return CloudRunCallbackURLSet{}, err
	}
	prodURL, err := CloudRunRegionalURL(project, core.EnvProd, svc, projectNumber, loc, baseDomain)
	if err != nil {
		return CloudRunCallbackURLSet{}, err
	}
	return assembleCloudRunCallbackSet(project, projectNumber, loc, baseDomain, cb, stageURL, prodURL), nil
}

// Private helpers ------------------------------------------------------------

func selectPrincipalClaim(explicit string, claims map[string]pulumi.StringOutput) (string, pulumi.StringOutput, bool) {
	if explicit != "" {
		out, ok := claims[explicit]
		return explicit, out, ok
	}
	for _, candidate := range []string{"project_id", "repository", "sub"} {
		if out, ok := claims[candidate]; ok {
			return candidate, out, true
		}
	}
	return "", pulumi.StringOutput{}, false
}

func applyAccountToWIFBroker(b *WIFBroker) {
	if b.Account == nil {
		return
	}
	acc := b.Account.gcpAccountValues()
	if b.GCPProject == nil && isOutputSet(acc.gcpProject) {
		b.GCPProject = nonEmptyStrPtrOutput(acc.gcpProject)
	}
	if b.PoolName == nil && isOutputSet(acc.poolName) {
		b.PoolName = acc.poolName
	}
}

func applyAccountToFederatedIdentity(spec *FederatedIdentityArgs, srcProv string) {
	if spec.Account == nil {
		return
	}
	acc := spec.Account.gcpAccountValues()
	if spec.GCPProject == nil && isOutputSet(acc.gcpProject) {
		spec.GCPProject = nonEmptyStrPtrOutput(acc.gcpProject)
	}
	if spec.PoolName == nil && isOutputSet(acc.poolName) {
		spec.PoolName = acc.poolName
	}
	if spec.OIDCProvider == nil && acc.oidcProvider != nil {
		spec.OIDCProvider = acc.oidcProvider(srcProv)
	}
}

func provisionFederatedIdentity(
	ctx *pulumi.Context,
	meta core.ResourceMeta,
	spec FederatedIdentityArgs,
	res *FederatedIdentity,
) error {
	srcProv := defaultStr(spec.SourceProvider, "external")
	applyAccountToFederatedIdentity(&spec, srcProv)
	claimKey := defaultStr(spec.ClaimKey, "project_id")
	poolOut := toStringOutOrEmpty(spec.PoolName)
	claimOut := toStringOutOrEmpty(spec.ClaimValue)
	sa, err := createFederatedSA(ctx, spec.GCPProject, meta, srcProv, poolOut, claimKey, claimOut, res)
	if err != nil {
		return err
	}
	res.ServiceAccount = sa
	res.ServiceAccountEmail = sa.Email
	res.Member = pulumi.Sprintf("serviceAccount:%s", sa.Email)
	res.ProviderAudience = formatProviderAudience(spec.OIDCProvider)
	return nil
}

func formatProviderAudience(provider pulumi.StringInput) pulumi.StringOutput {
	if provider == nil {
		return pulumi.String("").ToStringOutput()
	}
	return provider.ToStringOutput().ApplyT(func(v string) string {
		if strings.TrimSpace(v) == "" {
			return ""
		}
		return "//iam.googleapis.com/" + v
	}).(pulumi.StringOutput)
}

func toStringOutOrEmpty(in pulumi.StringInput) pulumi.StringOutput {
	if in == nil {
		return pulumi.String("").ToStringOutput()
	}
	return in.ToStringOutput()
}

func createFederatedSA(
	ctx *pulumi.Context,
	gcpProject pulumi.StringPtrInput,
	consumer core.ResourceMeta,
	sourceProvider string,
	poolOut pulumi.StringOutput,
	claimKey string,
	claimVal pulumi.StringOutput,
	parent pulumi.Resource,
) (*serviceaccount.Account, error) {
	saID, err := core.ServiceAccountID(consumer.Project, consumer.Environment, consumer.Name+"-f")
	if err != nil {
		return nil, err
	}
	var opts []pulumi.ResourceOption
	if parent != nil {
		opts = append(opts, pulumi.Parent(parent))
	}
	resPrefix := consumer.Name + "-gcp-wif"
	sa, err := serviceaccount.NewAccount(ctx, resPrefix+"-sa", &serviceaccount.AccountArgs{
		Project:     gcpProject,
		AccountId:   pulumi.String(saID),
		DisplayName: pulumi.String(core.BoundedName(consumer.CanonicalName+"-"+sourceProvider, 63)),
	}, opts...)
	if err != nil {
		return nil, err
	}
	member := pulumi.Sprintf("principalSet://iam.googleapis.com/%s/attribute.%s/%s", poolOut, claimKey, claimVal)
	_, err = serviceaccount.NewIAMMember(ctx, resPrefix+"-user", &serviceaccount.IAMMemberArgs{
		ServiceAccountId: sa.Name,
		Role:             pulumi.String("roles/iam.workloadIdentityUser"),
		Member:           member,
	}, opts...)
	return sa, err
}

func assembleCloudRunCallbackSet(
	project, projectNumber, loc, baseDomain, cb, stageURL, prodURL string,
) CloudRunCallbackURLSet {
	hashWildcard := fmt.Sprintf("https://%s-*-uc.a.run.app", project)
	nonProdR := []string{hashWildcard + cb}
	nonProdL := []string{hashWildcard + "/"}
	if projectNumber != "" {
		regWildcard := fmt.Sprintf("https://%s-*-%s.%s.run.app", project, projectNumber, loc)
		nonProdR = append(nonProdR, regWildcard+cb)
		nonProdL = append(nonProdL, regWildcard+"/")
	}
	stageHash := fmt.Sprintf("https://%s-stage-*-uc.a.run.app", project)
	prodHash := fmt.Sprintf("https://%s-prod-*-uc.a.run.app", project)
	prodR := []string{stageHash + cb, prodHash + cb}
	prodL := []string{stageHash + "/", prodHash + "/"}
	if core.NormalizeDomain(baseDomain) != "" {
		prodR = append(prodR, stageURL+cb, prodURL+cb)
		prodL = append(prodL, stageURL+"/", prodURL+"/")
		return CloudRunCallbackURLSet{
			ExtraNonProdRedirects: nonProdR,
			ExtraNonProdLogouts:   nonProdL,
			ExtraProdRedirect:     prodR,
			ExtraProdLogout:       prodL,
		}
	}
	return CloudRunCallbackURLSet{
		StageHost:             strings.TrimPrefix(stageURL, "https://"),
		ProdHost:              strings.TrimPrefix(prodURL, "https://"),
		ExtraNonProdRedirects: nonProdR,
		ExtraNonProdLogouts:   nonProdL,
		ExtraProdRedirect:     prodR,
		ExtraProdLogout:       prodL,
	}
}
