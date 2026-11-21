package synthetic

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatefulResource = (*Storage)(nil)

// StorageArgs configures an offline synthetic StatefulResource component supporting BranchFrom overlays.
type StorageArgs struct {
	Project     string
	Repo        string
	Environment string
	Kind        string
	BranchFrom  string
	Backup      *bool
	Links       []core.Resource
}

// Storage is the deterministic offline StatefulResource handle returned by NewStorage.
type Storage struct {
	pulumi.ResourceState
	core.StatefulMeta
	BucketName         pulumi.StringOutput
	FallbackBucketName pulumi.StringOutput
	GrantedConsumers   []string
}

// NewStorage registers a synthetic StatefulResource without cloud provider dependencies.
func NewStorage(ctx *pulumi.Context, name string, args *StorageArgs, opts ...pulumi.ResourceOption) (*Storage, error) {
	if args == nil {
		args = &StorageArgs{}
	}
	st := &Storage{}
	meta, err := core.InitStateful(ctx, "openbase:synthetic:Storage", "synthetic-storage", st, &st.StatefulMeta, core.StatefulSpec{
		Name:        name,
		Project:     args.Project,
		Repo:        args.Repo,
		Environment: args.Environment,
		Kind:        args.Kind,
		BranchFrom:  args.BranchFrom,
		Backup:      args.Backup,
		Links:       args.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	fallback, err := computeSyntheticFallback(meta, name)
	if err != nil {
		return nil, err
	}
	st.BucketName = pulumi.String(meta.CanonicalName).ToStringOutput()
	st.FallbackBucketName = pulumi.String(fallback).ToStringOutput()
	st.PublishLink(ctx, core.LinkDescriptor{
		Name: name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"bucketName":         st.BucketName,
			"fallbackBucketName": st.FallbackBucketName,
		},
		GrantAccess: func(g core.LinkGrantContext) error {
			st.GrantedConsumers = append(st.GrantedConsumers, fmt.Sprintf("%s:%s", g.Environment, g.ConsumerName))
			return nil
		},
	})
	return st, ctx.RegisterResourceOutputs(st, pulumi.Map{
		"bucket":         st.BucketName,
		"fallbackBucket": st.FallbackBucketName,
	})
}

// Private helpers ------------------------------------------------------------

func computeSyntheticFallback(meta core.ResourceMeta, name string) (string, error) {
	if meta.Environment.IsPreview() && meta.BranchFrom != "" {
		return core.CanonicalName(meta.Project, meta.BranchFrom, name)
	}
	return "", nil
}
