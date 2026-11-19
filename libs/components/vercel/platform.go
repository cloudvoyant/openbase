// Package vercel provides OpenBase Pulumi components for Vercel across Platform, Project, WebApp, and BlobStore tiers.
package vercel

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	vercelprov "github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatelessResource = (*PlatformResource)(nil)

// Platform declares organization/team-level Vercel settings and team-shared environment variables provisioned once per organization.
type Platform struct {
	Name             string
	Project          string
	Repo             string
	Environment      string
	TeamID           string
	TeamSlug         string
	BaseDomain       string
	SharedEnv        map[string]pulumi.StringInput
	SharedSecrets    map[string]pulumi.StringInput
	SharedTargets    []string
	SharedProjectIDs []string
}

// PlatformResource holds team-level Vercel configuration and team-shared environment variables exported for vercel.Project stacks.
type PlatformResource struct {
	pulumi.ResourceState
	core.StatelessMeta

	SharedEnvVars []*vercelprov.SharedEnvironmentVariable
	TeamID        pulumi.StringOutput
	TeamSlug      pulumi.StringOutput
	BaseDomain    pulumi.StringOutput
}

// Register provisions team-level Vercel settings and optional SharedEnvironmentVariables.
func (p Platform) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*PlatformResource, error) {
	p.Name = defaultVercelStr(p.Name, "vercel-platform")
	p.Project = defaultVercelStr(p.Project, "platform")
	env := defaultVercelStr(p.Environment, string(core.EnvShared))
	res := &PlatformResource{
		TeamID:     pulumi.String(p.TeamID).ToStringOutput(),
		TeamSlug:   pulumi.String(p.TeamSlug).ToStringOutput(),
		BaseDomain: pulumi.String(core.NormalizeDomain(p.BaseDomain)).ToStringOutput(),
	}
	meta, err := core.InitStateless(ctx, "openbase:vercel:Platform", "vercel-platform", res, &res.StatelessMeta, core.StatelessSpec{
		Name:        p.Name,
		Project:     p.Project,
		Repo:        p.Repo,
		Environment: env,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := registerTeamSharedEnvVars(ctx, p.Name, p, res); err != nil {
		return nil, err
	}
	res.SetLink(core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Env: map[string]pulumi.StringOutput{
			"VERCEL_TEAM_ID":     res.TeamID,
			"VERCEL_TEAM_SLUG":   res.TeamSlug,
			"VERCEL_BASE_DOMAIN": res.BaseDomain,
		},
	})
	return res, ctx.RegisterResourceOutputs(res, pulumi.Map{
		"teamId":           res.TeamID,
		"teamSlug":         res.TeamSlug,
		"baseDomain":       res.BaseDomain,
		"vercelBaseDomain": res.BaseDomain,
	})
}

// DeriveBlobBaseURL computes https://<slug>.public.blob.vercel-storage.com from a Vercel store_... ID.
func DeriveBlobBaseURL(storeID pulumi.StringInput) pulumi.StringOutput {
	return storeID.ToStringOutput().ApplyT(func(id string) string {
		slug := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "store_"))
		if slug == "" {
			return ""
		}
		return fmt.Sprintf("https://%s.public.blob.vercel-storage.com", slug)
	}).(pulumi.StringOutput)
}

// Private helpers ------------------------------------------------------------

func registerTeamSharedEnvVars(ctx *pulumi.Context, name string, spec Platform, res *PlatformResource) error {
	targets := spec.SharedTargets
	if len(targets) == 0 {
		targets = []string{"production", "preview"}
	}
	if err := createTeamEnvSlice(ctx, name, spec.SharedEnv, false, targets, spec.SharedProjectIDs, spec.TeamID, res); err != nil {
		return err
	}
	return createTeamEnvSlice(ctx, name, spec.SharedSecrets, true, targets, spec.SharedProjectIDs, spec.TeamID, res)
}

func createTeamEnvSlice(
	ctx *pulumi.Context,
	name string,
	entries map[string]pulumi.StringInput,
	sensitive bool,
	targets, projectIDs []string,
	teamID string,
	res *PlatformResource,
) error {
	for k, v := range entries {
		slug := core.BoundedName(strings.ToLower(k), 28)
		sev, err := vercelprov.NewSharedEnvironmentVariable(ctx, fmt.Sprintf("%s-senv-%s", name, slug), &vercelprov.SharedEnvironmentVariableArgs{
			TeamId:                       optionalVercelStr(teamID),
			ProjectIds:                   pulumi.ToStringArray(projectIDs),
			Key:                          pulumi.String(k),
			Value:                        v.ToStringOutput(),
			Sensitive:                    pulumi.Bool(sensitive),
			Targets:                      pulumi.ToStringArray(targets),
			ApplyToAllCustomEnvironments: pulumi.Bool(true),
		}, pulumi.Parent(res))
		if err != nil {
			return err
		}
		res.SharedEnvVars = append(res.SharedEnvVars, sev)
	}
	return nil
}

func defaultVercelStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func optionalVercelStr(val string) pulumi.StringPtrInput {
	if val == "" {
		return nil
	}
	return pulumi.String(val)
}
