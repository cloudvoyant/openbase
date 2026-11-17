package core

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

// LinkGrantContext supplies consumer identity details to linked resources that grant IAM or OIDC access.
type LinkGrantContext struct {
	Ctx                         *pulumi.Context
	ConsumerName                string
	Environment                 Environment
	GCPProject                  pulumi.StringPtrInput
	GCPServiceAccountEmail      pulumi.StringOutput
	GCPWorkloadIdentityPoolName pulumi.StringOutput
	VercelProjectID             pulumi.StringOutput
	VercelTeamSlug              pulumi.StringOutput
}

// LinkDescriptor describes the environment variables, secrets, and access grants exported by a Resource.
type LinkDescriptor struct {
	Name        string
	Kind        string
	Env         map[string]pulumi.StringOutput
	Secrets     map[string]pulumi.StringOutput
	GrantAccess func(LinkGrantContext) error
}

// ResolvedLinks holds merged plain and secret environment variables across all Links on a consumer.
type ResolvedLinks struct {
	Env     map[string]pulumi.StringOutput
	Secrets map[string]pulumi.StringOutput
}

// ResolveLinks merges environment variables and secrets from links and executes their GrantAccess hooks.
func ResolveLinks(grantCtx LinkGrantContext, links []Resource) (ResolvedLinks, error) {
	resolved := ResolvedLinks{
		Env:     map[string]pulumi.StringOutput{},
		Secrets: map[string]pulumi.StringOutput{},
	}
	seen := map[string]string{}
	for _, res := range links {
		if res == nil {
			continue
		}
		desc := res.Link()
		if err := mergeLinkOutputs(desc.Name, desc.Env, resolved.Env, seen, false); err != nil {
			return ResolvedLinks{}, err
		}
		if err := mergeLinkOutputs(desc.Name, desc.Secrets, resolved.Secrets, seen, true); err != nil {
			return ResolvedLinks{}, err
		}
		if desc.GrantAccess != nil && grantCtx.Ctx != nil {
			if err := desc.GrantAccess(grantCtx); err != nil {
				return ResolvedLinks{}, err
			}
		}
	}
	return resolved, nil
}

// Private helpers ------------------------------------------------------------

func mergeLinkOutputs(
	sourceName string,
	incoming map[string]pulumi.StringOutput,
	target map[string]pulumi.StringOutput,
	seen map[string]string,
	asSecret bool,
) error {
	for key, val := range incoming {
		if prev, exists := seen[key]; exists {
			return fmt.Errorf("%w: %q exported by both %q and %q", ErrDuplicateLinkEnvKey, key, prev, sourceName)
		}
		seen[key] = sourceName
		if asSecret {
			target[key] = pulumi.ToSecret(val).(pulumi.StringOutput)
		} else {
			target[key] = val
		}
	}
	return nil
}
