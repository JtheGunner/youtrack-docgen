package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/diff"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/youtrack/client"
)

const youtrackSink = "youtrack"

func newPublishCmd(now func() time.Time) *cobra.Command {
	var path string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Collect infrastructure data and publish the documentation to the YouTrack Knowledge Base",
		Long: "Runs the pipeline collector -> filter -> render and writes the result to the YouTrack\n" +
			"Knowledge Base. Articles are created or updated, never deleted; articles of objects that\n" +
			"disappeared are marked as stale.\n\n" +
			"With --dry-run nothing is written: the command prints what would change, exactly like the\n" +
			"diff command.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if dryRun {
				return diffYouTrack(cmd.Context(), cfg, now(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			return publish(cmd.Context(), cfg, now(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVarP(&path, "config", "c", defaultConfigPath, "path to the config file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "write nothing; print a unified diff of what would change in YouTrack")
	return cmd
}

// publish writes the rendered documents to YouTrack and prints a summary.
// The documents are built first: a failed collection must not reach YouTrack,
// because publishing a partial tree would mark the missing articles as stale.
func publish(ctx context.Context, cfg *config.Config, at time.Time, stdout, stderr io.Writer) error {
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
	s, err := youtrack.New(api, target)
	if err != nil {
		return err
	}
	res, err := s.Publish(ctx, root)
	if werr := printWarnings(stderr, res.Warnings); werr != nil && err == nil {
		err = werr
	}
	if err != nil {
		return fmt.Errorf("sink %s: %w", youtrackSink, err)
	}
	_, err = fmt.Fprintf(stdout, "sink %s: %d created, %d updated, %d unchanged, %d marked stale, %d restored\n",
		youtrackSink, res.Created, res.Updated, res.Unchanged, res.Stale, res.Restored)
	return err
}

// newYouTrackClient builds the client for the youtrack sink, which must be
// enabled.
func newYouTrackClient(cfg *config.Config) (*client.Client, error) {
	if !slices.Contains(cfg.EnabledSinks(), youtrackSink) {
		return nil, errors.New("no youtrack sink: enable the youtrack sink")
	}
	return client.New(cfg.YouTrack.URL, cfg.YouTrack.Token.Reveal())
}

// resolveProject looks up the project named in the config and returns the
// sink config for it. The query uses the key the server reported, in braces,
// so the listing and the lookup refer to the same project.
func resolveProject(ctx context.Context, api *client.Client, cfg *config.Config) (youtrack.Config, error) {
	project, err := api.FindProject(ctx, cfg.YouTrack.Project)
	if err != nil {
		return youtrack.Config{}, err
	}
	if project == nil {
		return youtrack.Config{}, fmt.Errorf("youtrack project %q not found: check youtrack.project and the permissions of the token", cfg.YouTrack.Project)
	}
	return youtrack.Config{ProjectID: project.ID, ProjectQuery: "project: {" + project.ShortName + "}"}, nil
}

// printWarnings writes one line per warning of the youtrack sink.
func printWarnings(stderr io.Writer, warnings []string) error {
	for _, w := range warnings {
		if _, err := fmt.Fprintf(stderr, "warning: %s\n", diff.EscapeControl(w)); err != nil {
			return err
		}
	}
	return nil
}
