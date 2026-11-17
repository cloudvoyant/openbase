package synthetic

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

// StorageArgs configures an offline synthetic StatefulResource.
type StorageArgs struct {
	core.StatefulResourceArgs
	SecretToken string
}

// Storage is a deterministic offline StatefulResource double supporting BranchFrom overlays.
type Storage struct {
	pulumi.ResourceState
	core.StatefulMeta
	BucketName         pulumi.StringOutput
	FallbackBucketName pulumi.StringOutput
	GrantedConsumers   []string
}

var _ core.StatefulResource = (*Storage)(nil)

// NewStorage constructs a synthetic StatefulResource without cloud provider dependencies.
func NewStorage(ctx *pulumi.Context, name string, args StorageArgs, opts ...pulumi.ResourceOption) (*Storage, error) {
	st := &Storage{}
	meta, err := core.InitStateful(ctx, "openbase:synthetic:Storage", name, "synthetic-storage", st, &st.StatefulMeta, args.StatefulResourceArgs, opts...)
	if err != nil {
		return nil, err
	}
	bucket := meta.CanonicalName
	fallback := ""
	if meta.Environment.IsPreview() && meta.BranchFrom != "" {
		fallback, err = core.CanonicalName(meta.Project, meta.BranchFrom, name)
		if err != nil {
			return nil, err
		}
	}
	st.BucketName = pulumi.String(bucket).ToStringOutput()
	st.FallbackBucketName = pulumi.String(fallback).ToStringOutput()
	prefix := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	envMap := map[string]pulumi.StringOutput{
		prefix + "_BUCKET": st.BucketName,
	}
	if fallback != "" {
		envMap[prefix+"_FALLBACK_BUCKET"] = st.FallbackBucketName
	}
	secMap := map[string]pulumi.StringOutput{}
	if args.SecretToken != "" {
		secMap[prefix+"_TOKEN"] = pulumi.ToSecret(pulumi.String(args.SecretToken)).(pulumi.StringOutput)
	}
	st.SetLink(core.LinkDescriptor{
		Name:    name,
		Kind:    meta.Kind,
		Env:     envMap,
		Secrets: secMap,
		GrantAccess: func(g core.LinkGrantContext) error {
			st.GrantedConsumers = append(st.GrantedConsumers, fmt.Sprintf("%s:%s", g.Environment, g.ConsumerName))
			return nil
		},
	})
	if err := ctx.RegisterResourceOutputs(st, pulumi.Map{
		"bucket":         st.BucketName,
		"fallbackBucket": st.FallbackBucketName,
	}); err != nil {
		return nil, err
	}
	return st, nil
}
