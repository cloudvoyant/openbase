package core

import "github.com/pulumi/pulumi/sdk/v3/go/pulumi"

// Public API -----------------------------------------------------------------

// Subject identifies a workload caller across cloud providers without coupling core to provider SDKs.
type Subject struct {
	Provider string
	NativeID pulumi.StringOutput
	Claims   map[string]pulumi.StringOutput
}

// PrincipalGrant holds a federated cloud IAM member resolved for an external workload Subject.
type PrincipalGrant struct {
	Provider string
	Member   pulumi.StringOutput
}

// IdentityBroker translates an external OIDC Subject into a target-cloud IAM member once per consumer.
type IdentityBroker interface {
	BrokerProvider() string
	ResolvePrincipal(ctx *pulumi.Context, consumer ResourceMeta, sub Subject) (PrincipalGrant, error)
}

// LinkGrantContext supplies consumer identity details and brokered cloud principals to linked resources.
type LinkGrantContext struct {
	Ctx          *pulumi.Context
	ConsumerName string
	ConsumerMeta ResourceMeta
	Environment  Environment
	Subject      Subject
	Principals   map[string]pulumi.StringOutput
	Brokers      []IdentityBroker
}

// LinkDescriptor describes the attributes, stack exports, and IAM/OIDC access grants of a Resource.
type LinkDescriptor struct {
	Name         string
	Kind         string
	Attributes   map[string]pulumi.StringOutput
	StackExports map[string]pulumi.Input
	GrantAccess  func(LinkGrantContext) error
}

// ResolveLinks resolves brokered cloud principals for grantCtx and executes GrantAccess on every linked Resource.
func ResolveLinks(grantCtx LinkGrantContext, links []Resource) (map[string]pulumi.StringOutput, error) {
	if err := populatePrincipals(&grantCtx); err != nil {
		return nil, err
	}
	for _, res := range links {
		if res == nil {
			continue
		}
		desc := res.Link()
		if desc.GrantAccess != nil && grantCtx.Ctx != nil {
			if err := desc.GrantAccess(grantCtx); err != nil {
				return nil, err
			}
		}
	}
	return grantCtx.Principals, nil
}

// Private helpers ------------------------------------------------------------

func populatePrincipals(grantCtx *LinkGrantContext) error {
	if grantCtx.Principals == nil {
		grantCtx.Principals = map[string]pulumi.StringOutput{}
	}
	if grantCtx.Subject.Provider != "" {
		if _, exists := grantCtx.Principals[grantCtx.Subject.Provider]; !exists {
			grantCtx.Principals[grantCtx.Subject.Provider] = grantCtx.Subject.NativeID
		}
	}
	if grantCtx.Ctx == nil || grantCtx.Subject.Provider == "" {
		return nil
	}
	for _, broker := range grantCtx.Brokers {
		if err := runSingleBroker(grantCtx, broker); err != nil {
			return err
		}
	}
	return nil
}

func runSingleBroker(grantCtx *LinkGrantContext, broker IdentityBroker) error {
	if broker == nil {
		return nil
	}
	target := broker.BrokerProvider()
	if _, exists := grantCtx.Principals[target]; exists {
		return nil
	}
	grant, err := broker.ResolvePrincipal(grantCtx.Ctx, grantCtx.ConsumerMeta, grantCtx.Subject)
	if err != nil {
		return err
	}
	grantCtx.Principals[target] = grant.Member
	return nil
}
