// Package synthetic provides deterministic offline StatelessResource and StatefulResource components for tests.
package synthetic

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*Service)(nil)

// ServiceArgs configures an offline synthetic StatelessResource component.
type ServiceArgs struct {
	ProjectName string
	Repo        string
	Environment string
	Kind        string
	Links       []core.Resource
	Env         map[string]pulumi.StringInput
	Secrets     map[string]pulumi.StringInput
	Endpoint    string
}

// Service is the deterministic offline StatelessResource handle returned by NewService.
type Service struct {
	pulumi.ResourceState
	core.StatelessMeta
	URL          pulumi.StringOutput
	ResolvedEnv  map[string]pulumi.StringOutput
	ResolvedSecs map[string]pulumi.StringOutput
	Granted      []string
}

// NewService registers a synthetic StatelessResource without cloud provider dependencies.
func NewService(ctx *pulumi.Context, name string, args *ServiceArgs, opts ...pulumi.ResourceOption) (*Service, error) {
	if args == nil {
		args = &ServiceArgs{}
	}
	svc := &Service{}
	meta, err := core.InitStateless(ctx, "openbase:synthetic:Service", "synthetic-service", svc, &svc.StatelessMeta, core.StatelessSpec{
		Name:        name,
		Project:     args.ProjectName,
		Repo:        args.Repo,
		Environment: args.Environment,
		Kind:        args.Kind,
		Links:       args.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if _, err := resolveSyntheticServiceLinks(ctx, meta, args.Links); err != nil {
		return nil, err
	}
	populateSyntheticEnvMaps(args, svc)
	endpoint := args.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.synthetic.local", meta.CanonicalName)
	}
	svc.URL = pulumi.String(endpoint).ToStringOutput()
	svc.PublishLink(ctx, core.LinkDescriptor{
		Name:       name,
		Kind:       meta.Kind,
		Attributes: map[string]pulumi.StringOutput{"url": svc.URL},
		GrantAccess: func(g core.LinkGrantContext) error {
			svc.Granted = append(svc.Granted, g.ConsumerName)
			return nil
		},
	})
	return svc, ctx.RegisterResourceOutputs(svc, pulumi.Map{"url": svc.URL})
}

// Private helpers ------------------------------------------------------------

func resolveSyntheticServiceLinks(ctx *pulumi.Context, meta core.ResourceMeta, links []core.Resource) (map[string]pulumi.StringOutput, error) {
	return core.ResolveLinks(core.LinkGrantContext{
		Ctx:          ctx,
		ConsumerName: meta.CanonicalName,
		ConsumerMeta: meta,
		Environment:  meta.Environment,
		Subject: core.Subject{
			Provider: "synthetic",
			NativeID: pulumi.String(meta.CanonicalName + "@synthetic.local").ToStringOutput(),
		},
	}, links)
}

func populateSyntheticEnvMaps(args *ServiceArgs, svc *Service) {
	svc.ResolvedEnv = map[string]pulumi.StringOutput{}
	for k, v := range args.Env {
		svc.ResolvedEnv[k] = v.ToStringOutput()
	}
	svc.ResolvedSecs = map[string]pulumi.StringOutput{}
	for k, v := range args.Secrets {
		svc.ResolvedSecs[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
}
