// Package synthetic provides deterministic offline StatelessResource and StatefulResource specs for tests.
package synthetic

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*ServiceResource)(nil)

// Service declares an offline synthetic StatelessResource spec.
type Service struct {
	Name        string
	Project     string
	Repo        string
	Environment string
	Kind        string
	Links       []core.Resource
	Env         map[string]pulumi.StringInput
	Secrets     map[string]pulumi.StringInput
	Endpoint    string
}

// ServiceResource is the deterministic offline StatelessResource handle returned by Service.Register.
type ServiceResource struct {
	pulumi.ResourceState
	core.StatelessMeta
	URL          pulumi.StringOutput
	ResolvedEnv  map[string]pulumi.StringOutput
	ResolvedSecs map[string]pulumi.StringOutput
	Granted      []string
}

// Register validates and provisions a synthetic StatelessResource without cloud provider dependencies.
func (s Service) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*ServiceResource, error) {
	svc := &ServiceResource{}
	meta, err := core.InitStateless(ctx, "openbase:synthetic:Service", "synthetic-service", svc, &svc.StatelessMeta, core.StatelessSpec{
		Name:        s.Name,
		Project:     s.Project,
		Repo:        s.Repo,
		Environment: s.Environment,
		Kind:        s.Kind,
		Links:       s.Links,
		Env:         s.Env,
		Secrets:     s.Secrets,
	}, opts...)
	if err != nil {
		return nil, err
	}
	resolved, err := core.ResolveLinks(core.LinkGrantContext{
		Ctx:                    ctx,
		ConsumerName:           meta.CanonicalName,
		Environment:            meta.Environment,
		GCPServiceAccountEmail: pulumi.String(meta.CanonicalName + "@synthetic.local").ToStringOutput(),
	}, s.Links)
	if err != nil {
		return nil, err
	}
	for k, v := range s.Env {
		resolved.Env[k] = v.ToStringOutput()
	}
	for k, v := range s.Secrets {
		resolved.Secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.synthetic.local", meta.CanonicalName)
	}
	svc.URL = pulumi.String(endpoint).ToStringOutput()
	svc.ResolvedEnv = resolved.Env
	svc.ResolvedSecs = resolved.Secrets
	envKey := strings.ToUpper(strings.ReplaceAll(s.Name, "-", "_")) + "_URL"
	svc.SetLink(core.LinkDescriptor{
		Name: s.Name,
		Kind: meta.Kind,
		Env:  map[string]pulumi.StringOutput{envKey: svc.URL},
		GrantAccess: func(g core.LinkGrantContext) error {
			svc.Granted = append(svc.Granted, g.ConsumerName)
			return nil
		},
	})
	if err := ctx.RegisterResourceOutputs(svc, pulumi.Map{"url": svc.URL}); err != nil {
		return nil, err
	}
	return svc, nil
}
