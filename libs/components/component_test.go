package components

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Fixtures -------------------------------------------------------------------

type mocks struct {
	childParent string
}

// Tests ----------------------------------------------------------------------

func TestComponent(t *testing.T) {
	t.Run("when creating a greeting component", func(t *testing.T) {
		// Arrange
		recorder := &mocks{}
		messageCh := make(chan string, 1)

		// Act
		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			c, err := NewGreeting(ctx, "test", GreetingArgs{Name: pulumi.String("Ada")})
			if err != nil {
				return err
			}
			c.Message.ApplyT(func(s string) string {
				messageCh <- s
				return s
			})
			return nil
		}, pulumi.WithMocks("example", "test", recorder))

		// Assert
		t.Run("it registers the parented child and sets the greeting message", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			if recorder.childParent == "" {
				t.Fatal("child has no parent")
			}
			if got, want := <-messageCh, "Hello, Ada!"; got != want {
				t.Fatalf("message = %q, want %q", got, want)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

func (m *mocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	if a.TypeToken == "premise:index:Message" {
		m.childParent = a.RegisterRPC.GetParent()
	}
	return a.Name + "_id", a.Inputs, nil
}

func (m *mocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) { return a.Args, nil }
