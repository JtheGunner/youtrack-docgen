package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack"
)

func newDiffCmd(now func() time.Time) *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show what would change in the YouTrack Knowledge Base",
		Long: "Runs the pipeline collector -> filter -> render and compares the result with the articles\n" +
			"in YouTrack. It prints a unified diff per changed article and a summary, and never writes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			return diffYouTrack(cmd.Context(), cfg, now(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVarP(&path, "config", "c", defaultConfigPath, "path to the config file")
	return cmd
}

// diffYouTrack prints the unified diff of every article that would change and
// a summary line. It only reads from YouTrack.
func diffYouTrack(ctx context.Context, cfg *config.Config, at time.Time, stdout io.Writer) error {
	api, err := newYouTrackClient(cfg)
	if err != nil {
		return err
	}
	planner, err := youtrack.NewPlanner(api, projectQuery(cfg))
	if err != nil {
		return err
	}
	root, err := buildDocuments(ctx, cfg, at)
	if err != nil {
		return err
	}
	plan, err := planner.Plan(ctx, root)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%ssink youtrack (dry run): %s\n", plan.Diff(), plan.Summary())
	return err
}
