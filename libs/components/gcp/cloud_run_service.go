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
	core.StatelessResourceArgs
	GCPProject               pulumi.StringPtrInput
	GCPProjectNumber         pulumi.StringInput
	SharedStackRef           string
	ArtifactRegistryRepo     pulumi.StringInput
	ArtifactRegistryLocation string
	WorkloadIdentityPoolName pulumi.StringInput
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
	Domain                   string
	StartupProbe             *ProbeConfig
	LivenessProbe            *ProbeConfig
	ExtraLabels              map[string]string
}

// CloudRunService provisions a Cloud Run v2 Service with dedicated SA, Secret Manager mounts, and IAM Links.
type CloudRunService struct {
	pulumi.ResourceState
	core.StatelessMeta

	ServiceAccount      *serviceaccount.Account
	Service             *cloudrunv2.Service
	DomainMapping       *cloudrun.DomainMapping
	URL                 pulumi.StringOutput
	ServiceAccountEmail pulumi.StringOutput
	ServiceAccountID    string
}

// NewCloudRunService registers a Cloud Run v2 Service satisfying core.StatelessResource.
func NewCloudRunService(
	ctx *pulumi.Context,
	name string,
	args CloudRunServiceArgs,
	opts ...pulumi.ResourceOption,
) (*CloudRunService, error) {
	if err := validateCloudRunArgs(args); err != nil {
		return nil, err
	}
	res := &CloudRunService{}
	meta, err := core.InitStateless(ctx, "openbase:gcp:CloudRunService", name, "gcp-cloud-run", res, &res.StatelessMeta, args.StatelessResourceArgs, opts...)
	if err != nil {
		return nil, err
	}
	if err := buildCloudRunResources(ctx, name, meta, args, res); err != nil {
		return nil, err
	}
	res.SetLink(buildCloudRunLink(name, meta, args, res))
	if err := registerCloudRunOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

type sharedRefs struct {
	repoID   pulumi.StringOutput
	poolName pulumi.StringOutput
}

func validateCloudRunArgs(args CloudRunServiceArgs) error {
	if args.Image == nil {
		return ErrImageRequired
	}
	switch defaultStr(args.Visibility, VisibilityPublic) {
	case VisibilityPublic, VisibilityInternal, VisibilityWorker:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidVisibility, args.Visibility)
	}
}

func buildCloudRunResources(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args CloudRunServiceArgs,
	res *CloudRunService,
) error {
	sa, saID, selfTokenGrant, err := createServiceAccount(ctx, name, meta, args, res)
	if err != nil {
		return err
	}
	res.ServiceAccount = sa
	res.ServiceAccountID = saID
	res.ServiceAccountEmail = sa.Email

	refs, err := resolveSharedRefs(ctx, name, args)
	if err != nil {
		return err
	}
	if err := grantArtifactRegistryAccess(ctx, name, args, refs, sa, res); err != nil {
		return err
	}
	envs, deps, err := resolveEnvsAndSecrets(ctx, name, meta, args, refs, sa, res)
	if err != nil {
		return err
	}
	deps = append(deps, selfTokenGrant)
	svc, err := createCloudRunV2(ctx, name, meta, args, sa, envs, deps, res)
	if err != nil {
		return err
	}
	res.Service = svc
	res.URL = svc.Uri
	if err := configureInvokerIAM(ctx, name, args, svc, res); err != nil {
		return err
	}
	return createOptionalDomainMapping(ctx, name, meta, args, svc, res)
}

func createServiceAccount(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args CloudRunServiceArgs,
	parent pulumi.Resource,
) (*serviceaccount.Account, string, *serviceaccount.IAMMember, error) {
	saID, err := core.ServiceAccountID(meta.Project, meta.Environment, name)
	if err != nil {
		return nil, "", nil, err
	}
	sa, err := serviceaccount.NewAccount(ctx, name+"-sa", &serviceaccount.AccountArgs{
		Project:     args.GCPProject,
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

func resolveSharedRefs(ctx *pulumi.Context, name string, args CloudRunServiceArgs) (sharedRefs, error) {
	var refs sharedRefs
	if args.ArtifactRegistryRepo != nil {
		refs.repoID = args.ArtifactRegistryRepo.ToStringOutput()
	}
	if args.WorkloadIdentityPoolName != nil {
		refs.poolName = args.WorkloadIdentityPoolName.ToStringOutput()
	}
	if args.SharedStackRef == "" {
		return refs, nil
	}
	sr, err := pulumi.NewStackReference(ctx, name+"-shared-ref", &pulumi.StackReferenceArgs{
		Name: pulumi.String(args.SharedStackRef),
	})
	if err != nil {
		return sharedRefs{}, err
	}
	if !isOutputSet(refs.repoID) {
		refs.repoID = sr.GetStringOutput(pulumi.String("repositoryId"))
	}
	if !isOutputSet(refs.poolName) {
		refs.poolName = sr.GetStringOutput(pulumi.String("workloadIdentityPoolName"))
	}
	return refs, nil
}

func grantArtifactRegistryAccess(
	ctx *pulumi.Context,
	name string,
	args CloudRunServiceArgs,
	refs sharedRefs,
	sa *serviceaccount.Account,
	parent pulumi.Resource,
) error {
	if !isOutputSet(refs.repoID) {
		return nil
	}
	arLoc := defaultStr(args.ArtifactRegistryLocation, defaultStr(args.Location, "us-central1"))
	_, err := artifactregistry.NewRepositoryIamMember(ctx, name+"-ar-sa", &artifactregistry.RepositoryIamMemberArgs{
		Project:    args.GCPProject,
		Location:   pulumi.String(arLoc),
		Repository: refs.repoID,
		Role:       pulumi.String("roles/artifactregistry.reader"),
		Member:     pulumi.Sprintf("serviceAccount:%s", sa.Email),
	}, pulumi.Parent(parent))
	if err != nil || args.GCPProjectNumber == nil {
		return err
	}
	_, err = artifactregistry.NewRepositoryIamMember(ctx, name+"-ar-robot", &artifactregistry.RepositoryIamMemberArgs{
		Project:    args.GCPProject,
		Location:   pulumi.String(arLoc),
		Repository: refs.repoID,
		Role:       pulumi.String("roles/artifactregistry.reader"),
		Member:     pulumi.Sprintf("serviceAccount:service-%s@serverless-robot-prod.iam.gserviceaccount.com", args.GCPProjectNumber),
	}, pulumi.Parent(parent))
	return err
}

func resolveEnvsAndSecrets(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args CloudRunServiceArgs,
	refs sharedRefs,
	sa *serviceaccount.Account,
	parent pulumi.Resource,
) (cloudrunv2.ServiceTemplateContainerEnvArray, []pulumi.Resource, error) {
	resolved, err := core.ResolveLinks(core.LinkGrantContext{
		Ctx:                         ctx,
		ConsumerName:                meta.CanonicalName,
		Environment:                 meta.Environment,
		GCPProject:                  args.GCPProject,
		GCPServiceAccountEmail:      sa.Email,
		GCPWorkloadIdentityPoolName: refs.poolName,
	}, args.Links)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range args.Env {
		resolved.Env[k] = v.ToStringOutput()
	}
	for k, v := range args.Secrets {
		resolved.Secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	envs := sortedPlainEnvs(resolved.Env)
	secretEnvs, deps, err := createSecretMounts(ctx, name, meta, args, resolved.Secrets, sa, parent)
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
	args CloudRunServiceArgs,
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
		envItem, itemDeps, err := createSingleSecretMount(ctx, name, key, secrets[key], meta, args, sa, parent)
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
	args CloudRunServiceArgs,
	sa *serviceaccount.Account,
	parent pulumi.Resource,
) (*cloudrunv2.ServiceTemplateContainerEnvArgs, []pulumi.Resource, error) {
	slug := core.BoundedName(strings.ToLower(envKey), 24)
	secretID := core.BoundedName(meta.CanonicalName+"-"+slug, 63)
	sec, err := secretmanager.NewSecret(ctx, name+"-sec-"+slug, &secretmanager.SecretArgs{
		Project:            args.GCPProject,
		SecretId:           pulumi.String(secretID),
		DeletionProtection: pulumi.Bool(false),
		Labels:             core.GCPLabels(meta, args.ExtraLabels),
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
		Project:  args.GCPProject,
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
	args CloudRunServiceArgs,
	sa *serviceaccount.Account,
	envs cloudrunv2.ServiceTemplateContainerEnvArray,
	deps []pulumi.Resource,
	parent pulumi.Resource,
) (*cloudrunv2.Service, error) {
	location := defaultStr(args.Location, "us-central1")
	port := args.Port
	if port == 0 {
		port = 8080
	}
	minInst, maxInst := resolveScaling(meta, args)
	container := &cloudrunv2.ServiceTemplateContainerArgs{
		Image:         args.Image,
		Commands:      toStringArrayOrNil(args.Command),
		Args:          toStringArrayOrNil(args.ContainerArgs),
		Envs:          envs,
		Ports:         &cloudrunv2.ServiceTemplateContainerPortsArgs{ContainerPort: pulumi.Int(port)},
		StartupProbe:  buildStartupProbe(args.StartupProbe, port),
		LivenessProbe: buildLivenessProbe(args.LivenessProbe, port),
	}
	serviceName := core.BoundedName(meta.CanonicalName, 49)
	return cloudrunv2.NewService(ctx, name+"-svc", &cloudrunv2.ServiceArgs{
		Project:            args.GCPProject,
		Name:               pulumi.String(serviceName),
		Location:           pulumi.String(location),
		DeletionProtection: pulumi.Bool(false),
		Ingress:            pulumi.String(resolveIngress(args)),
		Labels:             core.GCPLabels(meta, args.ExtraLabels),
		Template: &cloudrunv2.ServiceTemplateArgs{
			ServiceAccount: sa.Email,
			Labels:         core.GCPLabels(meta, args.ExtraLabels),
			Scaling: &cloudrunv2.ServiceTemplateScalingArgs{
				MinInstanceCount: pulumi.Int(minInst),
				MaxInstanceCount: pulumi.Int(maxInst),
			},
			Containers: cloudrunv2.ServiceTemplateContainerArray{container},
		},
	}, pulumi.Parent(parent), pulumi.DependsOn(deps))
}

func resolveScaling(meta core.ResourceMeta, args CloudRunServiceArgs) (int, int) {
	minInst := 0
	if args.MinInstances != nil {
		minInst = *args.MinInstances
	}
	maxInst := 10
	if meta.Ephemeral {
		maxInst = 3
	}
	if args.MaxInstances != nil {
		maxInst = *args.MaxInstances
	}
	return minInst, maxInst
}

func resolveIngress(args CloudRunServiceArgs) string {
	if args.Ingress != "" {
		return args.Ingress
	}
	if defaultStr(args.Visibility, VisibilityPublic) == VisibilityWorker {
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
	args CloudRunServiceArgs,
	svc *cloudrunv2.Service,
	parent pulumi.Resource,
) error {
	vis := defaultStr(args.Visibility, VisibilityPublic)
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
	for idx, member := range args.AllowedMembers {
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

func createOptionalDomainMapping(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args CloudRunServiceArgs,
	svc *cloudrunv2.Service,
	res *CloudRunService,
) error {
	if args.Domain == "" || !meta.Environment.IsPersistent() {
		return nil
	}
	dm, err := cloudrun.NewDomainMapping(ctx, name+"-domain", &cloudrun.DomainMappingArgs{
		Project:  svc.Project,
		Location: svc.Location,
		Name:     pulumi.String(args.Domain),
		Metadata: &cloudrun.DomainMappingMetadataArgs{
			Namespace: svc.Project,
			Labels:    core.GCPLabels(meta, args.ExtraLabels),
		},
		Spec: &cloudrun.DomainMappingSpecArgs{
			RouteName: svc.Name,
		},
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	res.DomainMapping = dm
	return nil
}

func buildCloudRunLink(name string, meta core.ResourceMeta, args CloudRunServiceArgs, res *CloudRunService) core.LinkDescriptor {
	urlKey := envKeyPrefix(meta.Name) + "_URL"
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			urlKey: res.URL,
		},
		GrantAccess: func(g core.LinkGrantContext) error {
			if defaultStr(args.Visibility, VisibilityPublic) == VisibilityPublic || !isOutputSet(g.GCPServiceAccountEmail) {
				return nil
			}
			slug := core.BoundedName(g.ConsumerName, 24)
			_, err := cloudrunv2.NewServiceIamMember(g.Ctx, name+"-"+slug+"-invoker", &cloudrunv2.ServiceIamMemberArgs{
				Project:  res.Service.Project,
				Location: res.Service.Location,
				Name:     res.Service.Name,
				Role:     pulumi.String("roles/run.invoker"),
				Member:   pulumi.Sprintf("serviceAccount:%s", g.GCPServiceAccountEmail),
			}, pulumi.Parent(res))
			return err
		},
	}
}

func registerCloudRunOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *CloudRunService) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName":       pulumi.String(meta.CanonicalName),
		"url":                 res.URL,
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
