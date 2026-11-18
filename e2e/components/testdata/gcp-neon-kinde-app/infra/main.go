package main

import (
	_ "embed"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/cloudvoyant/openbase/libs/components/composite"
)

//go:embed app/server.py
var cloudRunServerPy string

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		appProject := envOrDefault("OPENBASE_APP_PROJECT", "OPENBASE_TEST_APP_PROJECT", "obgapp")
		gcpProject := envOrDefault("OPENBASE_GCP_PROJECT", "OPENBASE_TEST_GCP_PROJECT", "cloudvoyant-openbase")
		projectNum := envOrDefault("OPENBASE_GCP_PROJECT_NUMBER", "OPENBASE_TEST_GCP_PROJECT_NUMBER", "785485436615")

		_, err := composite.GcpCloudRunNeonApp{
			Name:             "web",
			Project:          appProject,
			GCPProject:       gcpProject,
			GCPProjectNumber: projectNum,
			Image:            pulumi.String("mirror.gcr.io/library/python:3.12-slim"),
			Command:          []string{"python3", "-c", cloudRunServerPy},
			Auth:             &composite.KindeAppConfig{},
		}.Register(ctx)
		return err
	})
}

func envOrDefault(primaryKey, secondaryKey, fallback string) string {
	if v := os.Getenv(primaryKey); v != "" {
		return v
	}
	if secondaryKey != "" {
		if v := os.Getenv(secondaryKey); v != "" {
			return v
		}
	}
	return fallback
}
