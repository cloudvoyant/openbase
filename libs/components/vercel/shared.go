// Package vercel provides OpenBase Pulumi components for Vercel web applications and Blob storage.
package vercel

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

// SharedArgs configures cross-environment Vercel Project, CustomEnvironment, ProtectionBypass, and optional BlobStore.
type SharedArgs struct {
	Project               string
	Repo                  string
	Environment           string
	VercelProjectName     string
	TeamID                string
	TeamSlug              string
	Framework             string
	RootDirectory         string
	OIDCIssuerMode        string
	DeploymentProtection  string
	StageEnvironmentSlug  string
	SkipStageEnvironment  bool
	StageDomain           string
	ProdDomain            string
	EnableBlobStore       bool
	BlobAccess            string
	BlobRegion            string
	ProtectionBypassToken string
}

// Shared provisions the shared Vercel Project, staging CustomEnvironment, domains, and automation bypass secret.
type Shared struct {
	pulumi.ResourceState
	core.StatelessMeta

	Project                  *vercelprov.Project
	StageEnvironment         *vercelprov.CustomEnvironment
	ProtectionBypass         *vercelprov.ProjectProtectionBypass
	ProdProjectDomain        *vercelprov.ProjectDomain
	StageProjectDomain       *vercelprov.ProjectDomain
	BlobStore                *vercelprov.BlobStore
	BlobConnection           *vercelprov.BlobProjectConnection
	ProjectID                pulumi.StringOutput
	VercelProjectName        pulumi.StringOutput
	TeamID                   pulumi.StringOutput
	TeamSlug                 pulumi.StringOutput
	StageCustomEnvironmentID pulumi.StringOutput
	ProtectionBypassSecret   pulumi.StringOutput
	ProdDomainName           pulumi.StringOutput
	StageDomainName          pulumi.StringOutput
	BlobStoreID              pulumi.StringOutput
	BlobBaseURL              pulumi.StringOutput
}

// NewShared creates cross-environment Vercel project prerequisites in the "shared" stack.
func NewShared(ctx *pulumi.Context, name string, args SharedArgs, opts ...pulumi.ResourceOption) (*Shared, error) {
	res := &Shared{}
	env := defaultVercelStr(args.Environment, string(core.EnvShared))
	meta, err := core.InitStateless(ctx, "openbase:vercel:Shared", name, "vercel-shared", res, &res.StatelessMeta, core.StatelessResourceArgs{
		Project:     args.Project,
		Repo:        args.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionSharedVercel(ctx, name, meta, args, res); err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			"VERCEL_PROJECT_ID": res.ProjectID,
		},
		Secrets: map[string]pulumi.StringOutput{
			"VERCEL_AUTOMATION_BYPASS_SECRET": res.ProtectionBypassSecret,
		},
	})
	if err := registerSharedVercelOutputs(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

// DeriveBlobBaseURL computes https://<slug>.public.blob.vercel-storage.com from a Vercel store_... ID.
func DeriveBlobBaseURL(storeID pulumi.StringInput) pulumi.StringOutput {
	return storeID.ToStringOutput().ApplyT(func(id string) string {
		slug := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "store_"))
		if slug == "" {
			return ""
		}
		return fmt.Sprintf("https://%s.public.blob.vercel-storage.com", slug)
	}).(pulumi.StringOutput)
}

// Private helpers ------------------------------------------------------------

func provisionSharedVercel(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args SharedArgs,
	res *Shared,
) error {
	proj, vProjName, err := createVercelProject(ctx, name, meta, args, res)
	if err != nil {
		return err
	}
	res.Project = proj
	res.ProjectID = proj.ID().ToStringOutput()
	res.VercelProjectName = pulumi.String(vProjName).ToStringOutput()
	res.TeamID = proj.TeamId
	res.TeamSlug = pulumi.String(args.TeamSlug).ToStringOutput()

	if err := createStageCustomEnv(ctx, name, args, proj, res); err != nil {
		return err
	}
	if err := createSharedDomains(ctx, name, vProjName, args, proj, res); err != nil {
		return err
	}
	if err := createAutomationBypass(ctx, name, args, proj, res); err != nil {
		return err
	}
	return createOptionalSharedBlob(ctx, name, meta, args, proj, res)
}

func createVercelProject(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args SharedArgs,
	parent pulumi.Resource,
) (*vercelprov.Project, string, error) {
	vProjName := defaultVercelStr(args.VercelProjectName, core.BoundedName("openbase-"+meta.Project, 52))
	issuerMode := defaultVercelStr(args.OIDCIssuerMode, "team")
	protMode := defaultVercelStr(args.DeploymentProtection, "standard_protection_new")
	projArgs := &vercelprov.ProjectArgs{
		Name:          pulumi.String(vProjName),
		TeamId:        optionalVercelStr(args.TeamID),
		Framework:     optionalVercelStr(args.Framework),
		RootDirectory: optionalVercelStr(args.RootDirectory),
		IgnoreCommand: pulumi.String("exit 0"),
		AutomaticallyExposeSystemEnvironmentVariables: pulumi.Bool(true),
		OidcTokenConfig: &vercelprov.ProjectOidcTokenConfigArgs{
			IssuerMode: pulumi.String(issuerMode),
		},
		VercelAuthentication: &vercelprov.ProjectVercelAuthenticationArgs{
			DeploymentType: pulumi.String(protMode),
		},
	}
	proj, err := vercelprov.NewProject(ctx, name+"-project", projArgs, pulumi.Parent(parent))
	return proj, vProjName, err
}

func createStageCustomEnv(
	ctx *pulumi.Context,
	name string,
	args SharedArgs,
	proj *vercelprov.Project,
	res *Shared,
) error {
	if args.SkipStageEnvironment {
		res.StageCustomEnvironmentID = pulumi.String("").ToStringOutput()
		return nil
	}
	slug := defaultVercelStr(args.StageEnvironmentSlug, "staging")
	ce, err := vercelprov.NewCustomEnvironment(ctx, name+"-stage-env", &vercelprov.CustomEnvironmentArgs{
		ProjectId:   proj.ID(),
		TeamId:      optionalVercelStr(args.TeamID),
		Name:        pulumi.String(slug),
		Description: pulumi.String("OpenBase staging environment"),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.StageEnvironment = ce
	res.StageCustomEnvironmentID = ce.ID().ToStringOutput()
	return nil
}

func createSharedDomains(
	ctx *pulumi.Context,
	name, vProjName string,
	args SharedArgs,
	proj *vercelprov.Project,
	res *Shared,
) error {
	prodDom := args.ProdDomain
	if prodDom == "" && args.TeamSlug != "" {
		prodDom = fmt.Sprintf("%s-%s.vercel.app", vProjName, args.TeamSlug)
	}
	stageDom := args.StageDomain
	if stageDom == "" && args.TeamSlug != "" {
		stageDom = fmt.Sprintf("%s-stage-%s.vercel.app", vProjName, args.TeamSlug)
	}
	res.ProdDomainName = pulumi.String(prodDom).ToStringOutput()
	res.StageDomainName = pulumi.String(stageDom).ToStringOutput()
	if prodDom != "" {
		pd, err := vercelprov.NewProjectDomain(ctx, name+"-prod-domain", &vercelprov.ProjectDomainArgs{
			ProjectId:    proj.ID(),
			TeamId:       optionalVercelStr(args.TeamID),
			Domain:       pulumi.String(prodDom),
			WaitForReady: pulumi.Bool(true),
		}, pulumi.Parent(res))
		if err != nil {
			return err
		}
		res.ProdProjectDomain = pd
	}
	return attachStageDomain(ctx, name, stageDom, args, proj, res)
}

func attachStageDomain(
	ctx *pulumi.Context,
	name, stageDom string,
	args SharedArgs,
	proj *vercelprov.Project,
	res *Shared,
) error {
	if stageDom == "" || res.StageEnvironment == nil {
		return nil
	}
	sd, err := vercelprov.NewProjectDomain(ctx, name+"-stage-domain", &vercelprov.ProjectDomainArgs{
		ProjectId:           proj.ID(),
		TeamId:              optionalVercelStr(args.TeamID),
		Domain:              pulumi.String(stageDom),
		CustomEnvironmentId: res.StageEnvironment.ID(),
		WaitForReady:        pulumi.Bool(true),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.StageProjectDomain = sd
	return nil
}

func createAutomationBypass(
	ctx *pulumi.Context,
	name string,
	args SharedArgs,
	proj *vercelprov.Project,
	res *Shared,
) error {
	bpArgs := &vercelprov.ProjectProtectionBypassArgs{
		ProjectId: proj.ID(),
		TeamId:    optionalVercelStr(args.TeamID),
		Note:      pulumi.String("OpenBase automation bypass"),
		Secret:    optionalVercelStr(args.ProtectionBypassToken),
	}
	bp, err := vercelprov.NewProjectProtectionBypass(ctx, name+"-bypass", bpArgs, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.ProtectionBypass = bp
	res.ProtectionBypassSecret = pulumi.ToSecret(bp.Secret).(pulumi.StringOutput)
	return nil
}

func createOptionalSharedBlob(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args SharedArgs,
	proj *vercelprov.Project,
	res *Shared,
) error {
	if !args.EnableBlobStore {
		res.BlobStoreID = pulumi.String("").ToStringOutput()
		res.BlobBaseURL = pulumi.String("").ToStringOutput()
		return nil
	}
	store, err := vercelprov.NewBlobStore(ctx, name+"-blob", &vercelprov.BlobStoreArgs{
		Name:   pulumi.String(core.BoundedName(meta.Project+"-blob", 52)),
		Access: pulumi.String(defaultVercelStr(args.BlobAccess, "public")),
		Region: pulumi.String(defaultVercelStr(args.BlobRegion, "iad1")),
		TeamId: optionalVercelStr(args.TeamID),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	conn, err := vercelprov.NewBlobProjectConnection(ctx, name+"-blob-conn", &vercelprov.BlobProjectConnectionArgs{
		BlobStoreId:  store.ID(),
		ProjectId:    proj.ID(),
		TeamId:       optionalVercelStr(args.TeamID),
		Environments: pulumi.ToStringArray([]string{"development", "preview", "production"}),
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.BlobStore = store
	res.BlobConnection = conn
	res.BlobStoreID = store.ID().ToStringOutput()
	res.BlobBaseURL = DeriveBlobBaseURL(store.ID())
	return nil
}

func registerSharedVercelOutputs(ctx *pulumi.Context, res *Shared) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"projectId":                res.ProjectID,
		"vercelProjectName":        res.VercelProjectName,
		"teamId":                   res.TeamID,
		"teamSlug":                 res.TeamSlug,
		"stageCustomEnvironmentId": res.StageCustomEnvironmentID,
		"protectionBypassSecret":   res.ProtectionBypassSecret,
		"prodDomainName":           res.ProdDomainName,
		"stageDomainName":          res.StageDomainName,
		"blobStoreId":              res.BlobStoreID,
		"blobBaseUrl":              res.BlobBaseURL,
	})
}

func defaultVercelStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func optionalVercelStr(val string) pulumi.StringPtrInput {
	if val == "" {
		return nil
	}
	return pulumi.String(val)
}
