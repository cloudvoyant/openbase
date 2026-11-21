package gcp

import (
	"reflect"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/storage"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatefulResource = (*Bucket)(nil)

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

// BucketArgs configures a Google Cloud Storage Bucket component.
type BucketArgs struct {
	ProjectName        string
	Repo               string
	Environment        string
	Kind               string
	BranchFrom         string
	Backup             *bool
	Links              []core.Resource
	Account            AccountInput
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

// Bucket is the provisioned GCS bucket handle with tier-aware backups, protection, and BranchFrom overlays.
type Bucket struct {
	pulumi.ResourceState
	core.StatefulMeta

	Bucket             *storage.Bucket
	BucketName         pulumi.StringOutput
	BucketURL          pulumi.StringOutput
	FallbackBucketName pulumi.StringOutput
}

// NewBucket provisions a Google Cloud Storage Bucket satisfying core.StatefulResource.
func NewBucket(ctx *pulumi.Context, name string, args *BucketArgs, opts ...pulumi.ResourceOption) (*Bucket, error) {
	if args == nil {
		args = &BucketArgs{}
	}
	spec := *args
	if env, err := core.ResolveEnvironment(ctx, spec.Environment); err == nil {
		spec.BranchFrom = core.DefaultBranchFrom(env, spec.BranchFrom, false)
	}
	if spec.Account != nil && spec.GCPProject == nil {
		acc := spec.Account.gcpAccountValues()
		if isOutputSet(acc.gcpProject) {
			spec.GCPProject = nonEmptyStrPtrOutput(acc.gcpProject)
		}
	}
	res := &Bucket{}
	meta, err := core.InitStateful(ctx, "openbase:gcp:Bucket", "gcp-bucket", res, &res.StatefulMeta, core.StatefulSpec{
		Name:        name,
		Project:     spec.ProjectName,
		Repo:        spec.Repo,
		Environment: spec.Environment,
		Kind:        spec.Kind,
		BranchFrom:  spec.BranchFrom,
		Backup:      spec.Backup,
		Links:       spec.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	spec.BranchStackRef = core.DefaultBranchStackRef(ctx, meta.Project, meta.BranchFrom.String(), spec.BranchStackRef)
	bucketName, err := core.BucketName(meta.Project, meta.Environment, name, spec.BucketNamespace)
	if err != nil {
		return nil, err
	}
	bkt, err := createGCSBucket(ctx, name, bucketName, meta, spec, res)
	if err != nil {
		return nil, err
	}
	fallback, err := resolveFallbackBucket(ctx, name, meta, spec)
	if err != nil {
		return nil, err
	}
	res.Bucket = bkt
	res.BucketName = bkt.Name
	res.BucketURL = bkt.Url
	res.FallbackBucketName = fallback
	res.PublishLink(ctx, buildBucketLink(name, meta, res))
	return res, registerBucketOutputs(ctx, meta, res)
}

// Private helpers ------------------------------------------------------------

const sevenDaysSeconds = 604800

func createGCSBucket(
	ctx *pulumi.Context,
	name, bucketName string,
	meta core.ResourceMeta,
	spec BucketArgs,
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

func resolveFallbackBucket(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec BucketArgs) (pulumi.StringOutput, error) {
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

func buildBucketLink(name string, meta core.ResourceMeta, res *Bucket) core.LinkDescriptor {
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

func grantBucketAccess(name string, meta core.ResourceMeta, res *Bucket, g core.LinkGrantContext) error {
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
	res *Bucket,
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

func registerBucketOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *Bucket) error {
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
