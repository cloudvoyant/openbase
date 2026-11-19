package neon

import (
	"errors"
	"net/url"
	"reflect"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/internal/providers/neon"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatefulResource = (*DatabaseResource)(nil)

// ErrProjectIDRequired reports a missing NeonProject, ProjectID, and ProjectStackRef on Database.
var ErrProjectIDRequired = errors.New("neon: neonProject, projectId, or projectStackRef is required")

// Database declares a per-environment Neon branch, compute endpoint, role, and database spec.
type Database struct {
	Name            string
	Project         string
	Repo            string
	Environment     string
	Kind            string
	BranchFrom      string
	Backup          *bool
	Links           []core.Resource
	Env             map[string]pulumi.StringInput
	Secrets         map[string]pulumi.StringInput
	NeonProject     *ProjectResource
	ProjectID       pulumi.StringInput
	DefaultBranchID pulumi.StringInput
	ParentBranchID  pulumi.StringInput
	ProjectStackRef string
	BranchStackRef  string
	DatabaseName    string
	RoleName        string
	EnvPrefix       string
	SuspendSeconds  *int
}

// DatabaseResource is the provisioned Neon PostgreSQL branch handle with pooled DATABASE_URL and direct DIRECT_URL secrets.
type DatabaseResource struct {
	pulumi.ResourceState
	core.StatefulMeta

	Branch      *neonprov.Branch
	Endpoint    *neonprov.Endpoint
	Role        *neonprov.Role
	Database    *neonprov.Database
	BranchID    pulumi.StringOutput
	Host        pulumi.StringOutput
	PoolerHost  pulumi.StringOutput
	DatabaseURL pulumi.StringOutput
	DirectURL   pulumi.StringOutput
}

// Register provisions a Neon PostgreSQL branch satisfying core.StatefulResource.
func (d Database) Register(ctx *pulumi.Context, opts ...pulumi.ResourceOption) (*DatabaseResource, error) {
	if d.NeonProject == nil && d.ProjectID == nil && d.ProjectStackRef == "" {
		return nil, ErrProjectIDRequired
	}
	res := &DatabaseResource{}
	meta, err := core.InitStateful(ctx, "openbase:neon:Database", "neon-database", res, &res.StatefulMeta, core.StatefulSpec{
		Name:        d.Name,
		Project:     d.Project,
		Repo:        d.Repo,
		Environment: d.Environment,
		Kind:        d.Kind,
		BranchFrom:  d.BranchFrom,
		Backup:      d.Backup,
		Links:       d.Links,
		Env:         d.Env,
		Secrets:     d.Secrets,
	}, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionNeonBranch(ctx, d.Name, meta, d, res); err != nil {
		return nil, err
	}
	res.SetLink(buildDatabaseLink(meta, d, res))
	if err := registerDatabaseOutputs(ctx, meta, res); err != nil {
		return nil, err
	}
	return res, nil
}

// Private helpers ------------------------------------------------------------

type neonParentRefs struct {
	projectID      pulumi.StringOutput
	parentBranchID pulumi.StringOutput
}

func provisionNeonBranch(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Database,
	res *DatabaseResource,
) error {
	refs, err := resolveNeonRefs(ctx, name, meta, spec)
	if err != nil {
		return err
	}
	branch, err := neonprov.NewBranch(ctx, name+"-branch", &neonprov.BranchArgs{
		ProjectId: refs.projectID,
		Name:      pulumi.String(meta.Environment.String()),
		ParentId:  optionalStringPtr(refs.parentBranchID),
	}, core.ChildOptions(res, meta)...)
	if err != nil {
		return err
	}
	res.Branch = branch
	res.BranchID = branch.ID().ToStringOutput()

	ep, err := createNeonEndpoint(ctx, name, meta, spec, refs.projectID, branch, res)
	if err != nil {
		return err
	}
	res.Endpoint = ep
	res.Host = ep.Host
	res.PoolerHost = ep.HostPooling
	return configureCredentialsAndURLs(ctx, name, meta, spec, refs.projectID, branch, ep, res)
}

func resolveNeonRefs(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec Database) (neonParentRefs, error) {
	refs, defaultBranch := initDirectNeonRefs(spec)
	if spec.ProjectStackRef != "" {
		sr, err := pulumi.NewStackReference(ctx, name+"-project-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(spec.ProjectStackRef)})
		if err != nil {
			return neonParentRefs{}, err
		}
		if !isSet(refs.projectID) {
			refs.projectID = preferStackOutput(sr, "neonProjectId", "projectId")
		}
		if !isSet(defaultBranch) {
			defaultBranch = preferStackOutput(sr, "neonDefaultBranchId", "defaultBranchId")
		}
	}
	parentBranch, err := resolveParentBranch(ctx, name, meta, spec, defaultBranch)
	if err != nil {
		return neonParentRefs{}, err
	}
	refs.parentBranchID = parentBranch
	return refs, nil
}

func initDirectNeonRefs(spec Database) (neonParentRefs, pulumi.StringOutput) {
	var refs neonParentRefs
	var defaultBranch pulumi.StringOutput
	if spec.NeonProject != nil {
		refs.projectID = spec.NeonProject.ProjectID
		defaultBranch = spec.NeonProject.DefaultBranchID
	}
	if spec.ProjectID != nil {
		refs.projectID = spec.ProjectID.ToStringOutput()
	}
	if spec.DefaultBranchID != nil {
		defaultBranch = spec.DefaultBranchID.ToStringOutput()
	}
	return refs, defaultBranch
}

func resolveParentBranch(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Database,
	defaultBranch pulumi.StringOutput,
) (pulumi.StringOutput, error) {
	if spec.ParentBranchID != nil {
		return spec.ParentBranchID.ToStringOutput(), nil
	}
	if meta.BranchFrom == "" {
		return defaultBranch, nil
	}
	if spec.BranchStackRef != "" {
		br, err := pulumi.NewStackReference(ctx, name+"-branch-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(spec.BranchStackRef)})
		if err != nil {
			return pulumi.StringOutput{}, err
		}
		return br.GetStringOutput(pulumi.String("branchId")), nil
	}
	return defaultBranch, nil
}

func createNeonEndpoint(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Database,
	projectID pulumi.StringOutput,
	branch *neonprov.Branch,
	parent pulumi.Resource,
) (*neonprov.Endpoint, error) {
	suspend := 300
	if meta.Protected {
		suspend = 0
	}
	if spec.SuspendSeconds != nil {
		suspend = *spec.SuspendSeconds
	}
	return neonprov.NewEndpoint(ctx, name+"-ep", &neonprov.EndpointArgs{
		ProjectId:             projectID,
		BranchId:              branch.ID(),
		Type:                  pulumi.String("read_write"),
		SuspendTimeoutSeconds: pulumi.Float64(float64(suspend)),
	}, pulumi.Parent(parent))
}

func configureCredentialsAndURLs(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec Database,
	projectID pulumi.StringOutput,
	branch *neonprov.Branch,
	ep *neonprov.Endpoint,
	res *DatabaseResource,
) error {
	roleName := defaultIdentifier(spec.RoleName, "openbase_app")
	dbName := defaultIdentifier(spec.DatabaseName, "openbase")
	passwordOut, err := ensureRoleAndDatabase(ctx, name, meta, projectID, branch, ep, roleName, dbName, res)
	if err != nil {
		return err
	}
	res.DatabaseURL = formatPostgresURL(roleName, passwordOut, ep.HostPooling, dbName)
	res.DirectURL = formatPostgresURL(roleName, passwordOut, ep.Host, dbName)
	return nil
}

func ensureRoleAndDatabase(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	projectID pulumi.StringOutput,
	branch *neonprov.Branch,
	ep *neonprov.Endpoint,
	roleName, dbName string,
	res *DatabaseResource,
) (pulumi.StringOutput, error) {
	if meta.BranchFrom != "" {
		readyBranchID := pulumi.All(branch.ID(), ep.ID()).ApplyT(func(vs []any) string {
			return string(vs[0].(pulumi.ID))
		}).(pulumi.StringOutput)
		pw := neonprov.GetBranchRolePasswordOutput(ctx, neonprov.GetBranchRolePasswordOutputArgs{
			ProjectId: projectID,
			BranchId:  readyBranchID,
			RoleName:  pulumi.String(roleName),
		})
		return pulumi.ToSecret(pw.Password()).(pulumi.StringOutput), nil
	}
	role, err := neonprov.NewRole(ctx, name+"-role", &neonprov.RoleArgs{
		ProjectId: projectID,
		BranchId:  branch.ID(),
		Name:      pulumi.String(roleName),
	}, pulumi.Parent(res), pulumi.DependsOn([]pulumi.Resource{ep}))
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	db, err := neonprov.NewDatabase(ctx, name+"-db", &neonprov.DatabaseArgs{
		ProjectId: projectID,
		BranchId:  branch.ID(),
		Name:      pulumi.String(dbName),
		OwnerName: role.Name,
	}, pulumi.Parent(res), pulumi.DependsOn([]pulumi.Resource{ep, role}))
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	res.Role = role
	res.Database = db
	return pulumi.ToSecret(role.Password).(pulumi.StringOutput), nil
}

func formatPostgresURL(roleName string, password, host pulumi.StringOutput, dbName string) pulumi.StringOutput {
	raw := pulumi.All(password, host).ApplyT(func(vs []any) string {
		pw := vs[0].(string)
		h := vs[1].(string)
		u := url.URL{
			Scheme:   "postgresql",
			User:     url.UserPassword(roleName, pw),
			Host:     h,
			Path:     "/" + dbName,
			RawQuery: "sslmode=require",
		}
		return u.String()
	}).(pulumi.StringOutput)
	return pulumi.ToSecret(raw).(pulumi.StringOutput)
}

func buildDatabaseLink(meta core.ResourceMeta, spec Database, res *DatabaseResource) core.LinkDescriptor {
	dbKey, directKey, hostKey := "DATABASE_URL", "DIRECT_URL", "DATABASE_HOST"
	if spec.EnvPrefix != "" {
		p := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(spec.EnvPrefix), "-", "_"))
		dbKey = p + "_DATABASE_URL"
		directKey = p + "_DIRECT_URL"
		hostKey = p + "_DATABASE_HOST"
	}
	envMap := map[string]pulumi.StringOutput{hostKey: res.PoolerHost}
	for k, v := range spec.Env {
		envMap[k] = v.ToStringOutput()
	}
	secrets := map[string]pulumi.StringOutput{
		dbKey:     res.DatabaseURL,
		directKey: res.DirectURL,
	}
	for k, v := range spec.Secrets {
		secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return core.LinkDescriptor{
		Name:    meta.Name,
		Kind:    meta.Kind,
		Env:     envMap,
		Secrets: secrets,
	}
}

func registerDatabaseOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *DatabaseResource) error {
	return ctx.RegisterResourceOutputs(res, pulumi.Map{
		"canonicalName": pulumi.String(meta.CanonicalName),
		"branchId":      res.BranchID,
		"branchFrom":    pulumi.String(meta.BranchFrom.String()),
		"host":          res.Host,
		"poolerHost":    res.PoolerHost,
		"databaseUrl":   res.DatabaseURL,
		"directUrl":     res.DirectURL,
	})
}

func defaultIdentifier(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func isSet(out pulumi.StringOutput) bool {
	return !reflect.ValueOf(out).IsZero()
}

func optionalStringPtr(out pulumi.StringOutput) pulumi.StringPtrInput {
	if !isSet(out) {
		return nil
	}
	return out
}

func preferStackOutput(sr *pulumi.StackReference, primaryKey, fallbackKey string) pulumi.StringOutput {
	return pulumi.All(
		sr.GetOutput(pulumi.String(primaryKey)),
		sr.GetOutput(pulumi.String(fallbackKey)),
	).ApplyT(func(vs []any) string {
		if first, ok := vs[0].(string); ok && first != "" {
			return first
		}
		if second, ok := vs[1].(string); ok {
			return second
		}
		return ""
	}).(pulumi.StringOutput)
}
