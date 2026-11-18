package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack"
)

func newDiffCmd(now func() time.Time) *cobra.Command {
	var path string
	var allowMassStale bool
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show what would change in the YouTrack Knowledge Base",
		Long: "Runs the pipeline collector -> filter -> render and compares the result with the articles\n" +
			"in YouTrack. It prints a unified diff per changed article, including the articles that would be\n" +
			"marked as stale or restored, and a summary. It never writes. It fails like publish when the run\n" +
			"would mark too many articles as stale; --allow-mass-stale allows that.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			return diffYouTrack(cmd.Context(), cfg, runOptions{allowMassStale: allowMassStale}, now(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	addAllowMassStaleFlag(cmd, &allowMassStale)
	cmd.Flags().StringVarP(&path, "config", "c", defaultConfigPath, "path to the config file")
	return cmd
}

// diffYouTrack prints the unified diff of every article that would change and
// a summary line. It only reads from YouTrack.
func diffYouTrack(ctx context.Context, cfg *config.Config, opts runOptions, at time.Time, stdout, stderr io.Writer) error {
	api, err := newYouTrackClient(cfg)
	if err != nil {
		return err
	}
	root, err := buildDocuments(ctx, cfg, at)
	if err != nil {
		return err
	}
	target, err := resolveProject(ctx, api, cfg)
	if err != nil {
		return err
	}
	planner, err := youtrack.NewPlanner(api, sinkConfig(target, cfg, opts))
	if err != nil {
		return err
	}
	// A refused run still has a plan: show it, then fail like publish does.
	plan, planErr := planner.Plan(ctx, root)
	if planErr != nil && !errors.Is(planErr, youtrack.ErrMassStale) {
		return planErr
	}
	if err := printWarnings(stderr, plan.Warnings); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "%ssink youtrack (dry run): %s\n", plan.Diff(), plan.Summary()); err != nil {
		return err
	}
	if planErr != nil {
		return fmt.Errorf("sink %s: %w", youtrackSink, planErr)
	}
	return nil
}
