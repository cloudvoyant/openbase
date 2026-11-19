package core

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

const (
	// TierPlatform marks organization-wide foundations (<provider>.Platform and composite.Platform).
	TierPlatform = "platform"
	// TierResource marks project-shared and per-environment resources (StatelessResource and StatefulResource).
	TierResource = "resource"
)

// PlatformInfo captures validated metadata for an organization-level PlatformResource.
type PlatformInfo struct {
	Name          string
	CanonicalName string
	Org           string
	Kind          string
	Tags          map[string]string
}

// PlatformSpec holds organization-level inputs for registering a PlatformResource.
type PlatformSpec struct {
	Name string
	Org  string
	Kind string
}

// PlatformResource marks organization-wide foundation handles provisioned once per org.
type PlatformResource interface {
	pulumi.Resource
	OpenBasePlatform() PlatformInfo
	Link() LinkDescriptor
}

// PlatformMeta stores PlatformInfo and Links on structs that directly embed pulumi.ResourceState.
type PlatformMeta struct {
	info PlatformInfo
	link LinkDescriptor
}

// OpenBasePlatform returns the validated organization metadata for the platform component.
func (b *PlatformMeta) OpenBasePlatform() PlatformInfo { return b.info }

// Link returns the LinkDescriptor exported by the platform component.
func (b *PlatformMeta) Link() LinkDescriptor { return b.link }

// SetLink sets the LinkDescriptor exported by the platform component.
func (b *PlatformMeta) SetLink(desc LinkDescriptor) { b.link = desc }

// InitPlatform validates spec and registers an organization-level PlatformResource component.
func InitPlatform(
	ctx *pulumi.Context,
	typeToken, defaultKind string,
	self pulumi.ComponentResource,
	holder *PlatformMeta,
	spec PlatformSpec,
	opts ...pulumi.ResourceOption,
) (PlatformInfo, error) {
	org := OrgScope(spec.Org, ctx.Organization())
	if err := ValidateIdentifier("name", spec.Name); err != nil {
		return PlatformInfo{}, err
	}
	kind := spec.Kind
	if kind == "" {
		kind = defaultKind
	}
	info := PlatformInfo{
		Name:          spec.Name,
		CanonicalName: BoundedName(fmt.Sprintf("%s-%s", org, spec.Name), 63),
		Org:           org,
		Kind:          kind,
		Tags: map[string]string{
			TagTier: TierPlatform,
			TagOrg:  org,
		},
	}
	holder.info = info
	if err := ctx.RegisterComponentResource(typeToken, spec.Name, self, opts...); err != nil {
		return PlatformInfo{}, err
	}
	return info, nil
}

// GCPPlatformLabels builds GCP-valid labels for an organization-level PlatformResource.
func GCPPlatformLabels(info PlatformInfo, extra map[string]string) pulumi.StringMap {
	labels := pulumi.StringMap{
		"openbase-tier": pulumi.String(TierPlatform),
		"openbase-org":  pulumi.String(sanitizeGCPLabelValue(info.Org)),
	}
	appendExtraGCPLabels(labels, extra)
	return labels
}
