package neon

import (
	"errors"
	"net/url"
	"reflect"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	neonprov "github.com/cloudvoyant/openbase/libs/internal/pulumi-neon-go-sdk"

	"github.com/cloudvoyant/openbase/libs/components/core"
)

// Public API -----------------------------------------------------------------

var _ core.StatefulResource = (*Database)(nil)

// ErrProjectIDRequired reports a missing NeonProject, Shared, ProjectID, and ProjectStackRef on DatabaseArgs.
var ErrProjectIDRequired = errors.New("neon: neonProject, shared, projectId, or projectStackRef is required")

// DatabaseArgs configures a per-environment Neon branch, compute endpoint, role, and database.
type DatabaseArgs struct {
	Project         string
	Repo            string
	Environment     string
	Kind            string
	BranchFrom      string
	Backup          *bool
	Links           []core.Resource
	NeonProject     *Project
	Shared          *core.ScopeRef
	ProjectID       pulumi.StringInput
	DefaultBranchID pulumi.StringInput
	ParentBranchID  pulumi.StringInput
	ProjectStackRef string
	BranchStackRef  string
	APIKey          pulumi.StringInput
	APIBaseURL      string
	DatabaseName    string
	RoleName        string
	SuspendSeconds  *int
}

// Database is the provisioned Neon PostgreSQL branch handle with pooled DATABASE_URL and direct DIRECT_URL secrets.
type Database struct {
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

// NewDatabase provisions a Neon PostgreSQL branch satisfying core.StatefulResource.
func NewDatabase(ctx *pulumi.Context, name string, args *DatabaseArgs, opts ...pulumi.ResourceOption) (*Database, error) {
	if args == nil || !hasNeonProjectSource(args) {
		return nil, ErrProjectIDRequired
	}
	spec := *args
	applyDefaultDBBranching(ctx, &spec)
	res := &Database{}
	meta, err := core.InitStateful(ctx, "openbase:neon:Database", "neon-database", res, &res.StatefulMeta, core.StatefulSpec{
		Name:        name,
		Project:     spec.Project,
		Repo:        spec.Repo,
		Environment: spec.Environment,
		Kind:        spec.Kind,
		BranchFrom:  spec.BranchFrom,
		Backup:      spec.Backup,
		Links:       spec.Links,
	}, opts...)
	if err != nil {
		return nil, err
	}
	spec.BranchStackRef = core.DefaultBranchStackRef(ctx, meta.Project, meta.BranchFrom.String(), spec.BranchStackRef)
	inheritNeonProjectAPISettings(&spec)
	if err := provisionNeonBranch(ctx, name, meta, &spec, res); err != nil {
		return nil, err
	}
	res.PublishLink(ctx, buildDatabaseLink(meta, res))
	return res, registerDatabaseOutputs(ctx, meta, res)
}

// Private helpers ------------------------------------------------------------

type neonParentRefs struct {
	projectID      pulumi.StringOutput
	parentBranchID pulumi.StringOutput
}

func hasNeonProjectSource(args *DatabaseArgs) bool {
	return args.NeonProject != nil || args.Shared != nil || args.ProjectID != nil || args.ProjectStackRef != ""
}

func applyDefaultDBBranching(ctx *pulumi.Context, spec *DatabaseArgs) {
	if env, err := core.ResolveEnvironment(ctx, spec.Environment); err == nil {
		spec.BranchFrom = core.DefaultBranchFrom(env, spec.BranchFrom, true)
	}
}

func provisionNeonBranch(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec *DatabaseArgs,
	res *Database,
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
	res.PoolerHost = ep.HostPooler
	return configureCredentialsAndURLs(ctx, name, meta, spec, refs.projectID, branch, ep, res)
}

func resolveNeonRefs(ctx *pulumi.Context, name string, meta core.ResourceMeta, spec *DatabaseArgs) (neonParentRefs, error) {
	refs, defaultBranch := initDirectNeonRefs(spec)
	sr, err := core.ResolveStackReference(ctx, name+"-project-ref", spec.Shared, spec.ProjectStackRef)
	if err != nil {
		return neonParentRefs{}, err
	}
	if sr != nil {
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

func initDirectNeonRefs(spec *DatabaseArgs) (neonParentRefs, pulumi.StringOutput) {
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
	spec *DatabaseArgs,
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
	spec *DatabaseArgs,
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

func inheritNeonProjectAPISettings(d *DatabaseArgs) {
	if d.NeonProject == nil {
		return
	}
	if d.APIKey == nil {
		d.APIKey = d.NeonProject.apiKey
	}
	if d.APIBaseURL == "" {
		d.APIBaseURL = d.NeonProject.apiBaseURL
	}
}

func configureCredentialsAndURLs(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec *DatabaseArgs,
	projectID pulumi.StringOutput,
	branch *neonprov.Branch,
	ep *neonprov.Endpoint,
	res *Database,
) error {
	roleName := defaultIdentifier(spec.RoleName, "openbase_app")
	dbName := defaultIdentifier(spec.DatabaseName, "openbase")
	passwordOut, err := ensureRoleAndDatabase(ctx, name, meta, spec, projectID, branch, ep, roleName, dbName, res)
	if err != nil {
		return err
	}
	res.DatabaseURL = formatPostgresURL(roleName, passwordOut, ep.HostPooler, dbName)
	res.DirectURL = formatPostgresURL(roleName, passwordOut, ep.Host, dbName)
	return nil
}

func ensureRoleAndDatabase(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	spec *DatabaseArgs,
	projectID pulumi.StringOutput,
	branch *neonprov.Branch,
	ep *neonprov.Endpoint,
	roleName, dbName string,
	res *Database,
) (pulumi.StringOutput, error) {
	if meta.BranchFrom != "" {
		readyBranchID := pulumi.All(branch.ID(), ep.ID()).ApplyT(func(vs []any) string {
			return string(vs[0].(pulumi.ID))
		}).(pulumi.StringOutput)
		return revealRolePasswordOutput(ctx, projectID, readyBranchID, roleName, spec.APIKey, spec.APIBaseURL), nil
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
	readyBranchID := pulumi.All(branch.ID(), role.ID(), db.ID()).ApplyT(func(vs []any) string {
		return string(vs[0].(pulumi.ID))
	}).(pulumi.StringOutput)
	return revealRolePasswordOutput(ctx, projectID, readyBranchID, roleName, spec.APIKey, spec.APIBaseURL), nil
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

func buildDatabaseLink(meta core.ResourceMeta, res *Database) core.LinkDescriptor {
	return core.LinkDescriptor{
		Name: meta.Name,
		Kind: meta.Kind,
		Attributes: map[string]pulumi.StringOutput{
			"branchId":   res.BranchID,
			"host":       res.Host,
			"poolerHost": res.PoolerHost,
		},
		StackExports: map[string]pulumi.Input{
			"branchId":    res.BranchID,
			"host":        res.Host,
			"poolerHost":  res.PoolerHost,
			"databaseUrl": res.DatabaseURL,
			"directUrl":   res.DirectURL,
		},
	}
}

func registerDatabaseOutputs(ctx *pulumi.Context, meta core.ResourceMeta, res *Database) error {
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
