package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/youtrackt-docgen/internal/config"
	"github.com/JtheGunner/youtrackt-docgen/internal/sink/youtrack"
	"github.com/JtheGunner/youtrackt-docgen/internal/sink/youtrack/client"
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
	if !slices.Contains(cfg.EnabledSinks(), "youtrack") {
		return errors.New("no youtrack sink to compare with: enable the youtrack sink")
	}
	api, err := client.New(cfg.YouTrack.URL, cfg.YouTrack.Token.Reveal())
	if err != nil {
		return err
	}
	planner, err := youtrack.NewPlanner(api, "project: "+cfg.YouTrack.Project)
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
