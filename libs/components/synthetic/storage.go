package synthetic

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatefulResource = (*StorageResource)(nil)

// Storage declares an offline synthetic StatefulResource spec supporting BranchFrom overlays.
type Storage struct {
	Name        string
	Project     string
	Repo        string
	Environment string
	Kind        string
	BranchFrom  string
	Backup      *bool
	Links       []core.Resource
	Env         map[string]pulumi.StringInput
	Secrets     map[string]pulumi.StringInput
	SecretToken string
}

// StorageResource is the deterministic offline StatefulResource handle returned by Storage.Register.
type StorageResource struct {
	pulumi.ResourceState
	core.StatefulMeta
	BucketName         pulumi.StringOutput
	FallbackBucketName pulumi.StringOutput
	GrantedConsumers   []string
}

// Register validates and provisions a synthetic StatefulResource without cloud provider dependencies.
func (s Storage) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*StorageResource, error) {
	st := &StorageResource{}
	meta, err := core.InitStateful(ctx, "openbase:synthetic:Storage", "synthetic-storage", st, &st.StatefulMeta, core.StatefulSpec{
		Name:        s.Name,
		Project:     s.Project,
		Repo:        s.Repo,
		Environment: s.Environment,
		Kind:        s.Kind,
		BranchFrom:  s.BranchFrom,
		Backup:      s.Backup,
		Links:       s.Links,
		Env:         s.Env,
		Secrets:     s.Secrets,
	}, opts...)
	if err != nil {
		return nil, err
	}
	bucket := meta.CanonicalName
	fallback := ""
	if meta.Environment.IsPreview() && meta.BranchFrom != "" {
		fallback, err = core.CanonicalName(meta.Project, meta.BranchFrom, s.Name)
		if err != nil {
			return nil, err
		}
	}
	st.BucketName = pulumi.String(bucket).ToStringOutput()
	st.FallbackBucketName = pulumi.String(fallback).ToStringOutput()
	prefix := strings.ToUpper(strings.ReplaceAll(s.Name, "-", "_"))
	envMap := map[string]pulumi.StringOutput{
		prefix + "_BUCKET": st.BucketName,
	}
	if fallback != "" {
		envMap[prefix+"_FALLBACK_BUCKET"] = st.FallbackBucketName
	}
	secMap := map[string]pulumi.StringOutput{}
	if s.SecretToken != "" {
		secMap[prefix+"_TOKEN"] = pulumi.ToSecret(pulumi.String(s.SecretToken)).(pulumi.StringOutput)
	}
	st.SetLink(core.LinkDescriptor{
		Name:    s.Name,
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
