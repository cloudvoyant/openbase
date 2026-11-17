// Package synthetic provides deterministic offline StatelessResource and StatefulResource doubles for tests.
package synthetic

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

// ServiceArgs configures an offline synthetic StatelessResource.
type ServiceArgs struct {
	core.StatelessResourceArgs
	Endpoint string
}

// Service is a deterministic offline StatelessResource double.
type Service struct {
	pulumi.ResourceState
	core.StatelessMeta
	URL          pulumi.StringOutput
	ResolvedEnv  map[string]pulumi.StringOutput
	ResolvedSecs map[string]pulumi.StringOutput
	Granted      []string
}

var _ core.StatelessResource = (*Service)(nil)

// NewService constructs a synthetic StatelessResource without cloud provider dependencies.
func NewService(ctx *pulumi.Context, name string, args ServiceArgs, opts ...pulumi.ResourceOption) (*Service, error) {
	svc := &Service{}
	meta, err := core.InitStateless(ctx, "openbase:synthetic:Service", name, "synthetic-service", svc, &svc.StatelessMeta, args.StatelessResourceArgs, opts...)
	if err != nil {
		return nil, err
	}
	resolved, err := core.ResolveLinks(core.LinkGrantContext{
		Ctx:                    ctx,
		ConsumerName:           meta.CanonicalName,
		Environment:            meta.Environment,
		GCPServiceAccountEmail: pulumi.String(meta.CanonicalName + "@synthetic.local").ToStringOutput(),
	}, args.Links)
	if err != nil {
		return nil, err
	}
	for k, v := range args.Env {
		resolved.Env[k] = v.ToStringOutput()
	}
	for k, v := range args.Secrets {
		resolved.Secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	endpoint := args.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.synthetic.local", meta.CanonicalName)
	}
	svc.URL = pulumi.String(endpoint).ToStringOutput()
	svc.ResolvedEnv = resolved.Env
	svc.ResolvedSecs = resolved.Secrets
	envKey := strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_URL"
	svc.SetLink(core.LinkDescriptor{
		Name: name,
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
