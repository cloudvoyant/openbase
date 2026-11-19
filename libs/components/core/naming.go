package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Public API -----------------------------------------------------------------

// ValidateIdentifier checks that value starts with a lowercase letter and uses only [a-z0-9-].
func ValidateIdentifier(label, value string) error {
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%w: %s=%q", ErrInvalidIdentifier, label, value)
	}
	return nil
}

// CanonicalName returns deterministic "<project>-<env>-<name>" bounded to 63 characters.
func CanonicalName(project string, env Environment, name string) (string, error) {
	if err := ValidateIdentifier("project", project); err != nil {
		return "", err
	}
	if _, err := ParseEnvironment(string(env)); err != nil {
		return "", err
	}
	if err := ValidateIdentifier("name", name); err != nil {
		return "", err
	}
	return BoundedName(fmt.Sprintf("%s-%s-%s", project, env, name), 63), nil
}

// BoundedName returns raw if len(raw) <= maxLen; otherwise truncates and appends an 8-char SHA-256 suffix.
func BoundedName(raw string, maxLen int) string {
	clean := sanitizeSlug(raw)
	if len(clean) <= maxLen {
		return clean
	}
	sum := sha256.Sum256([]byte(clean))
	hash := hex.EncodeToString(sum[:])[:8]
	if maxLen <= 9 {
		return clean[:maxLen]
	}
	prefixLen := maxLen - len(hash) - 1
	prefix := strings.TrimRight(clean[:prefixLen], "-")
	if prefix == "" {
		prefix = "ob"
	}
	return prefix + "-" + hash
}

// ServiceAccountID builds a GCP-compliant service account ID (6-30 chars, ^[a-z]([-a-z0-9]*[a-z0-9])$).
func ServiceAccountID(project string, env Environment, name string) (string, error) {
	canonical, err := CanonicalName(project, env, name)
	if err != nil {
		return "", err
	}
	id := BoundedName(canonical, 30)
	for len(id) < 6 {
		id += "-sa"
	}
	if len(id) > 30 {
		id = BoundedName(id, 30)
	}
	return id, nil
}

// BucketName builds a globally scoped GCS bucket name (3-63 chars, lowercase [a-z0-9-]).
func BucketName(project string, env Environment, name, namespace string) (string, error) {
	canonical, err := CanonicalName(project, env, name)
	if err != nil {
		return "", err
	}
	raw := canonical
	if namespace != "" {
		raw = sanitizeSlug(namespace) + "-" + canonical
	}
	return BoundedName(raw, 63), nil
}

// OrgScope returns a sanitized lowercase organization identifier for org-level Platform specs, defaulting to "platform".
func OrgScope(candidates ...string) string {
	for _, c := range candidates {
		trimmed := strings.TrimSpace(c)
		if trimmed != "" && trimmed != "organization" {
			return BoundedName(trimmed, 32)
		}
	}
	return "platform"
}

// Private helpers ------------------------------------------------------------

var (
	identifierPattern = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)
	nonSlugChars      = regexp.MustCompile(`[^a-z0-9-]+`)
	multiHyphens      = regexp.MustCompile(`-+`)
)

func sanitizeSlug(raw string) string {
	s := strings.ToLower(raw)
	s = nonSlugChars.ReplaceAllString(s, "-")
	s = multiHyphens.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "ob-" + s
		s = strings.Trim(s, "-")
	}
	return s
}
