package gcp

import (
	"reflect"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/storage"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var (
	_ core.StatefulResource              = (*BucketResource)(nil)
	_ core.ScopedSpecOf[*BucketResource] = Bucket{}
)

func init() {
	core.RegisterRemoteGrantHandler("gcp-bucket", grantRemoteBucketAccess)
}

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
	GCPProject         pulumi.StringPtrInput
	BucketNamespace    string
	Location           string
	StorageClass       string
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
	res.SetLink(buildBucketLink(b.Name, meta, res))
	if err := registerBucketOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// RegisterInScope adapts gcp.Bucket to openbase.Project and openbase.Environment.
func (b Bucket) RegisterInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (core.Resource, error) {
	return b.RegisterTypedInScope(ctx, scope, opts...)
}

// RegisterTypedInScope registers gcp.Bucket with inherited Scope defaults.
func (b Bucket) RegisterTypedInScope(ctx *pulumi.Context, scope core.Scope, opts ...pulumi.ResourceOption) (*BucketResource, error) {
	applyBucketScope(ctx, &b, scope)
	return b.Register(ctx, opts...)
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

func buildBucketLink(name string, meta core.ResourceMeta, res *BucketResource) core.LinkDescriptor {
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"bucketName":         res.BucketName,
			"bucketUrl":          res.BucketURL,
			"fallbackBucketName": res.FallbackBucketName,
		},
		StackExports: map[string]pulumi.Input{
			"bucketName":         res.BucketName,
			"fallbackBucketName": res.FallbackBucketName,
		},
		GrantAccess: func(g core.LinkGrantContext) error {
			return grantBucketAccess(name, meta, res, g)
		},
	}
}

func applyBucketScope(ctx *pulumi.Context, b *Bucket, scope core.Scope) {
	b.Project = defaultStr(b.Project, scope.Project)
	b.Repo = defaultStr(b.Repo, scope.Repo)
	b.Environment = defaultStr(b.Environment, string(scope.Env))
	b.BranchFrom = defaultStr(b.BranchFrom, scope.DefaultStorageBranchFrom())
	if b.BranchStackRef == "" && b.BranchFrom != "" {
		b.BranchStackRef = scope.BranchStackRef(ctx, b.BranchFrom)
	}
}

func grantBucketAccess(name string, meta core.ResourceMeta, res *BucketResource, g core.LinkGrantContext) error {
	member, ok := g.Principals["gcp"]
	if !ok || !isOutputSet(member) {
		return nil
	}
	consumerSlug := core.BoundedName(g.ConsumerName, 24)
	return bindBucketMember(g.Ctx, name+"-"+consumerSlug+"-gcp", meta, res, member)
}

func grantRemoteBucketAccess(g core.LinkGrantContext, remote *core.RemoteResource) error {
	member, ok := g.Principals["gcp"]
	if !ok || !isOutputSet(member) || g.Ctx == nil {
		return nil
	}
	bktName := remote.Attr("bucketName")
	if !isOutputSet(bktName) {
		return nil
	}
	slug := core.BoundedName(remote.Name()+"-"+g.ConsumerName, 36)
	_, err := storage.NewBucketIAMMember(g.Ctx, slug+"-remote-admin", &storage.BucketIAMMemberArgs{
		Bucket: bktName,
		Role:   pulumi.String("roles/storage.objectAdmin"),
		Member: member,
	})
	return err
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

func isOutputSet(out pulumi.StringOutput) bool {
	return !reflect.ValueOf(out).IsZero()
}
