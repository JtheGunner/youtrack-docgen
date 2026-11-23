package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/youtrack-docgen/internal/collector"
	"github.com/JtheGunner/youtrack-docgen/internal/collector/kubernetes"
	"github.com/JtheGunner/youtrack-docgen/internal/collector/netbox"
	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/filter"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
	"github.com/JtheGunner/youtrack-docgen/internal/render"
	"github.com/JtheGunner/youtrack-docgen/internal/sink"
	"github.com/JtheGunner/youtrack-docgen/internal/sink/markdown"
)

func newGenerateCmd(now func() time.Time) *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Collect infrastructure data and write the documentation as Markdown files",
		Long: "Runs the pipeline collector -> filter -> render -> sink with the enabled collectors\n" +
			"and writes the result with the markdown sink. The youtrack sink is not part of this command;\n" +
			"use publish to write to the YouTrack Knowledge Base.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			return generate(cmd.Context(), cfg, now(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVarP(&path, "config", "c", defaultConfigPath, "path to the config file")
	return cmd
}

func generate(ctx context.Context, cfg *config.Config, at time.Time, stdout, stderr io.Writer) error {
	sinks, err := buildSinks(cfg, stderr)
	if err != nil {
		return err
	}
	if len(sinks) == 0 {
		return errors.New("no sink to write to: enable the markdown sink")
	}
	built, err := buildDocuments(ctx, cfg, at)
	if err != nil {
		return err
	}
	if err := printFilterReport(stderr, cfg.EnabledCollectors(), built.filterReport); err != nil {
		return err
	}
	if err := printWarnings(stderr, built.linkWarnings); err != nil {
		return err
	}
	root := built.root

	count := 0
	root.Walk(func(render.Document) { count++ })
	for _, s := range sinks {
		if err := s.Publish(ctx, root); err != nil {
			return fmt.Errorf("sink %s: %w", s.Name(), err)
		}
		if _, err := fmt.Fprintf(stdout, "sink %s: wrote %d documents\n", s.Name(), count); err != nil {
			return err
		}
	}
	return nil
}

// built is the result of the pipeline up to rendering.
type built struct {
	root render.Document
	// incomplete maps a source name to the reasons its collection may lack
	// objects. Such a source must not make articles stale.
	incomplete map[string][]string
	// denied are the IDs of entities the filter removed. Their articles are
	// cleaned even when their source was read incompletely.
	denied []string
	// decideDenied answers from an ID alone whether the rules deny it; it
	// covers articles of objects that were not collected.
	decideDenied func(id string) (denied, decided bool)
	// linkWarnings describe ambiguous host matches that were not linked. A
	// warning that names a denied entity is dropped.
	linkWarnings []string
	// filterReport counts what the filter removed per collector.
	filterReport filter.Report
}

// publishableLinkWarnings renders the link warnings from the entities that
// survived the filter only: a warning is dropped when its subject was removed
// or fewer than two candidates are left, and the message never names a removed
// entity.
func publishableLinkWarnings(warnings []model.LinkWarning, kept map[model.ID]bool) []string {
	var out []string
	for _, w := range warnings {
		if msg, ok := w.Describe(func(id model.ID) bool { return kept[id] }); ok {
			out = append(out, msg)
		}
	}
	return out
}

// buildDocuments runs collector -> filter -> render and returns the document
// tree. It writes nothing.
func buildDocuments(ctx context.Context, cfg *config.Config, at time.Time) (built, error) {
	collectors, err := buildCollectors(cfg)
	if err != nil {
		return built{}, err
	}
	flt, err := cfg.Filter()
	if err != nil {
		return built{}, err
	}
	graph := model.NewGraph()
	incomplete := map[string][]string{}
	for _, c := range collectors {
		if err := c.Collect(ctx, graph); err != nil {
			return built{}, fmt.Errorf("collector %s: %w", c.Name(), err)
		}
		if r, ok := c.(collector.CompletenessReporter); ok {
			if reasons := r.Incomplete(); len(reasons) > 0 {
				incomplete[c.Name()] = reasons
			}
		}
	}
	_, linkWarnings := model.LinkWithWarnings(graph)
	filtered, report, err := flt.ApplyWithReport(graph)
	if err != nil {
		return built{}, err
	}
	// Derived from the filtered graph only, so a rejected entity can never
	// appear in a dependency.
	model.DeriveDependencies(filtered)
	var denied []string
	kept := map[model.ID]bool{}
	for _, id := range filtered.IDs() {
		kept[id] = true
	}
	for _, id := range graph.IDs() {
		if !kept[id] {
			denied = append(denied, string(id))
		}
	}
	decide := func(id string) (bool, bool) {
		switch flt.DecideID(model.ID(id)) {
		case filter.Denied:
			return true, true
		case filter.Permitted:
			return false, true
		}
		return false, false
	}
	return built{
		root:       render.Render(filtered, render.Options{GeneratedAt: at, RootTitle: cfg.RootTitle(), Diagrams: cfg.DiagramFormat()}),
		incomplete: incomplete, denied: denied, decideDenied: decide,
		linkWarnings: publishableLinkWarnings(linkWarnings, kept),
		filterReport: report,
	}, nil
}

func buildCollectors(cfg *config.Config) ([]collector.Collector, error) {
	var out []collector.Collector
	for _, name := range cfg.EnabledCollectors() {
		switch name {
		case netbox.Source:
			c, err := netbox.New(cfg.Collectors[name])
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		case kubernetes.Source:
			c, err := kubernetes.New(cfg.Collectors[name])
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		default:
			return nil, fmt.Errorf("collector %q is not supported by generate yet; disable it with enabled: false", name)
		}
	}
	return out, nil
}

// buildSinks returns the sinks generate can write to. The youtrack sink is
// skipped with a note, so one config serves both generate and publish.
func buildSinks(cfg *config.Config, stderr io.Writer) ([]sink.Sink, error) {
	var out []sink.Sink
	for _, name := range cfg.EnabledSinks() {
		switch name {
		case markdown.Name:
			dir, _ := cfg.Sinks[name].Settings["dir"].(string)
			s, err := markdown.New(dir)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		case youtrackSink:
			if _, err := fmt.Fprintln(stderr, "note: skipping the youtrack sink; generate only writes Markdown, use publish for YouTrack"); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("sink %q is not supported by generate yet; disable it with enabled: false", name)
		}
	}
	return out, nil
}
