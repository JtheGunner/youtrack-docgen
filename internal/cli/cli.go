// Package cli implements the youtrack-docgen command line.
package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
)

const defaultConfigPath = "docgen.yaml"

// NewRootCmd builds the command tree. Output goes to stdout and stderr so
// tests can capture it.
func NewRootCmd(stdout, stderr io.Writer) *cobra.Command {
	return newRootCmd(stdout, stderr, time.Now)
}

func newRootCmd(stdout, stderr io.Writer, now func() time.Time) *cobra.Command {
	root := &cobra.Command{
		Use:           "youtrack-docgen",
		Short:         "Generate infrastructure documentation for the YouTrack Knowledge Base",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newValidateConfigCmd(), newGenerateCmd(now), newDiffCmd(now), newPublishCmd(now))
	return root
}

func newValidateConfigCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "validate-config",
		Short: "Check docgen.yaml and the environment variables it references",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "config ok: %s\ncollectors: %s\nsinks: %s\n",
				path, names(cfg.EnabledCollectors()), names(cfg.EnabledSinks()))
			return err
		},
	}
	cmd.Flags().StringVarP(&path, "config", "c", defaultConfigPath, "path to the config file")
	return cmd
}

func names(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// Execute runs the command line with the process streams.
func Execute(stdout, stderr io.Writer) error {
	return NewRootCmd(stdout, stderr).Execute()
}
