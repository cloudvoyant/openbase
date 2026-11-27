package vercel

import (
	"os"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Private helpers ------------------------------------------------------------

// resolveVercelAPIToken centralizes Vercel API token lookup for direct REST helpers
// in the same precedence order as the Pulumi Vercel provider: explicit arg,
// Pulumi config `vercel:apiToken`, then `VERCEL_API_TOKEN` / `VERCEL_TOKEN`.
func resolveVercelAPIToken(ctx *pulumi.Context, explicit pulumi.StringInput) pulumi.StringOutput {
	if explicit != nil {
		return pulumi.ToSecret(explicit.ToStringOutput()).(pulumi.StringOutput)
	}
	cfg := config.New(ctx, "vercel")
	if cfg.Get("apiToken") != "" {
		return cfg.RequireSecret("apiToken")
	}
	if tok := strings.TrimSpace(os.Getenv("VERCEL_API_TOKEN")); tok != "" {
		return pulumi.ToSecret(pulumi.String(tok)).(pulumi.StringOutput)
	}
	if tok := strings.TrimSpace(os.Getenv("VERCEL_TOKEN")); tok != "" {
		return pulumi.ToSecret(pulumi.String(tok)).(pulumi.StringOutput)
	}
	return pulumi.String("").ToStringOutput()
}
