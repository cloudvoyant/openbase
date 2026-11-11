package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

var version = "dev"

// RootCommand builds an independent command tree.
func RootCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "based",
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Hello from based")
			return err
		},
	}
}

// Private helpers ------------------------------------------------------------

func main() {
	if err := RootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
