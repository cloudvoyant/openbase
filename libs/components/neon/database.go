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

// ErrProjectIDRequired reports a missing Project or ProjectID on DatabaseArgs.
var ErrProjectIDRequired = errors.New("neon: project or projectId is required")

// DatabaseArgs configures a per-environment Neon branch, compute endpoint, role, and database.
type DatabaseArgs struct {
	// ProjectName overrides ctx.Project() in canonical naming and default tags.
	ProjectName string
	// Repo records the source repository slug in openbase.io/* resource tags.
	Repo string
	// Environment overrides ctx.Stack() when resolving the deployment tier (dev, stage, prod, preview-*).
	Environment string
	// Kind overrides the default "neon-database" kind stored in openbaseManifest.
	Kind string
	// BranchFrom names the parent environment stack to branch from via Neon copy-on-write (defaults to "prod" on stage and "dev" on preview-*).
	BranchFrom string
	// Backup overrides tier-default deletion protection on the Neon branch.
	Backup *bool
	// Links lists upstream OpenBase resources linked to this database.
	Links []core.Resource
	// Project supplies the shared neon.Project (or neon.LookupProject(shared)) that owns this branch.
	Project ProjectInput
	// ProjectID sets the Neon project ID directly when Project is omitted.
	ProjectID pulumi.StringInput
	// DefaultBranchID sets the root Neon branch ID directly when Project is omitted.
	DefaultBranchID pulumi.StringInput
	// ParentBranchID overrides stack-reference lookup of the parent Neon branch ID when BranchFrom is set.
	ParentBranchID pulumi.StringInput
	// BranchStackRef overrides the Pulumi StackReference name used to read the parent environment's branchId output.
	BranchStackRef string
	// APIKey supplies the Neon API key used to call reveal_password on the Neon v2 API (inherited from Project when in-memory).
	APIKey pulumi.StringInput
	// APIBaseURL overrides the Neon v2 API base URL (used in offline tests).
	APIBaseURL string
	// DatabaseName sets the PostgreSQL database name created on root branches (defaults to "openbase").
	DatabaseName string
	// RoleName sets the PostgreSQL role name created on root branches (defaults to "openbase_app").
	RoleName string
	// SuspendSeconds maps to Neon EndpointArgs.SuspendTimeoutSeconds to scale idle computes to zero (defaults to 300s on dev/preview and 0/always-on on prod/stage).
	SuspendSeconds *int
}

// Database is the provisioned Neon PostgreSQL branch handle with pooled DATABASE_URL and direct DIRECT_URL secrets.
type Database struct {
	pulumi.ResourceState
	core.StatefulMeta

	// Branch is the underlying Pulumi neon.Branch resource for this environment.
	Branch *neonprov.Branch
	// Endpoint is the underlying Pulumi neon.Endpoint read-write compute resource.
	Endpoint *neonprov.Endpoint
	// Role is the underlying Pulumi neon.Role resource (nil on child branches that inherit roles from their parent).
	Role *neonprov.Role
	// Database is the underlying Pulumi neon.Database resource (nil on child branches that inherit databases from their parent).
	Database *neonprov.Database
	// BranchID is the Neon branch ID string output exported so downstream stacks can branch from this environment.
	BranchID pulumi.StringOutput
	// Host is the direct unpooled PostgreSQL compute hostname.
	Host pulumi.StringOutput
	// PoolerHost is the PgBouncer pooled hostname (-pooler) for serverless workloads.
	PoolerHost pulumi.StringOutput
	// DatabaseURL is the secret pooled PostgreSQL connection URL (sslmode=require).
	DatabaseURL pulumi.StringOutput
	// DirectURL is the secret direct unpooled PostgreSQL connection URL for schema migrations.
	DirectURL pulumi.StringOutput
}

// NewDatabase provisions a Neon PostgreSQL branch satisfying core.StatefulResource.
func NewDatabase(ctx *pulumi.Context, name string, args *DatabaseArgs, opts ...pulumi.ResourceOption) (*Database, error) {
	if args == nil || (args.Project == nil && args.ProjectID == nil) {
		return nil, ErrProjectIDRequired
	}
	spec := *args
	applyDefaultDBBranching(ctx, &spec)
	res := &Database{}
	meta, err := core.InitStateful(ctx, "openbase:neon:Database", "neon-database", res, &res.StatefulMeta, core.StatefulSpec{
		Name:        name,
		Project:     spec.ProjectName,
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
	if spec.Project != nil {
		pv := spec.Project.neonProjectValues()
		refs.projectID = pv.projectID
		defaultBranch = pv.defaultBranchID
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
	if d.Project == nil {
		return
	}
	pv := d.Project.neonProjectValues()
	if d.APIKey == nil {
		d.APIKey = pv.apiKey
	}
	if d.APIBaseURL == "" {
		d.APIBaseURL = pv.apiBaseURL
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
