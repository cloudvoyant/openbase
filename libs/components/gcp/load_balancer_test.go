package gcp_test

import (
	"errors"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/gcp"
)

// Public API -----------------------------------------------------------------

func TestCloudLoadBalancer(t *testing.T) {
	t.Run("given gcp.CloudLoadBalancer specs", func(t *testing.T) {
		t.Run("when BaseDomain is gcp.cloudvoyant.io, then it provisions Global IPv4, wildcard Certificate Manager TLS, and Serverless NEG URL-mask routing", func(t *testing.T) {
			// Arrange.
			mocks := newGCPMocks()
			var gotMask string

			// Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				lb, err := gcp.CloudLoadBalancer{
					Project:    "storefront",
					GCPProject: pulumi.String("cloudvoyant-openbase"),
					BaseDomain: "gcp.cloudvoyant.io",
				}.Register(ctx)
				if err != nil {
					return err
				}
				gotMask = lb.URLMask
				if lb.OpenBaseMeta().CanonicalName != "storefront-lb" {
					t.Fatalf("shared CanonicalName = %q, want storefront-lb", lb.OpenBaseMeta().CanonicalName)
				}
				return nil
			}, pulumi.WithMocks("storefront", "shared", mocks))

			// Assert.
			requireGCPNoErr(t, err)
			if gotMask != "<service>.gcp.cloudvoyant.io" {
				t.Fatalf("URLMask = %q, want <service>.gcp.cloudvoyant.io", gotMask)
			}
			mocks.assertCount(t, "gcp:compute/globalAddress:GlobalAddress", 1)
			mocks.assertCount(t, "gcp:certificatemanager/dnsAuthorization:DnsAuthorization", 1)
			mocks.assertCount(t, "gcp:certificatemanager/certificate:Certificate", 1)
			mocks.assertCount(t, "gcp:certificatemanager/certificateMap:CertificateMap", 1)
			mocks.assertCount(t, "gcp:certificatemanager/certificateMapEntry:CertificateMapEntry", 1)
			mocks.assertCount(t, "gcp:compute/regionNetworkEndpointGroup:RegionNetworkEndpointGroup", 1)
			mocks.assertCount(t, "gcp:compute/backendService:BackendService", 1)
			mocks.assertCount(t, "gcp:compute/uRLMap:URLMap", 1)
			mocks.assertCount(t, "gcp:compute/targetHttpsProxy:TargetHttpsProxy", 1)
			mocks.assertCount(t, "gcp:compute/globalForwardingRule:GlobalForwardingRule", 1)
		})

		t.Run("when BaseDomain is empty, then Register rejects with ErrBaseDomainRequired", func(t *testing.T) {
			// Arrange / Act.
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				_, e := gcp.CloudLoadBalancer{Project: "storefront"}.Register(ctx)
				return e
			}, pulumi.WithMocks("storefront", "prod", newGCPMocks()))

			// Assert.
			if !errors.Is(err, gcp.ErrBaseDomainRequired) {
				t.Fatalf("expected ErrBaseDomainRequired, got %v", err)
			}
		})
	})
}
