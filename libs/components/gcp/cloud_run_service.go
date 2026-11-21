package gcp

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/artifactregistry"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/cloudrun"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/cloudrunv2"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/secretmanager"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/serviceaccount"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*CloudRunService)(nil)

const (
	// VisibilityPublic allows unauthenticated internet invocations (allUsers roles/run.invoker).
	VisibilityPublic = "public"
	// VisibilityInternal requires Google Cloud IAM authentication (roles/run.invoker).
	VisibilityInternal = "internal"
	// VisibilityWorker restricts ingress to internal VPC/Google traffic with IAM enforcement.
	VisibilityWorker = "worker"
)

var (
	// ErrInvalidVisibility reports an unsupported CloudRunService visibility mode.
	ErrInvalidVisibility = errors.New("gcp: invalid visibility (expected public, internal, or worker)")
	// ErrImageRequired reports a missing container image on CloudRunServiceArgs.
	ErrImageRequired = errors.New("gcp: container image is required")
)

// ProbeConfig configures an HTTP startup or liveness check on a Cloud Run container.
type ProbeConfig struct {
	Path                string
	Port                int
	InitialDelaySeconds int
	PeriodSeconds       int
	FailureThreshold    int
}

// CloudRunServiceArgs configures a Google Cloud Run v2 Service component.
type CloudRunServiceArgs struct {
	ProjectName              string
	Repo                     string
	Environment              string
	Kind                     string
	Links                    []core.Resource
	Env                      map[string]pulumi.StringInput
	Secrets                  map[string]pulumi.StringInput
	Account                  AccountInput
	Project                  ProjectInput
	GCPProject               pulumi.StringPtrInput
	GCPProjectNumber         pulumi.StringInput
	ArtifactRegistryRepo     pulumi.StringInput
	ArtifactRegistryLocation string
	Location                 string
	Image                    pulumi.StringInput
	Command                  []string
	ContainerArgs            []string
	Visibility               string
	Ingress                  string
	Port                     int
	MinInstances             *int
	MaxInstances             *int
	AllowedMembers           []pulumi.StringInput
	BaseDomain               string
	Domain                   string
	Domains                  []string
	StartupProbe             *ProbeConfig
	LivenessProbe            *ProbeConfig
	ExtraLabels              map[string]string
}

// CloudRunService is the provisioned Cloud Run v2 Service handle with dedicated SA, secrets, and IAM Links.
type CloudRunService struct {
	pulumi.ResourceState
	core.StatelessMeta

	ServiceAccount      *serviceaccount.Account
	Service             *cloudrunv2.Service
	DomainMapping       *cloudrun.DomainMapping
	DomainMappings      []*cloudrun.DomainMapping
	URL                 pulumi.StringOutput
	RegionalURL         pulumi.StringOutput
	CustomURL           pulumi.StringOutput
	URLs                pulumi.StringArrayOutput
	ServiceAccountEmail pulumi.StringOutput
	ServiceAccountID    string
}

// NewCloudRunService provisions a Cloud Run v2 Service satisfying core.StatelessResource.
func NewCloudRunService(
	ctx *pulumi.Context,
	name string,
	args *CloudRunServiceArgs,
	opts ...pulumi.ResourceOption,
) (*CloudRunService, error) {
	if args == nil {
		args = &CloudRunServiceArgs{}
	}
	if err := validateCloudRunSpec(*args); err != nil {
		return nil, err
	}
	spec := *args
	if spec.Account != nil && spec.GCPProject == nil {
		acc := spec.Account.gcpAccountValues()
		if isOutputSet(acc.gcpProject) {
			spec.GCPProject = nonEmptyStrPtrOutput(acc.gcpProject)
		}
	}
	res := &CloudRunService{}
	meta, err := core.InitStateless(ctx, "openbase:gcp:CloudRunService", "gcp-cloud-run", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        name,
		Project:     spec.ProjectName,
		Repo:        spec.Repo,
		Environment: spec.Environment,
		Kind:        spec.Kind,
		Links:       spec.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := buildCloudRunResources(ctx, name, meta, spec, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, buildCloudRunLink(name, meta, spec, res))
	return res, registerCloudRunOutputs(ctx, meta, res)
}

// CloudRunURL returns the deterministic regional Cloud Run v2 URL for a service before deployment.
func CloudRunURL(project string, env core.Environment, name, projectNumber, location string) (string, error) {
	return CloudRunRegionalURL(project, env, name, projectNumber, location, "")
}

// CloudRunRegionalURL returns the deterministic regional *.run.app URL, accounting for BaseDomain service naming when set.
func CloudRunRegionalURL(project string, env core.Environment, name, projectNumber, location, baseDomain string) (string, error) {
	svcName, err := CloudRunServiceName(project, env, name, baseDomain)
	if err != nil {
		return "", err
	}
	loc := defaultStr(location, "us-central1")
	return fmt.Sprintf("https://%s-%s.%s.run.app", svcName, strings.TrimSpace(projectNumber), loc), nil
}

// CloudRunServiceName returns the Cloud Run v2 service name ("{project}-{envLabel}" when BaseDomain is set, else "{project}-{env}-{name}").
func CloudRunServiceName(project string, env core.Environment, name, baseDomain string) (string, error) {
	if core.NormalizeDomain(baseDomain) != "" {
		return core.HostLabel(project, env, name)
	}
	canonical, err := core.CanonicalName(project, env, name)
	if err != nil {
		return "", err
	}
	return core.BoundedName(canonical, 49), nil
}

// Private helpers ------------------------------------------------------------

type sharedRefs struct {
	repoID pulumi.StringOutput
}

func validateCloudRunSpec(spec CloudRunServiceArgs) error {
	if spec.Image == nil {
		return ErrImageRequired
	}
	switch defaultStr(spec.Visibility, VisibilityPublic) {
	case VisibilityPublic, VisibilityInternal, VisibilityWorker:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidVisibility, spec.Visibility)
	}
}

func buildCloudRunResources(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec CloudRunServiceArgs,
	res *CloudRunService,
) error {
	sa, saID, selfTokenGrant, err := createServiceAccount(ctx, name, meta, spec, res)
	if err != nil {
		return err
	}
	res.ServiceAccount = sa
	res.ServiceAccountID = saID
	res.ServiceAccountEmail = sa.Email

	refs := resolveProjectRefs(spec)
	if err := grantArtifactRegistryAccess(ctx, name, spec, refs, sa, res); err != nil {
		return err
	}
	envs, deps, err := resolveEnvsAndSecrets(ctx, name, meta, spec, sa, res)
	if err != nil {
		return err
	}
	deps = append(deps, selfTokenGrant)
	svc, err := createCloudRunV2(ctx, name, meta, spec, sa, envs, deps, res)
	if err != nil {
		return err
	}
	res.Service = svc
	res.RegionalURL = computeRegionalCloudRunURL(meta, spec, svc.Uri)
	res.CustomURL = computeCustomBaseURL(meta, spec)
	res.URL = selectPrimaryCloudRunURL(svc.Uri, res.CustomURL, spec)
	res.URLs = collectCloudRunURLs(svc.Uri, res.CustomURL, spec)
	if err := configureInvokerIAM(ctx, name, spec, svc, res); err != nil {
		return err
	}
	return createOptionalDomainMappings(ctx, name, meta, spec, svc, res)
}

func createServiceAccount(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec CloudRunServiceArgs,
	parent pulumi.Resource,
) (*serviceaccount.Account, string, *serviceaccount.IAMMember, error) {
	saID, err := core.ServiceAccountID(meta.Project, meta.Environment, name)
	if err != nil {
		return nil, "", nil, err
	}
	sa, err := serviceaccount.NewAccount(ctx, name+"-sa", &serviceaccount.AccountArgs{
		Project:     spec.GCPProject,
		AccountId:   pulumi.String(saID),
		DisplayName: pulumi.String(meta.CanonicalName),
	}, pulumi.Parent(parent))
	if err != nil {
		return nil, "", nil, err
	}
	selfGrant, err := serviceaccount.NewIAMMember(ctx, name+"-sa-token-creator", &serviceaccount.IAMMemberArgs{
		ServiceAccountId: sa.Name,
		Role:             pulumi.String("roles/iam.serviceAccountTokenCreator"),
		Member:           pulumi.Sprintf("serviceAccount:%s", sa.Email),
	}, pulumi.Parent(parent))
	if err != nil {
		return nil, "", nil, err
	}
	return sa, saID, selfGrant, nil
}

func resolveProjectRefs(spec CloudRunServiceArgs) sharedRefs {
	var refs sharedRefs
	if spec.Project != nil {
		refs.repoID = spec.Project.gcpProjectValues().repositoryID
	}
	if spec.ArtifactRegistryRepo != nil {
		refs.repoID = spec.ArtifactRegistryRepo.ToStringOutput()
	}
	return refs
}

func grantArtifactRegistryAccess(
	ctx *pulumi.Context,
	name string,
	spec CloudRunServiceArgs,
	refs sharedRefs,
	sa *serviceaccount.Account,
	parent pulumi.Resource,
) error {
	if !isOutputSet(refs.repoID) {
		return nil
	}
	arLoc := defaultStr(spec.ArtifactRegistryLocation, defaultStr(spec.Location, "us-central1"))
	_, err := artifactregistry.NewRepositoryIamMember(ctx, name+"-ar-sa", &artifactregistry.RepositoryIamMemberArgs{
		Project:    spec.GCPProject,
		Location:   pulumi.String(arLoc),
		Repository: refs.repoID,
		Role:       pulumi.String("roles/artifactregistry.reader"),
		Member:     pulumi.Sprintf("serviceAccount:%s", sa.Email),
	}, pulumi.Parent(parent))
	if err != nil || spec.GCPProjectNumber == nil {
		return err
	}
	_, err = artifactregistry.NewRepositoryIamMember(ctx, name+"-ar-robot", &artifactregistry.RepositoryIamMemberArgs{
		Project:    spec.GCPProject,
		Location:   pulumi.String(arLoc),
		Repository: refs.repoID,
		Role:       pulumi.String("roles/artifactregistry.reader"),
		Member:     pulumi.Sprintf("serviceAccount:service-%s@serverless-robot-prod.iam.gserviceaccount.com", spec.GCPProjectNumber),
	}, pulumi.Parent(parent))
	return err
}

func resolveEnvsAndSecrets(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec CloudRunServiceArgs,
	sa *serviceaccount.Account,
	parent pulumi.Resource,
) (cloudrunv2.ServiceTemplateContainerEnvArray, []pulumi.Resource, error) {
	if _, err := core.ResolveLinks(core.LinkGrantContext{
		Ctx:          ctx,
		ConsumerName: meta.CanonicalName,
		ConsumerMeta: meta,
		Environment:  meta.Environment,
		Subject: core.Subject{
			Provider: "gcp",
			NativeID: pulumi.Sprintf("serviceAccount:%s", sa.Email),
		},
	}, spec.Links); err != nil {
		return nil, nil, err
	}
	plain := make(map[string]pulumi.StringOutput, len(spec.Env))
	for k, v := range spec.Env {
		plain[k] = v.ToStringOutput()
	}
	secrets := make(map[string]pulumi.StringOutput, len(spec.Secrets))
	for k, v := range spec.Secrets {
		secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	envs := sortedPlainEnvs(plain)
	secretEnvs, deps, err := createSecretMounts(ctx, name, meta, spec, secrets, sa, parent)
	if err != nil {
		return nil, nil, err
	}
	return append(envs, secretEnvs...), deps, nil
}

func sortedPlainEnvs(plain map[string]pulumi.StringOutput) cloudrunv2.ServiceTemplateContainerEnvArray {
	keys := make([]string, 0, len(plain))
	for k := range plain {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make(cloudrunv2.ServiceTemplateContainerEnvArray, 0, len(keys))
	for _, k := range keys {
		out = append(out, &cloudrunv2.ServiceTemplateContainerEnvArgs{
			Name:  pulumi.String(k),
			Value: plain[k],
		})
	}
	return out
}

func createSecretMounts(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec CloudRunServiceArgs,
	secrets map[string]pulumi.StringOutput,
	sa *serviceaccount.Account,
	parent pulumi.Resource,
) (cloudrunv2.ServiceTemplateContainerEnvArray, []pulumi.Resource, error) {
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var envs cloudrunv2.ServiceTemplateContainerEnvArray
	var deps []pulumi.Resource
	for _, key := range keys {
		envItem, itemDeps, err := createSingleSecretMount(ctx, name, key, secrets[key], meta, spec, sa, parent)
		if err != nil {
			return nil, nil, err
		}
		envs = append(envs, envItem)
		deps = append(deps, itemDeps...)
	}
	return envs, deps, nil
}

func createSingleSecretMount(
	ctx *pulumi.Context,
	name, envKey string,
	secretVal pulumi.StringOutput,
	meta core.ResourceMeta,
	spec CloudRunServiceArgs,
	sa *serviceaccount.Account,
	parent pulumi.Resource,
) (*cloudrunv2.ServiceTemplateContainerEnvArgs, []pulumi.Resource, error) {
	slug := core.BoundedName(strings.ToLower(envKey), 24)
	secretID := core.BoundedName(meta.CanonicalName+"-"+slug, 63)
	sec, err := secretmanager.NewSecret(ctx, name+"-sec-"+slug, &secretmanager.SecretArgs{
		Project:            spec.GCPProject,
		SecretId:           pulumi.String(secretID),
		DeletionProtection: pulumi.Bool(false),
		Labels:             core.GCPLabels(meta, spec.ExtraLabels),
		Replication:        &secretmanager.SecretReplicationArgs{Auto: &secretmanager.SecretReplicationAutoArgs{}},
	}, pulumi.Parent(parent))
	if err != nil {
		return nil, nil, err
	}
	ver, err := secretmanager.NewSecretVersion(ctx, name+"-secver-"+slug, &secretmanager.SecretVersionArgs{
		Secret:     sec.ID(),
		SecretData: secretVal,
	}, pulumi.Parent(parent))
	if err != nil {
		return nil, nil, err
	}
	iamMem, err := secretmanager.NewSecretIamMember(ctx, name+"-seciam-"+slug, &secretmanager.SecretIamMemberArgs{
		Project:  spec.GCPProject,
		SecretId: sec.SecretId,
		Role:     pulumi.String("roles/secretmanager.secretAccessor"),
		Member:   pulumi.Sprintf("serviceAccount:%s", sa.Email),
	}, pulumi.Parent(parent))
	if err != nil {
		return nil, nil, err
	}
	envArg := &cloudrunv2.ServiceTemplateContainerEnvArgs{
		Name: pulumi.String(envKey),
		ValueSource: &cloudrunv2.ServiceTemplateContainerEnvValueSourceArgs{
			SecretKeyRef: &cloudrunv2.ServiceTemplateContainerEnvValueSourceSecretKeyRefArgs{
				Secret:  sec.SecretId,
				Version: pulumi.String("latest"),
			},
		},
	}
	return envArg, []pulumi.Resource{ver, iamMem}, nil
}

func createCloudRunV2(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec CloudRunServiceArgs,
	sa *serviceaccount.Account,
	envs cloudrunv2.ServiceTemplateContainerEnvArray,
	deps []pulumi.Resource,
	parent pulumi.Resource,
) (*cloudrunv2.Service, error) {
	location := defaultStr(spec.Location, "us-central1")
	port := spec.Port
	if port == 0 {
		port = 8080
	}
	minInst, maxInst := resolveScaling(meta, spec)
	container := &cloudrunv2.ServiceTemplateContainerArgs{
		Image:         spec.Image,
		Commands:      toStringArrayOrNil(spec.Command),
		Args:          toStringArrayOrNil(spec.ContainerArgs),
		Envs:          envs,
		Ports:         &cloudrunv2.ServiceTemplateContainerPortsArgs{ContainerPort: pulumi.Int(port)},
		StartupProbe:  buildStartupProbe(spec.StartupProbe, port),
		LivenessProbe: buildLivenessProbe(spec.LivenessProbe, port),
	}
	serviceName, err := CloudRunServiceName(meta.Project, meta.Environment, meta.Name, spec.BaseDomain)
	if err != nil {
		return nil, err
	}
	return cloudrunv2.NewService(ctx, name+"-svc", &cloudrunv2.ServiceArgs{
		Project:            spec.GCPProject,
		Name:               pulumi.String(serviceName),
		Location:           pulumi.String(location),
		DeletionProtection: pulumi.Bool(false),
		Ingress:            pulumi.String(resolveIngress(spec)),
		Labels:             core.GCPLabels(meta, spec.ExtraLabels),
		Template: &cloudrunv2.ServiceTemplateArgs{
			ServiceAccount: sa.Email,
			Labels:         core.GCPLabels(meta, spec.ExtraLabels),
			Scaling: &cloudrunv2.ServiceTemplateScalingArgs{
				MinInstanceCount: pulumi.Int(minInst),
				MaxInstanceCount: pulumi.Int(maxInst),
			},
			Containers: cloudrunv2.ServiceTemplateContainerArray{container},
		},
	}, pulumi.Parent(parent), pulumi.DependsOn(deps))
}

func resolveScaling(meta core.ResourceMeta, spec CloudRunServiceArgs) (int, int) {
	minInst := 0
	if spec.MinInstances != nil {
		minInst = *spec.MinInstances
	}
	maxInst := 10
	if meta.Ephemeral {
		maxInst = 3
	}
	if spec.MaxInstances != nil {
		maxInst = *spec.MaxInstances
	}
	return minInst, maxInst
}

func resolveIngress(spec CloudRunServiceArgs) string {
	if spec.Ingress != "" {
		return spec.Ingress
	}
	if defaultStr(spec.Visibility, VisibilityPublic) == VisibilityWorker {
		return "INGRESS_TRAFFIC_INTERNAL_ONLY"
	}
	return "INGRESS_TRAFFIC_ALL"
}

func buildStartupProbe(cfg *ProbeConfig, defaultPort int) *cloudrunv2.ServiceTemplateContainerStartupProbeArgs {
	if cfg == nil {
		return nil
	}
	port, period, threshold := probeDefaults(cfg, defaultPort)
	return &cloudrunv2.ServiceTemplateContainerStartupProbeArgs{
		InitialDelaySeconds: pulumi.Int(cfg.InitialDelaySeconds),
		PeriodSeconds:       pulumi.Int(period),
		FailureThreshold:    pulumi.Int(threshold),
		HttpGet: &cloudrunv2.ServiceTemplateContainerStartupProbeHttpGetArgs{
			Path: pulumi.String(defaultStr(cfg.Path, "/")),
			Port: pulumi.Int(port),
		},
	}
}

func buildLivenessProbe(cfg *ProbeConfig, defaultPort int) *cloudrunv2.ServiceTemplateContainerLivenessProbeArgs {
	if cfg == nil {
		return nil
	}
	port, period, threshold := probeDefaults(cfg, defaultPort)
	return &cloudrunv2.ServiceTemplateContainerLivenessProbeArgs{
		InitialDelaySeconds: pulumi.Int(cfg.InitialDelaySeconds),
		PeriodSeconds:       pulumi.Int(period),
		FailureThreshold:    pulumi.Int(threshold),
		HttpGet: &cloudrunv2.ServiceTemplateContainerLivenessProbeHttpGetArgs{
			Path: pulumi.String(defaultStr(cfg.Path, "/")),
			Port: pulumi.Int(port),
		},
	}
}

func probeDefaults(cfg *ProbeConfig, defaultPort int) (int, int, int) {
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	period := cfg.PeriodSeconds
	if period == 0 {
		period = 10
	}
	threshold := cfg.FailureThreshold
	if threshold == 0 {
		threshold = 3
	}
	return port, period, threshold
}

func configureInvokerIAM(
	ctx *pulumi.Context,
	name string,
	spec CloudRunServiceArgs,
	svc *cloudrunv2.Service,
	parent pulumi.Resource,
) error {
	vis := defaultStr(spec.Visibility, VisibilityPublic)
	if vis == VisibilityPublic {
		_, err := cloudrunv2.NewServiceIamMember(ctx, name+"-public-invoker", &cloudrunv2.ServiceIamMemberArgs{
			Project:  svc.Project,
			Location: svc.Location,
			Name:     svc.Name,
			Role:     pulumi.String("roles/run.invoker"),
			Member:   pulumi.String("allUsers"),
		}, pulumi.Parent(parent))
		if err != nil {
			return err
		}
	}
	for idx, member := range spec.AllowedMembers {
		_, err := cloudrunv2.NewServiceIamMember(ctx, fmt.Sprintf("%s-invoker-%d", name, idx), &cloudrunv2.ServiceIamMemberArgs{
			Project:  svc.Project,
			Location: svc.Location,
			Name:     svc.Name,
			Role:     pulumi.String("roles/run.invoker"),
			Member:   member,
		}, pulumi.Parent(parent))
		if err != nil {
			return err
		}
	}
	return nil
}

func computeRegionalCloudRunURL(meta core.ResourceMeta, spec CloudRunServiceArgs, fallbackURI pulumi.StringOutput) pulumi.StringOutput {
	if spec.GCPProjectNumber == nil {
		return fallbackURI
	}
	loc := defaultStr(spec.Location, "us-central1")
	return spec.GCPProjectNumber.ToStringOutput().ApplyT(func(num string) string {
		if strings.TrimSpace(num) == "" {
			return ""
		}
		u, err := CloudRunRegionalURL(meta.Project, meta.Environment, meta.Name, num, loc, spec.BaseDomain)
		if err != nil {
			return ""
		}
		return u
	}).(pulumi.StringOutput)
}

func computeCustomBaseURL(meta core.ResourceMeta, spec CloudRunServiceArgs) pulumi.StringOutput {
	if dom, err := core.EnvironmentDomain(meta.Project, meta.Environment, meta.Name, spec.BaseDomain); err == nil && dom != "" {
		return pulumi.String("https://" + dom).ToStringOutput()
	}
	return pulumi.String("").ToStringOutput()
}

func selectPrimaryCloudRunURL(svcURI, customURL pulumi.StringOutput, spec CloudRunServiceArgs) pulumi.StringOutput {
	if core.NormalizeDomain(spec.BaseDomain) != "" {
		return customURL
	}
	return svcURI
}

func collectCloudRunURLs(svcURI, customURL pulumi.StringOutput, spec CloudRunServiceArgs) pulumi.StringArrayOutput {
	mapped := core.DedupeDomains([]string{spec.Domain}, spec.Domains)
	return pulumi.All(svcURI, customURL).ApplyT(func(vs []any) []string {
		uri, custom := vs[0].(string), vs[1].(string)
		var out []string
		if custom != "" {
			out = append(out, custom)
		}
		for _, d := range mapped {
			out = append(out, "https://"+d)
		}
		if uri != "" {
			out = append(out, uri)
		}
		return out
	}).(pulumi.StringArrayOutput)
}

func createOptionalDomainMappings(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec CloudRunServiceArgs,
	svc *cloudrunv2.Service,
	res *CloudRunService,
) error {
	domains := core.DedupeDomains([]string{spec.Domain}, spec.Domains)
	if len(domains) == 0 || !meta.Environment.IsPersistent() {
		return nil
	}
	for idx, dom := range domains {
		resName := name + "-domain"
		if idx > 0 {
			resName = fmt.Sprintf("%s-domain-%d", name, idx)
		}
		dm, err := cloudrun.NewDomainMapping(ctx, resName, &cloudrun.DomainMappingArgs{
			Project:  svc.Project,
			Location: svc.Location,
			Name:     pulumi.String(dom),
			Metadata: &cloudrun.DomainMappingMetadataArgs{
				Namespace: svc.Project,
				Labels:    core.GCPLabels(meta, spec.ExtraLabels),
			},
			Spec: &cloudrun.DomainMappingSpecArgs{
				RouteName: svc.Name,
			},
		}, pulumi.Parent(res))
		if err != nil {
			return err
		}
		if idx == 0 {
			res.DomainMapping = dm
		}
		res.DomainMappings = append(res.DomainMappings, dm)
	}
	return nil
}

func buildCloudRunLink(name string, meta core.ResourceMeta, spec CloudRunServiceArgs, res *CloudRunService) core.LinkDescriptor {
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"url":                 res.URL,
			"regionalUrl":         res.RegionalURL,
			"serviceAccountEmail": res.ServiceAccountEmail,
		},
		StackExports: map[string]pulumi.Input{
			"url":                 res.URL,
			"urls":                res.URLs,
			"regionalUrl":         res.RegionalURL,
			"serviceName":         res.Service.Name,
			"serviceAccountEmail": res.ServiceAccountEmail,
		},
		GrantAccess: func(g core.LinkGrantContext) error {
			member, ok := g.Principals["gcp"]
			if defaultStr(spec.Visibility, VisibilityPublic) == VisibilityPublic || !ok || !isOutputSet(member) {
				return nil
			}
			slug := core.BoundedName(g.ConsumerName, 24)
			_, err := cloudrunv2.NewServiceIamMember(g.Ctx, name+"-"+slug+"-invoker", &cloudrunv2.ServiceIamMemberArgs{
				Project:  res.Service.Project,
				Location: res.Service.Location,
				Name:     res.Service.Name,
				Role:     pulumi.String("roles/run.invoker"),
				Member:   member,
			}, pulumi.Parent(res))
			return err
		},
	}
}

func registerCloudRunOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *CloudRunService) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName":       pulumi.String(meta.CanonicalName),
		"url":                 res.URL,
		"customUrl":           res.CustomURL,
		"urls":                res.URLs,
		"serviceAccountEmail": res.ServiceAccountEmail,
		"serviceAccountId":    pulumi.String(res.ServiceAccountID),
	})
}

func toStringArrayOrNil(items []string) pulumi.StringArray {
	if len(items) == 0 {
		return nil
	}
	return pulumi.ToStringArray(items)
}
