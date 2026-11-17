package gcp

import (
	"reflect"
	"strings"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/storage"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatefulResource = (*BucketResource)(nil)

// BucketCORSRule configures direct browser uploads or reads on a GCS bucket.
type BucketCORSRule struct {
	Origins         []string
	Methods         []string
	ResponseHeaders []string
	MaxAgeSeconds   int
}

// Bucket declares a Google Cloud Storage Bucket component spec.
type Bucket struct {
	Name               string
	Project            string
	Repo               string
	Environment        string
	Kind               string
	BranchFrom         string
	Backup             *bool
	Links              []core.Resource
	Env                map[string]pulumi.StringInput
	Secrets            map[string]pulumi.StringInput
	GCPProject         pulumi.StringPtrInput
	BucketNamespace    string
	Location           string
	StorageClass       string
	EnvPrefix          string
	FallbackBucketName pulumi.StringInput
	BranchStackRef     string
	CORS               []BucketCORSRule
	ExtraLabels        map[string]string
	ForceDestroy       *bool
}

// BucketResource is the provisioned GCS bucket handle with tier-aware backups, protection, and BranchFrom overlays.
type BucketResource struct {
	pulumi.ResourceState
	core.StatefulMeta

	Bucket             *storage.Bucket
	BucketName         pulumi.StringOutput
	BucketURL          pulumi.StringOutput
	FallbackBucketName pulumi.StringOutput
}

// Register provisions a Google Cloud Storage Bucket satisfying core.StatefulResource.
func (b Bucket) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*BucketResource, error) {
	res := &BucketResource{}
	meta, err := core.InitStateful(ctx, "openbase:gcp:Bucket", "gcp-bucket", res, &res.StatefulMeta, core.StatefulSpec{
		Name:        b.Name,
		Project:     b.Project,
		Repo:        b.Repo,
		Environment: b.Environment,
		Kind:        b.Kind,
		BranchFrom:  b.BranchFrom,
		Backup:      b.Backup,
		Links:       b.Links,
		Env:         b.Env,
		Secrets:     b.Secrets,
	}, opts...)
	if err != nil {
		return nil, err
	}
	bucketName, err := core.BucketName(meta.Project, meta.Environment, b.Name, b.BucketNamespace)
	if err != nil {
		return nil, err
	}
	bkt, err := createGCSBucket(ctx, b.Name, bucketName, meta, b, res)
	if err != nil {
		return nil, err
	}
	fallback, err := resolveFallbackBucket(ctx, b.Name, meta, b)
	if err != nil {
		return nil, err
	}
	res.Bucket = bkt
	res.BucketName = bkt.Name
	res.BucketURL = bkt.Url
	res.FallbackBucketName = fallback
	res.SetLink(buildBucketLink(b.Name, meta, b, res))
	if err := registerBucketOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

const sevenDaysSeconds = 604800

func createGCSBucket(
	ctx *pulumi.Context,
	name, bucketName string,
	meta core.ResourceMeta,
	spec Bucket,
	parent pulumi.Resource,
) (*storage.Bucket, error) {
	forceDestroy := !meta.Protected
	if spec.ForceDestroy != nil {
		forceDestroy = *spec.ForceDestroy
	}
	retention := 0
	if meta.Backup {
		retention = sevenDaysSeconds
	}
	bktArgs := &storage.BucketArgs{
		Project:                  spec.GCPProject,
		Name:                     pulumi.String(bucketName),
		Location:                 pulumi.String(defaultStr(spec.Location, "US")),
		StorageClass:             pulumi.String(defaultStr(spec.StorageClass, "STANDARD")),
		UniformBucketLevelAccess: pulumi.Bool(true),
		PublicAccessPrevention:   pulumi.String("enforced"),
		ForceDestroy:             pulumi.Bool(forceDestroy),
		Versioning:               &storage.BucketVersioningArgs{Enabled: pulumi.Bool(meta.Backup)},
		SoftDeletePolicy:         &storage.BucketSoftDeletePolicyArgs{RetentionDurationSeconds: pulumi.Int(retention)},
		Cors:                     buildBucketCORS(spec.CORS),
		Labels:                   core.GCPLabels(meta, spec.ExtraLabels),
	}
	return storage.NewBucket(ctx, name+"-bkt", bktArgs, core.ChildOptions(parent, meta)...)
}

func buildBucketCORS(rules []BucketCORSRule) storage.BucketCorArray {
	if len(rules) == 0 {
		return nil
	}
	out := make(storage.BucketCorArray, 0, len(rules))
	for _, r := range rules {
		maxAge := r.MaxAgeSeconds
		if maxAge == 0 {
			maxAge = 3600
		}
		out = append(out, &storage.BucketCorArgs{
			Origins:         pulumi.ToStringArray(r.Origins),
			Methods:         pulumi.ToStringArray(r.Methods),
			ResponseHeaders: pulumi.ToStringArray(r.ResponseHeaders),
			MaxAgeSeconds:   pulumi.Int(maxAge),
		})
	}
	return out
}

func resolveFallbackBucket(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec Bucket) (pulumi.StringOutput, error) {
	if meta.BranchFrom == "" {
		return pulumi.String("").ToStringOutput(), nil
	}
	if spec.FallbackBucketName != nil {
		return spec.FallbackBucketName.ToStringOutput(), nil
	}
	if spec.BranchStackRef != "" {
		ref, err := pulumi.NewStackReference(ctx, name+"-branch-ref", &pulumi.StackReferenceArgs{
			Name: pulumi.String(spec.BranchStackRef),
		})
		if err != nil {
			return pulumi.StringOutput{}, err
		}
		return ref.GetStringOutput(pulumi.String("bucketName")), nil
	}
	derived, err := core.BucketName(meta.Project, meta.BranchFrom, name, spec.BucketNamespace)
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	return pulumi.String(derived).ToStringOutput(), nil
}

func buildBucketLink(name string, meta core.ResourceMeta, spec Bucket, res *BucketResource) core.LinkDescriptor {
	prefix := envKeyPrefix(defaultStr(spec.EnvPrefix, "STORAGE"))
	envMap := map[string]pulumi.StringOutput{
		prefix + "_BUCKET": res.BucketName,
	}
	if meta.BranchFrom != "" {
		envMap[prefix+"_FALLBACK_BUCKET"] = res.FallbackBucketName
	}
	for k, v := range spec.Env {
		envMap[k] = v.ToStringOutput()
	}
	secrets := map[string]pulumi.StringOutput{}
	for k, v := range spec.Secrets {
		secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return core.LinkDescriptor{
		Name:    meta.Name,
		Kind:    meta.Kind,
		Env:     envMap,
		Secrets: secrets,
		GrantAccess: func(g core.LinkGrantContext) error {
			return grantBucketAccess(name, meta, res, g)
		},
	}
}

func grantBucketAccess(name string, meta core.ResourceMeta, res *BucketResource, g core.LinkGrantContext) error {
	consumerSlug := core.BoundedName(g.ConsumerName, 24)
	if isOutputSet(g.GCPServiceAccountEmail) {
		member := pulumi.Sprintf("serviceAccount:%s", g.GCPServiceAccountEmail)
		if err := bindBucketMember(g.Ctx, name+"-"+consumerSlug+"-sa", meta, res, member); err != nil {
			return err
		}
	}
	if isOutputSet(g.GCPWorkloadIdentityPoolName) && isOutputSet(g.VercelProjectID) {
		member := pulumi.Sprintf("principalSet://iam.googleapis.com/%s/attribute.project_id/%s", g.GCPWorkloadIdentityPoolName, g.VercelProjectID)
		if err := bindBucketMember(g.Ctx, name+"-"+consumerSlug+"-wif", meta, res, member); err != nil {
			return err
		}
	}
	return nil
}

func bindBucketMember(
	ctx *pulumi.Context,
	prefix string,
	meta core.ResourceMeta,
	res *BucketResource,
	member pulumi.StringInput,
) error {
	_, err := storage.NewBucketIAMMember(ctx, prefix+"-admin", &storage.BucketIAMMemberArgs{
		Bucket: res.BucketName,
		Role:   pulumi.String("roles/storage.objectAdmin"),
		Member: member,
	}, pulumi.Parent(res))
	if err != nil {
		return err
	}
	if meta.BranchFrom == "" {
		return nil
	}
	_, err = storage.NewBucketIAMMember(ctx, prefix+"-fallback-viewer", &storage.BucketIAMMemberArgs{
		Bucket: res.FallbackBucketName,
		Role:   pulumi.String("roles/storage.objectViewer"),
		Member: member,
	}, pulumi.Parent(res))
	return err
}

func registerBucketOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *BucketResource) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName":      pulumi.String(meta.CanonicalName),
		"bucketName":         res.BucketName,
		"bucketUrl":          res.BucketURL,
		"fallbackBucketName": res.FallbackBucketName,
		"branchFrom":         pulumi.String(meta.BranchFrom.String()),
	})
}

func envKeyPrefix(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.NewReplacer("-", "_", ".", "_", "/", "_").Replace(s)
	return strings.Trim(s, "_")
}

func isOutputSet(out pulumi.StringOutput) bool {
	return !reflect.ValueOf(out).IsZero()
}
