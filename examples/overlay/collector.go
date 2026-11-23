package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"

	"github.com/JtheGunner/youtrack-docgen/collector"
	"github.com/JtheGunner/youtrack-docgen/model"
)

// Source is the source name used in every entity ID of this collector and the
// key of its block below "collectors" in docgen.yaml.
const Source = "inventory"

type inventoryCollector struct{ path string }

// newFromSettings is the collector.Factory: it reads the "path" option of the
// collector block.
func newFromSettings(s collector.Settings) (collector.Collector, error) {
	path, ok := s.Options["path"].(string)
	if !ok || path == "" {
		return nil, errors.New("inventory: option \"path\" must be a non-empty string")
	}
	return &inventoryCollector{path: path}, nil
}

func (*inventoryCollector) Name() string { return Source }

type record struct {
	Hostname string   `json:"hostname"`
	Address  string   `json:"address"`
	Role     string   `json:"role"`
	Aliases  []string `json:"aliases"`
}

func (c *inventoryCollector) Collect(ctx context.Context, g *model.Graph) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("inventory: %w", err)
	}
	var records []record
	if err := json.Unmarshal(raw, &records); err != nil {
		return fmt.Errorf("inventory: parse %s: %w", c.path, err)
	}

	hosts := make([]model.Entity, 0, len(records))
	for _, r := range records {
		id, err := model.NewID(Source, model.KindHost, r.Hostname)
		if err != nil {
			return fmt.Errorf("inventory: %w", err)
		}
		addr, err := netip.ParseAddr(r.Address)
		if err != nil {
			return fmt.Errorf("inventory: host %s: %w", r.Hostname, err)
		}
		hosts = append(hosts, model.Entity{
			ID:        id,
			Kind:      model.KindHost,
			Name:      r.Hostname,
			Source:    Source,
			Labels:    map[string]string{"role": r.Role},
			Aliases:   r.Aliases,
			Addresses: []netip.Addr{addr},
		})
	}

	// Add to the graph only after everything was read and validated.
	for _, h := range hosts {
		if err := g.AddEntity(h); err != nil {
			return fmt.Errorf("inventory: %w", err)
		}
	}
	return nil
}
