// Package core defines OpenBase's PlatformResource and Resource (StatelessResource / StatefulResource) contracts, naming, tags, and Links.
package core

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Public API -----------------------------------------------------------------

// Environment identifies a canonical OpenBase resource stack (shared, dev, stage, prod, or preview-<slug>).
type Environment string

const (
	// EnvShared hosts cross-environment project assets in infra/shared.
	EnvShared Environment = "shared"
	// EnvDev is the mutable integration environment.
	EnvDev Environment = "dev"
	// EnvStage mirrors production topology before promotion.
	EnvStage Environment = "stage"
	// EnvProd is the protected customer-facing environment.
	EnvProd Environment = "prod"
	// PreviewPrefix marks ephemeral branch environments ("preview-<slug>").
	PreviewPrefix = "preview-"
)

var (
	// ErrInvalidEnvironment reports an unsupported or malformed environment string.
	ErrInvalidEnvironment = errors.New("core: invalid environment (expected shared, dev, stage, prod, or preview-<slug>)")
	// ErrInvalidBranchSource reports an unsupported or self-referential BranchFrom target.
	ErrInvalidBranchSource = errors.New("core: invalid branchFrom source (expected dev, stage, or prod distinct from target)")
	// ErrInvalidIdentifier reports a malformed project or resource identifier.
	ErrInvalidIdentifier = errors.New("core: invalid identifier (expected lowercase [a-z][a-z0-9-]*)")
)

// ParseEnvironment validates and normalizes an OpenBase environment string.
func ParseEnvironment(raw string) (Environment, error) {
	switch Environment(raw) {
	case EnvShared, EnvDev, EnvStage, EnvProd:
		return Environment(raw), nil
	}
	if !strings.HasPrefix(raw, PreviewPrefix) {
		return "", fmt.Errorf("%w: %q", ErrInvalidEnvironment, raw)
	}
	slug := strings.TrimPrefix(raw, PreviewPrefix)
	if !slugPattern.MatchString(slug) {
		return "", fmt.Errorf("%w: %q", ErrInvalidEnvironment, raw)
	}
	return Environment(raw), nil
}

// ResolveEnvironment resolves an explicit environment or falls back to Pulumi stack metadata.
func ResolveEnvironment(ctx *pulumi.Context, explicit string) (Environment, error) {
	if explicit != "" {
		return ParseEnvironment(explicit)
	}
	if candidate := readStackEnvironment(ctx); candidate != "" {
		return ParseEnvironment(candidate)
	}
	return ParseEnvironment(ctx.Stack())
}

// ValidateBranchSource ensures branchFrom names a persistent tier distinct from current.
func ValidateBranchSource(current Environment, branchFrom string) (Environment, error) {
	if branchFrom == "" {
		return "", nil
	}
	src, err := ParseEnvironment(branchFrom)
	if err != nil || !src.IsPersistent() || src == current {
		return "", fmt.Errorf("%w: %q (for environment %q)", ErrInvalidBranchSource, branchFrom, current)
	}
	return src, nil
}

// String returns the raw environment identifier.
func (e Environment) String() string { return string(e) }

// IsPreview reports whether the environment is an ephemeral preview-<slug> stack.
func (e Environment) IsPreview() bool { return strings.HasPrefix(string(e), PreviewPrefix) }

// PreviewID returns <slug> for preview-<slug> stacks and "" otherwise.
func (e Environment) PreviewID() string {
	if !e.IsPreview() {
		return ""
	}
	return strings.TrimPrefix(string(e), PreviewPrefix)
}

// IsProtected reports whether stateful resources require deletion protection and backups.
func (e Environment) IsProtected() bool { return e == EnvProd || e == EnvStage }

// IsEphemeral reports whether the environment is torn down automatically on branch close.
func (e Environment) IsEphemeral() bool { return e.IsPreview() }

// IsPersistent reports whether the environment can act as a BranchFrom parent.
func (e Environment) IsPersistent() bool { return e == EnvDev || e == EnvStage || e == EnvProd }

// Private helpers ------------------------------------------------------------

var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func readStackEnvironment(ctx *pulumi.Context) string {
	cfg := config.New(ctx, "premise")
	if stack := cfg.Get("stack"); stack != "" {
		return stack
	}
	env := cfg.Get("environment")
	name := cfg.Get("environmentName")
	if env == "preview" && name != "" {
		return PreviewPrefix + name
	}
	return env
}
