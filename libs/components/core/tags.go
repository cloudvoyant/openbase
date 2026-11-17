package core

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

const (
	// TagEnvironment records the OpenBase environment ("shared", "dev", "stage", "prod", "preview-*").
	TagEnvironment = "openbase.io/environment"
	// TagProject records the logical OpenBase project name.
	TagProject = "openbase.io/project"
	// TagRepo records the source repository slug when provided.
	TagRepo = "openbase.io/repo"
	// TagStateful marks whether a resource holds persistent state ("true" | "false").
	TagStateful = "openbase.io/stateful"
	// TagEphemeral marks whether a resource belongs to a disposable preview stack ("true" | "false").
	TagEphemeral = "openbase.io/ephemeral"
	// TagPreviewID records the preview slug on preview-<slug> stacks.
	TagPreviewID = "openbase.io/preview-id"
)

// StandardTags returns the canonical openbase.io/* metadata map for a resource.
func StandardTags(meta ResourceMeta) map[string]string {
	tags := map[string]string{
		TagEnvironment: meta.Environment.String(),
		TagProject:     meta.Project,
		TagStateful:    strconv.FormatBool(meta.Stateful),
		TagEphemeral:   strconv.FormatBool(meta.Ephemeral),
	}
	if meta.Repo != "" {
		tags[TagRepo] = meta.Repo
	}
	if previewID := meta.Environment.PreviewID(); previewID != "" {
		tags[TagPreviewID] = previewID
	}
	return tags
}

// GCPLabels converts StandardTags and optional extra pairs into GCP-valid label keys and values.
func GCPLabels(meta ResourceMeta, extra map[string]string) pulumi.StringMap {
	labels := pulumi.StringMap{
		"openbase-environment": pulumi.String(sanitizeGCPLabelValue(meta.Environment.String())),
		"openbase-project":     pulumi.String(sanitizeGCPLabelValue(meta.Project)),
		"openbase-stateful":    pulumi.String(strconv.FormatBool(meta.Stateful)),
		"openbase-ephemeral":   pulumi.String(strconv.FormatBool(meta.Ephemeral)),
	}
	if meta.Repo != "" {
		labels["openbase-repo"] = pulumi.String(sanitizeGCPLabelValue(meta.Repo))
	}
	if previewID := meta.Environment.PreviewID(); previewID != "" {
		labels["openbase-preview-id"] = pulumi.String(sanitizeGCPLabelValue(previewID))
	}
	for k, v := range extra {
		if key := sanitizeGCPLabelKey(k); key != "" {
			labels[key] = pulumi.String(sanitizeGCPLabelValue(v))
		}
	}
	return labels
}

// Private helpers ------------------------------------------------------------

var nonGCPLabelChars = regexp.MustCompile(`[^a-z0-9_-]+`)

func sanitizeGCPLabelKey(raw string) string {
	s := strings.ToLower(raw)
	s = nonGCPLabelChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-_")
	if s == "" {
		return ""
	}
	if s[0] < 'a' || s[0] > 'z' {
		s = "l-" + s
	}
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func sanitizeGCPLabelValue(raw string) string {
	s := strings.ToLower(raw)
	s = nonGCPLabelChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}
