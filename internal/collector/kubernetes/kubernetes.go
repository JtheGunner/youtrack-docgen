// Package kubernetes reads workloads, services, ingresses and namespaces from
// Kubernetes manifests on disk (Git checkouts or rendered Kustomize/Helm
// output) into the neutral model. It never talks to a cluster and never maps
// Secret manifests, environment values or annotations.
package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/JtheGunner/youtrack-docgen/internal/config"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// Source is the source name used in every entity ID created by this collector.
const Source = "kubernetes"

// Collector implements collector.Collector for Kubernetes manifests.
type Collector struct {
	paths    []string
	warnings io.Writer
}

// New builds a collector from the "kubernetes" collector module config. The
// "paths" setting lists the files and directories holding YAML manifests.
func New(m config.Module) (*Collector, error) {
	raw, ok := m.Settings["paths"]
	if !ok {
		return nil, errors.New("kubernetes: paths is required")
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, errors.New("kubernetes: paths must be a non-empty list of strings")
	}
	paths := make([]string, 0, len(list))
	for _, item := range list {
		p, ok := item.(string)
		if !ok || p == "" {
			return nil, errors.New("kubernetes: paths must be a non-empty list of strings")
		}
		paths = append(paths, p)
	}
	return NewFromPaths(paths...), nil
}

// NewFromPaths builds a collector that reads the given files and directories.
func NewFromPaths(paths ...string) *Collector {
	return &Collector{paths: paths, warnings: os.Stderr}
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
	objects, err := readObjects(ctx, c.paths, c.warn)
	if err != nil {
		return err
	}
	b := newBuilder()
	if err := b.build(objects); err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}
	return b.commit(g)
}

func (c *Collector) warn(msg string) {
	fmt.Fprintf(c.warnings, "warning: kubernetes: %s\n", msg) //nolint:errcheck // a failing warning stream must not stop the run
}
