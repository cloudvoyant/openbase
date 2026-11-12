// Package components provides cloud-neutral Pulumi components.
package components

import "github.com/pulumi/pulumi/sdk/v3/go/pulumi"

// Public API -----------------------------------------------------------------

// GreetingArgs supplies a greeting's input.
type GreetingArgs struct{ Name pulumi.StringInput }

// Greeting is a reusable component with an exported message.
type Greeting struct {
	pulumi.ResourceState
	Message pulumi.StringOutput
}

// NewGreeting registers a component and a parented child without cloud providers.
func NewGreeting(ctx *pulumi.Context, name string, args GreetingArgs, opts ...pulumi.ResourceOption) (*Greeting, error) {
	c := &Greeting{}
	if err := ctx.RegisterComponentResource("premise:index:Greeting", name, c, opts...); err != nil {
		return nil, err
	}
	child := &pulumi.ResourceState{}
	if err := ctx.RegisterComponentResource("premise:index:Message", name+"-message", child, pulumi.Parent(c)); err != nil {
		return nil, err
	}
	c.Message = args.Name.ToStringOutput().ApplyT(func(n string) string { return "Hello, " + n + "!" }).(pulumi.StringOutput)
	if err := ctx.RegisterResourceOutputs(c, pulumi.Map{"message": c.Message}); err != nil {
		return nil, err
	}
	return c, nil
}

// Private helpers ------------------------------------------------------------
