package core

import (
	"fmt"
	"strings"
)

// Public API -----------------------------------------------------------------

// NormalizeDomain strips any http(s) scheme, trailing slashes, and whitespace from a domain.
func NormalizeDomain(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	return strings.TrimRight(s, "/")
}

// EnvDomainLabel returns dev, stage, prod, or previewId (guarding reserved tier names on preview branches).
func EnvDomainLabel(env Environment) string {
	if !env.IsPreview() {
		return string(env)
	}
	pid := env.PreviewID()
	switch Environment(pid) {
	case EnvShared, EnvDev, EnvStage, EnvProd:
		return string(env)
	default:
		return pid
	}
}

// HostLabel builds "{project}-{dev|stage|prod|previewId}" (omitting env when shared, and inserting "{service}" when service != "web").
func HostLabel(project string, env Environment, serviceName string) (string, error) {
	if err := ValidateIdentifier("project", project); err != nil {
		return "", err
	}
	if _, err := ParseEnvironment(string(env)); err != nil {
		return "", err
	}
	hasService := serviceName != "" && serviceName != "web"
	if hasService {
		if err := ValidateIdentifier("name", serviceName); err != nil {
			return "", err
		}
	}
	if env == EnvShared {
		if hasService {
			return BoundedName(fmt.Sprintf("%s-%s", project, serviceName), 49), nil
		}
		return BoundedName(project, 49), nil
	}
	label := EnvDomainLabel(env)
	if hasService {
		return BoundedName(fmt.Sprintf("%s-%s-%s", project, serviceName, label), 49), nil
	}
	return BoundedName(fmt.Sprintf("%s-%s", project, label), 49), nil
}

// EnvironmentDomain returns "{project}-{dev|stage|prod|previewId}.{baseDomain}" for a non-empty baseDomain.
func EnvironmentDomain(project string, env Environment, serviceName, baseDomain string) (string, error) {
	base := NormalizeDomain(baseDomain)
	if base == "" {
		return "", nil
	}
	sub, err := HostLabel(project, env, serviceName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s", sub, base), nil
}

// DedupeDomains normalizes and deduplicates domain strings while preserving order.
func DedupeDomains(groups ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range groups {
		for _, raw := range g {
			d := NormalizeDomain(raw)
			if d != "" && !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}
