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

var _ core.StatefulResource = (*Database)(nil)

// ErrProjectIDRequired reports a missing Neon ProjectID and SharedStackRef on DatabaseArgs.
var ErrProjectIDRequired = errors.New("neon: projectId or sharedStackRef is required")

// DatabaseArgs configures a per-environment Neon branch, compute endpoint, role, and database.
type DatabaseArgs struct {
	core.StatefulResourceArgs
	ProjectID       pulumi.StringInput
	DefaultBranchID pulumi.StringInput
	ParentBranchID  pulumi.StringInput
	SharedStackRef  string
	BranchStackRef  string
	DatabaseName    string
	RoleName        string
	EnvPrefix       string
	SuspendSeconds  *int
}

// Database provisions a Neon PostgreSQL branch with pooled DATABASE_URL and direct DIRECT_URL secrets.
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

// NewDatabase creates a Neon PostgreSQL branch satisfying core.StatefulResource.
func NewDatabase(ctx *pulumi.Context, name string, args DatabaseArgs, opts ...pulumi.ResourceOption) (*Database, error) {
	if args.ProjectID == nil && args.SharedStackRef == "" {
		return nil, ErrProjectIDRequired
	}
	res := &Database{}
	meta, err := core.InitStateful(ctx, "openbase:neon:Database", name, "neon-database", res, &res.StatefulMeta, args.StatefulResourceArgs, opts...)
	if err != nil {
		return nil, err
	}
	if err := provisionNeonBranch(ctx, name, meta, args, res); err != nil {
		return nil, err
	}
	res.SetLink(buildDatabaseLink(meta, args, res))
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
	args DatabaseArgs,
	res *Database,
) error {
	refs, err := resolveNeonRefs(ctx, name, meta, args)
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

	ep, err := createNeonEndpoint(ctx, name, meta, args, refs.projectID, branch, res)
	if err != nil {
		return err
	}
	res.Endpoint = ep
	res.Host = ep.Host
	res.PoolerHost = ep.HostPooling
	return configureCredentialsAndURLs(ctx, name, meta, args, refs.projectID, branch, ep, res)
}

func resolveNeonRefs(ctx *pulumi.Context, name string, meta core.ResourceMeta, args DatabaseArgs) (neonParentRefs, error) {
	var refs neonParentRefs
	var defaultBranch pulumi.StringOutput
	if args.ProjectID != nil {
		refs.projectID = args.ProjectID.ToStringOutput()
	}
	if args.DefaultBranchID != nil {
		defaultBranch = args.DefaultBranchID.ToStringOutput()
	}
	if args.SharedStackRef != "" {
		sr, err := pulumi.NewStackReference(ctx, name+"-shared-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(args.SharedStackRef)})
		if err != nil {
			return neonParentRefs{}, err
		}
		if !isSet(refs.projectID) {
			refs.projectID = sr.GetStringOutput(pulumi.String("projectId"))
		}
		if !isSet(defaultBranch) {
			defaultBranch = sr.GetStringOutput(pulumi.String("defaultBranchId"))
		}
	}
	parentBranch, err := resolveParentBranch(ctx, name, meta, args, defaultBranch)
	if err != nil {
		return neonParentRefs{}, err
	}
	refs.parentBranchID = parentBranch
	return refs, nil
}

func resolveParentBranch(
	ctx *pulumi.Context,
	name string,
	meta core.ResourceMeta,
	args DatabaseArgs,
	defaultBranch pulumi.StringOutput,
) (pulumi.StringOutput, error) {
	if args.ParentBranchID != nil {
		return args.ParentBranchID.ToStringOutput(), nil
	}
	if meta.BranchFrom == "" {
		return defaultBranch, nil
	}
	if args.BranchStackRef != "" {
		br, err := pulumi.NewStackReference(ctx, name+"-branch-ref", &pulumi.StackReferenceArgs{Name: pulumi.String(args.BranchStackRef)})
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
	args DatabaseArgs,
	projectID pulumi.StringOutput,
	branch *neonprov.Branch,
	parent pulumi.Resource,
) (*neonprov.Endpoint, error) {
	suspend := 300
	if meta.Protected {
		suspend = 0
	}
	if args.SuspendSeconds != nil {
		suspend = *args.SuspendSeconds
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
	args DatabaseArgs,
	projectID pulumi.StringOutput,
	branch *neonprov.Branch,
	ep *neonprov.Endpoint,
	res *Database,
) error {
	roleName := defaultIdentifier(args.RoleName, "openbase_app")
	dbName := defaultIdentifier(args.DatabaseName, "openbase")
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
	res *Database,
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

func buildDatabaseLink(meta core.ResourceMeta, args DatabaseArgs, res *Database) core.LinkDescriptor {
	dbKey, directKey, hostKey := "DATABASE_URL", "DIRECT_URL", "DATABASE_HOST"
	if args.EnvPrefix != "" {
		p := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(args.EnvPrefix), "-", "_"))
		dbKey = p + "_DATABASE_URL"
		directKey = p + "_DIRECT_URL"
		hostKey = p + "_DATABASE_HOST"
	}
	envMap := map[string]pulumi.StringOutput{hostKey: res.PoolerHost}
	for k, v := range args.Env {
		envMap[k] = v.ToStringOutput()
	}
	secrets := map[string]pulumi.StringOutput{
		dbKey:     res.DatabaseURL,
		directKey: res.DirectURL,
	}
	for k, v := range args.Secrets {
		secrets[k] = pulumi.ToSecret(v.ToStringOutput()).(pulumi.StringOutput)
	}
	return core.LinkDescriptor{
		Name:    meta.Name,
		Kind:    meta.Kind,
		Env:     envMap,
		Secrets: secrets,
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
