package core

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

const (
	// TierPlatform identifies organization-wide foundations provisioned once per org.
	TierPlatform = "platform"
	// TierProject identifies per-application project assets provisioned once per repo (infra/shared).
	TierProject = "project"
	// TierEnvironment identifies per-environment deployments (dev, stage, prod, preview-<slug>).
	TierEnvironment = "environment"
)

// PlatformInfo captures validated metadata for an organization-level PlatformResource.
type PlatformInfo struct {
	Name          string
	CanonicalName string
	Org           string
	Kind          string
	Tags          map[string]string
}

// PlatformSpec holds organization-level lifecycle inputs for registering a PlatformResource.
type PlatformSpec struct {
	Name string
	Org  string
	Kind string
}

// PlatformResource marks organization-wide component handles provisioned once per org.
type PlatformResource interface {
	Resource
	OpenBasePlatform() PlatformInfo
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

// ProjectInfo captures validated metadata for a project-level ProjectResource.
type ProjectInfo struct {
	Name          string
	CanonicalName string
	Project       string
	Repo          string
	Kind          string
	Tags          map[string]string
}

// ProjectSpec holds project-level lifecycle inputs for registering a ProjectResource.
type ProjectSpec struct {
	Name    string
	Project string
	Repo    string
	Kind    string
}

// ProjectResource marks per-application project handles provisioned once per repo in infra/shared.
type ProjectResource interface {
	Resource
	OpenBaseProject() ProjectInfo
}

// ProjectMeta stores ProjectInfo and Links on structs that directly embed pulumi.ResourceState.
type ProjectMeta struct {
	info ProjectInfo
	link LinkDescriptor
}

// OpenBaseProject returns the validated project metadata for the project component.
func (b *ProjectMeta) OpenBaseProject() ProjectInfo { return b.info }

// Link returns the LinkDescriptor exported by the project component.
func (b *ProjectMeta) Link() LinkDescriptor { return b.link }

// SetLink sets the LinkDescriptor exported by the project component.
func (b *ProjectMeta) SetLink(desc LinkDescriptor) { b.link = desc }

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
		CanonicalName: BoundedName(fmt.Sprintf("%s-platform-%s", org, spec.Name), 63),
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

// InitProject validates spec and registers a project-level ProjectResource component.
func InitProject(
	ctx *pulumi.Context,
	typeToken, defaultKind string,
	self pulumi.ComponentResource,
	holder *ProjectMeta,
	spec ProjectSpec,
	opts ...pulumi.ResourceOption,
) (ProjectInfo, error) {
	if err := ValidateIdentifier("project", spec.Project); err != nil {
		return ProjectInfo{}, err
	}
	if err := ValidateIdentifier("name", spec.Name); err != nil {
		return ProjectInfo{}, err
	}
	kind := spec.Kind
	if kind == "" {
		kind = defaultKind
	}
	tags := map[string]string{
		TagTier:    TierProject,
		TagProject: spec.Project,
	}
	if spec.Repo != "" {
		tags[TagRepo] = spec.Repo
	}
	info := ProjectInfo{
		Name:          spec.Name,
		CanonicalName: BoundedName(fmt.Sprintf("%s-project-%s", spec.Project, spec.Name), 63),
		Project:       spec.Project,
		Repo:          spec.Repo,
		Kind:          kind,
		Tags:          tags,
	}
	holder.info = info
	if err := ctx.RegisterComponentResource(typeToken, spec.Name, self, opts...); err != nil {
		return ProjectInfo{}, err
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

// GCPProjectLabels builds GCP-valid labels for a project-level ProjectResource.
func GCPProjectLabels(info ProjectInfo, extra map[string]string) pulumi.StringMap {
	labels := pulumi.StringMap{
		"openbase-tier":    pulumi.String(TierProject),
		"openbase-project": pulumi.String(sanitizeGCPLabelValue(info.Project)),
	}
	if info.Repo != "" {
		labels["openbase-repo"] = pulumi.String(sanitizeGCPLabelValue(info.Repo))
	}
	appendExtraGCPLabels(labels, extra)
	return labels
}
