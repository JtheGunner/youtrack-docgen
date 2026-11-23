package cli

import (
	"fmt"
	"maps"
	"slices"

	"github.com/JtheGunner/youtrack-docgen/collector"
	"github.com/JtheGunner/youtrack-docgen/internal/collector/kubernetes"
	"github.com/JtheGunner/youtrack-docgen/internal/collector/netbox"
	"github.com/JtheGunner/youtrack-docgen/internal/config"
)

// BuiltinCollectors returns the factories of the collectors that ship with
// youtrack-docgen, keyed by their name. Every call returns a new map.
func BuiltinCollectors() map[string]collector.Factory {
	return map[string]collector.Factory{
		netbox.Source: func(s collector.Settings) (collector.Collector, error) {
			return netbox.New(moduleFromSettings(s))
		},
		kubernetes.Source: func(s collector.Settings) (collector.Collector, error) {
			return kubernetes.New(moduleFromSettings(s))
		},
	}
}

// settingsFromModule hands a collector block to a factory. Allow and deny
// rules stay behind: the filter applies them.
func settingsFromModule(m config.Module) collector.Settings {
	return collector.Settings{
		URL:     m.URL,
		Token:   collector.NewSecret(m.Token.Reveal()),
		Options: maps.Clone(m.Settings),
	}
}

// moduleFromSettings is the inverse of settingsFromModule for the built-in
// collectors, whose constructors take a config.Module.
func moduleFromSettings(s collector.Settings) config.Module {
	return config.Module{
		URL:      s.URL,
		Token:    config.Secret(s.Token.Value()),
		Settings: s.Options,
	}
}

// buildCollectors builds every enabled collector through the factory
// registered under its name.
func buildCollectors(cfg *config.Config, factories map[string]collector.Factory) ([]collector.Collector, error) {
	var out []collector.Collector
	for _, name := range cfg.EnabledCollectors() {
		factory, ok := factories[name]
		if !ok {
			return nil, fmt.Errorf("collector %q is not registered (registered: %s); disable it with enabled: false",
				name, names(slices.Sorted(maps.Keys(factories))))
		}
		c, err := factory(settingsFromModule(cfg.Collectors[name]))
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, fmt.Errorf("collector %q: factory returned no collector", name)
		}
		if c.Name() != name {
			return nil, fmt.Errorf("collector registered as %q reports the name %q", name, c.Name())
		}
		out = append(out, c)
	}
	return out, nil
}
