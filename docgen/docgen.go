package docgen

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"

	"github.com/JtheGunner/youtrack-docgen/collector"
	"github.com/JtheGunner/youtrack-docgen/internal/cli"
	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/spf13/cobra"
)

// App is the youtrack-docgen command line with a set of registered
// collectors. Create it with New; the zero value is not usable. An App is not
// safe for concurrent Register calls.
type App struct {
	factories map[string]collector.Factory
	builtin   map[string]bool
}

// New returns an App with the built-in collectors (netbox, kubernetes)
// registered.
func New() *App {
	app := &App{factories: map[string]collector.Factory{}, builtin: map[string]bool{}}
	for name, factory := range cli.BuiltinCollectors() {
		if err := app.register(name, factory); err != nil {
			panic(fmt.Sprintf("docgen: register built-in collector: %v", err))
		}
		app.builtin[name] = true
	}
	return app
}

// Register adds a collector factory under name, the key of its block below
// collectors in docgen.yaml. The collector the factory builds must report the
// same name. Register fails for an invalid name (lowercase letters, digits,
// "-" and "_", starting with a letter), a nil factory, a name that is already
// registered and the name of a built-in collector.
func (a *App) Register(name string, f collector.Factory) error {
	if a.builtin[name] {
		return fmt.Errorf("collector %q is built in and cannot be replaced", name)
	}
	return a.register(name, f)
}

func (a *App) register(name string, f collector.Factory) error {
	if !config.ValidModuleName(name) {
		return fmt.Errorf(`invalid collector name %q (use lowercase letters, digits, "-" and "_", starting with a letter)`, name)
	}
	if f == nil {
		return fmt.Errorf("collector %q: factory is nil", name)
	}
	if _, ok := a.factories[name]; ok {
		return fmt.Errorf("collector %q is already registered", name)
	}
	a.factories[name] = f
	return nil
}

// Run executes the command line with args (without the program name) and
// writes to stdout and stderr. Cancelling ctx stops a running collection.
// Use ExitCode to map the returned error to a process exit status.
func (a *App) Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return a.newRoot(args, stdout, stderr).ExecuteContext(ctx)
}

func (a *App) newRoot(args []string, stdout, stderr io.Writer) *cobra.Command {
	root := cli.NewRootCmdWithCollectors(stdout, stderr, maps.Clone(a.factories))
	if args == nil {
		// cobra falls back to os.Args for nil args.
		args = []string{}
	}
	root.SetArgs(args)
	return root
}

// Main runs the App with the process arguments and streams. SIGINT and
// SIGTERM cancel the running command. It exits the process with the status
// ExitCode returns for the result.
func (a *App) Main() {
	err := cli.RunWithSignals(a.newRoot(os.Args[1:], os.Stdout, os.Stderr))
	os.Exit(ExitCode(err))
}

// Main runs youtrack-docgen with the built-in collectors only. It is the body
// of the youtrack-docgen command.
func Main() { New().Main() }

// ExitCode maps an error returned by Run to the process exit status: 0 for
// none, 2 when `diff --exit-code` found differences, 1 for every other error.
func ExitCode(err error) int { return cli.ExitCode(err) }
