// Package kubernetes reads workloads, services, ingresses and namespaces from
// Kubernetes manifests on disk (Git checkouts or rendered Kustomize/Helm
// output) into the neutral model, one cluster entity per configured cluster.
// It never talks to a cluster and never maps Secret manifests, environment
// values or annotations.
package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/model"
)

// Source is the source name used in every entity ID created by this collector.
const Source = "kubernetes"

// Collector implements collector.Collector for Kubernetes manifests.
type Collector struct {
	paths    []config.PathSource
	warnings io.Writer
	// incomplete holds the reasons the last Collect skipped something that may
	// exist in the sources.
	incomplete []string
}

// New builds a collector from the "kubernetes" collector module config. The
// "paths" setting lists the files and directories holding YAML manifests; an
// entry may carry a repo_url, which is attached to the objects read from it.
func New(m config.Module) (*Collector, error) {
	raw, ok := m.Settings["paths"]
	if !ok {
		return nil, errors.New("kubernetes: paths is required")
	}
	sources, err := config.ParsePathSources(raw)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: paths: %w", err)
	}
	return &Collector{paths: sources, warnings: os.Stderr}, nil
}

// NewFromPaths builds a collector that reads the given files and directories
// as one cluster named "test".
func NewFromPaths(paths ...string) *Collector {
	sources := make([]config.PathSource, len(paths))
	for i, p := range paths {
		sources[i] = config.PathSource{Path: p, Cluster: "test"}
	}
	return &Collector{paths: sources, warnings: os.Stderr}
}

// WithWarnings sends warnings about skipped files, skipped documents and empty
// paths to w instead of standard error.
func (c *Collector) WithWarnings(w io.Writer) *Collector {
	c.warnings = w
	return c
}

// Name returns the source name.
func (*Collector) Name() string { return Source }

// Collect reads the manifests and adds their entities and relations to g.
// Unreadable files, undecodable documents and duplicates are skipped with a
// warning. Nothing is added when reading or mapping fails, so a partial view never
// reaches the graph.
func (c *Collector) Collect(ctx context.Context, g *model.Graph) error {
	c.incomplete = nil
	r := &reporter{warn: c.warn}
	objects, clusters, err := readObjects(ctx, c.paths, r)
	c.incomplete = r.incomplete
	if err != nil {
		return err
	}
	b := newBuilder()
	if err := b.build(clusters, objects); err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}
	return b.Commit(g)
}

// Incomplete returns the reasons the last Collect may have missed objects: a
// skipped file or document, a Namespace without a name, or a configured path
// without any object. It is empty when the read was complete. Callers that
// derive a vanished object from absence must not do so for an incomplete
// source.
func (c *Collector) Incomplete() []string { return slices.Clone(c.incomplete) }

func (c *Collector) warn(msg string) {
	fmt.Fprintf(c.warnings, "warning: kubernetes: %s\n", msg) //nolint:errcheck // a failing warning stream must not stop the run
}
