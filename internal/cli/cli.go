// Package cli implements the youtrack-docgen command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/youtrack-docgen/collector"
	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/version"
)

const defaultConfigPath = "docgen.yaml"

// NewRootCmd builds the command tree with the built-in collectors. Output goes
// to stdout and stderr so tests can capture it.
func NewRootCmd(stdout, stderr io.Writer) *cobra.Command {
	return newRootCmd(stdout, stderr, time.Now)
}

// NewRootCmdWithCollectors builds the command tree with the given collector
// factories, keyed by collector name. The map is copied, so later changes to
// it do not reach the commands.
func NewRootCmdWithCollectors(stdout, stderr io.Writer, factories map[string]collector.Factory) *cobra.Command {
	return newRootCmdWithCollectors(stdout, stderr, time.Now, maps.Clone(factories))
}

func newRootCmd(stdout, stderr io.Writer, now func() time.Time) *cobra.Command {
	return newRootCmdWithCollectors(stdout, stderr, now, BuiltinCollectors())
}

func newRootCmdWithCollectors(stdout, stderr io.Writer, now func() time.Time, factories map[string]collector.Factory) *cobra.Command {
	root := &cobra.Command{
		Use:           "youtrack-docgen",
		Short:         "Generate infrastructure documentation for the YouTrack Knowledge Base",
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       version.String(),
	}
	root.SetVersionTemplate("youtrack-docgen {{.Version}}\n")
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newValidateConfigCmd(), newGenerateCmd(now, factories), newDiffCmd(now, factories), newPublishCmd(now, factories))
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

// exitCodeChanges is the exit status of `diff --exit-code` when the Knowledge
// Base differs from the generated documentation.
const exitCodeChanges = 2

// errChanges reports that diff found differences. It is not a failure, so the
// command prints nothing for it and only the exit status carries the result.
var errChanges = errors.New("the knowledge base differs from the generated documentation")

// Execute runs the command line with the process streams. SIGINT and SIGTERM
// cancel the context of the running command, so a collection stops and no
// output file stays half-written.
func Execute(stdout, stderr io.Writer) error {
	return executeWithSignals(NewRootCmd(stdout, stderr))
}

func executeWithSignals(root *cobra.Command) error {
	ctx, stop := SignalContext(context.Background())
	defer stop()
	return root.ExecuteContext(ctx)
}

// SignalContext returns a context that SIGINT and SIGTERM cancel, and the
// function that stops listening for them.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// ExitCode maps an error returned by Execute to the process exit status: 0 for
// none, 2 when `diff --exit-code` found differences, 1 for every other error.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errChanges):
		return exitCodeChanges
	default:
		return 1
	}
}
