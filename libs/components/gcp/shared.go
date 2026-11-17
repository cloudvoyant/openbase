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

// SharedArgs configures cross-environment GCP prerequisites in the "shared" stack.
type SharedArgs struct {
	Project          string
	Repo             string
	Environment      string
	GCPProject       pulumi.StringPtrInput
	Location         string
	PoolSuffix       string
	GitHubOrg        string
	GitHubRepo       string
	VercelTeamSlug   string
	VercelIssuerMode string
	Services         []string
	SkipKeyRing      bool
	ExtraLabels      map[string]string
}

// Shared provisions Artifact Registry, Workload Identity Federation, KMS, and required GCP APIs.
type Shared struct {
	pulumi.ResourceState
	core.StatelessMeta

	EnabledServices          []*projects.Service
	Repository               *artifactregistry.Repository
	WorkloadIdentityPool     *iam.WorkloadIdentityPool
	GitHubOIDCProvider       *iam.WorkloadIdentityPoolProvider
	VercelOIDCProvider       *iam.WorkloadIdentityPoolProvider
	KeyRing                  *kms.KeyRing
	RepositoryID             pulumi.StringOutput
	RepositoryURL            pulumi.StringOutput
	WorkloadIdentityPoolName pulumi.StringOutput
	VercelOIDCProviderName   pulumi.StringOutput
	KeyRingID                pulumi.StringOutput
}

// NewShared registers cross-environment GCP prerequisites for consumption via StackReference.
func NewShared(ctx *pulumi.Context, name string, args SharedArgs, opts ...pulumi.ResourceOption) (*Shared, error) {
	res := &Shared{}
	env := args.Environment
	if env == "" {
		env = string(core.EnvShared)
	}
	meta, err := core.InitStateless(ctx, "openbase:gcp:Shared", name, "gcp-shared", res, &res.StatelessMeta, core.StatelessResourceArgs{
		Project:     args.Project,
		Repo:        args.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionSharedResources(ctx, name, meta, args, res); err != nil {
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

func provisionSharedResources(ctx *pulumi.Context, name string, meta core.ResourceMeta, args SharedArgs, res *Shared) error {
	location := defaultStr(args.Location, "us-central1")
	svcs, err := enableProjectServices(ctx, name, args, res)
	if err != nil {
		return err
	}
	res.EnabledServices = svcs

	repo, err := createArtifactRepository(ctx, name, location, meta, args, res)
	if err != nil {
		return err
	}
	res.Repository = repo
	res.RepositoryID = repo.RepositoryId
	res.RepositoryURL = pulumi.Sprintf("%s-docker.pkg.dev/%s/%s", location, repo.Project, repo.RepositoryId)

	if err := createIdentityFederation(ctx, name, meta, args, res); err != nil {
		return err
	}
	return createOptionalKeyRing(ctx, name, location, meta, args, res)
}

func enableProjectServices(ctx *pulumi.Context, name string, args SharedArgs, parent pulumi.Resource) ([]*projects.Service, error) {
	list := args.Services
	if len(list) == 0 {
		list = defaultSharedServices
	}
	out := make([]*projects.Service, 0, len(list))
	for _, api := range list {
		short := strings.TrimSuffix(api, ".googleapis.com")
		svc, err := projects.NewService(ctx, fmt.Sprintf("%s-api-%s", name, short), &projects.ServiceArgs{
			Project:                  args.GCPProject,
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
	args SharedArgs,
	parent pulumi.Resource,
) (*artifactregistry.Repository, error) {
	repoID := core.BoundedName(meta.CanonicalName, 63)
	return artifactregistry.NewRepository(ctx, name+"-ar", &artifactregistry.RepositoryArgs{
		Project:      args.GCPProject,
		Location:     pulumi.String(location),
		RepositoryId: pulumi.String(repoID),
		Format:       pulumi.String("DOCKER"),
		Description:  pulumi.String("OpenBase shared container registry for " + meta.Project),
		Labels:       core.GCPLabels(meta, args.ExtraLabels),
	}, pulumi.Parent(parent))
}

func createIdentityFederation(ctx *pulumi.Context, name string, meta core.ResourceMeta, args SharedArgs, res *Shared) error {
	rawPool := "ob-" + meta.Project + "-pool"
	if args.PoolSuffix != "" {
		rawPool += "-" + args.PoolSuffix
	}
	poolID := core.BoundedName(rawPool, 32)
	pool, err := iam.NewWorkloadIdentityPool(ctx, name+"-wif", &iam.WorkloadIdentityPoolArgs{
		Project:                args.GCPProject,
		WorkloadIdentityPoolId: pulumi.String(poolID),
		DisplayName:            pulumi.String(core.BoundedName(meta.Project+"-wif", 32)),
		Description:            pulumi.String("OpenBase OIDC pool for " + meta.Project),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.WorkloadIdentityPool = pool
	res.WorkloadIdentityPoolName = pool.Name

	if err := createGitHubProvider(ctx, name, args, pool, res); err != nil {
		return err
	}
	return createVercelProvider(ctx, name, args, pool, res)
}

func createGitHubProvider(
	ctx *pulumi.Context,
	name string,
	args SharedArgs,
	pool *iam.WorkloadIdentityPool,
	res *Shared,
) error {
	if args.GitHubOrg == "" && args.GitHubRepo == "" {
		return nil
	}
	cond := fmt.Sprintf("assertion.repository_owner == '%s'", args.GitHubOrg)
	if args.GitHubRepo != "" {
		cond = fmt.Sprintf("assertion.repository == '%s'", args.GitHubRepo)
	}
	prov, err := iam.NewWorkloadIdentityPoolProvider(ctx, name+"-github-oidc", &iam.WorkloadIdentityPoolProviderArgs{
		Project:                        args.GCPProject,
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
	args SharedArgs,
	pool *iam.WorkloadIdentityPool,
	res *Shared,
) error {
	if args.VercelTeamSlug == "" {
		res.VercelOIDCProviderName = pulumi.String("").ToStringOutput()
		return nil
	}
	issuer := "https://oidc.vercel.com/" + args.VercelTeamSlug
	if args.VercelIssuerMode == "global" {
		issuer = "https://oidc.vercel.com"
	}
	prov, err := iam.NewWorkloadIdentityPoolProvider(ctx, name+"-vercel-oidc", &iam.WorkloadIdentityPoolProviderArgs{
		Project:                        args.GCPProject,
		WorkloadIdentityPoolId:         pool.WorkloadIdentityPoolId,
		WorkloadIdentityPoolProviderId: pulumi.String("vercel-oidc"),
		DisplayName:                    pulumi.String("Vercel OIDC"),
		AttributeCondition:             pulumi.String(fmt.Sprintf("assertion.owner == '%s'", args.VercelTeamSlug)),
		AttributeMapping: pulumi.StringMap{
			"google.subject":        pulumi.String("assertion.sub"),
			"attribute.project_id":  pulumi.String("assertion.project_id"),
			"attribute.project":     pulumi.String("assertion.project"),
			"attribute.owner":       pulumi.String("assertion.owner"),
			"attribute.environment": pulumi.String("assertion.environment"),
		},
		Oidc: &iam.WorkloadIdentityPoolProviderOidcArgs{
			IssuerUri:        pulumi.String(issuer),
			AllowedAudiences: pulumi.ToStringArray([]string{"https://vercel.com/" + args.VercelTeamSlug}),
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
	args SharedArgs,
	res *Shared,
) error {
	if args.SkipKeyRing {
		res.KeyRingID = pulumi.String("").ToStringOutput()
		return nil
	}
	ringName := core.BoundedName(meta.CanonicalName+"-kr", 63)
	kr, err := kms.NewKeyRing(ctx, name+"-kr", &kms.KeyRingArgs{
		Project:  args.GCPProject,
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

func buildSharedLink(meta core.ResourceMeta, res *Shared) core.LinkDescriptor {
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			"GCP_ARTIFACT_REGISTRY_URL":  res.RepositoryURL,
			"GCP_WORKLOAD_IDENTITY_POOL": res.WorkloadIdentityPoolName,
		},
	}
}

func registerSharedOutputs(ctx *pulumi.Context, res *Shared) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"repositoryId":             res.RepositoryID,
		"repositoryUrl":            res.RepositoryURL,
		"workloadIdentityPoolName": res.WorkloadIdentityPoolName,
		"vercelOidcProviderName":   res.VercelOIDCProviderName,
		"keyRingId":                res.KeyRingID,
	})
}

func defaultStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}
