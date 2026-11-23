package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/youtrack-docgen/collector"
	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack"
)

func newDiffCmd(now func() time.Time, factories map[string]collector.Factory) *cobra.Command {
	var path string
	var allowMassStale, exitCode bool
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show what would change in the YouTrack Knowledge Base",
		Long: "Runs the pipeline collector -> filter -> render and compares the result with the articles\n" +
			"in YouTrack. It prints a unified diff per changed article, including the articles that would be\n" +
			"marked as stale or restored, and a summary. It never writes. It fails like publish when the run\n" +
			"would mark too many articles as stale; --allow-mass-stale allows that.\n" +
			"Exit status: 0 on success; with --exit-code, 2 when articles would change; 1 on any error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			changed, err := diffYouTrack(cmd.Context(), cfg, factories, runOptions{allowMassStale: allowMassStale}, now(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if exitCode && changed {
				cmd.SilenceErrors = true
				return errChanges
			}
			return nil
		},
	}
	addAllowMassStaleFlag(cmd, &allowMassStale)
	cmd.Flags().BoolVar(&exitCode, "exit-code", false, "exit with status 2 when the knowledge base would change")
	cmd.Flags().StringVarP(&path, "config", "c", defaultConfigPath, "path to the config file")
	return cmd
}

// diffYouTrack prints the unified diff of every article that would change and
// a summary line, and reports whether anything would change. It only reads from YouTrack.
func diffYouTrack(ctx context.Context, cfg *config.Config, factories map[string]collector.Factory, opts runOptions, at time.Time, stdout, stderr io.Writer) (changed bool, err error) {
	api, err := newYouTrackClient(cfg)
	if err != nil {
		return false, err
	}
	b, err := buildDocuments(ctx, cfg, factories, at)
	if err != nil {
		return false, err
	}
	if err := printFilterReport(stderr, cfg.EnabledCollectors(), b.filterReport); err != nil {
		return false, err
	}
	if err := printWarnings(stderr, b.linkWarnings); err != nil {
		return false, err
	}
	root := b.root
	target, err := resolveProject(ctx, api, cfg)
	if err != nil {
		return false, err
	}
	if err := printIncompleteSources(stderr, b.incomplete); err != nil {
		return false, err
	}
	planner, err := youtrack.NewPlanner(api, sinkConfig(target, cfg, opts, b))
	if err != nil {
		return false, err
	}
	// A refused run still has a plan: show it, then fail like publish does.
	plan, planErr := planner.Plan(ctx, root)
	if planErr != nil && !errors.Is(planErr, youtrack.ErrMassStale) {
		return false, planErr
	}
	if err := printWarnings(stderr, plan.Warnings); err != nil {
		return false, err
	}
	if _, err := fmt.Fprintf(stdout, "%ssink youtrack (dry run): %s\n", plan.Diff(), plan.Summary()); err != nil {
		return false, err
	}
	if planErr != nil {
		return false, fmt.Errorf("sink %s: %w", youtrackSink, planErr)
	}
	return len(plan.Changes) > 0, nil
}
