package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/JtheGunner/youtrack-docgen/internal/filter"
)

// Config is the parsed and validated docgen.yaml.
type Config struct {
	// YouTrack holds the connection to the YouTrack instance.
	YouTrack YouTrack `yaml:"youtrack"`
	// Collectors maps a module name to its configuration.
	Collectors map[string]Module `yaml:"collectors"`
	// Redact tunes secret redaction on top of the built-in patterns.
	Redact filter.RedactConfig `yaml:"redact"`
	// Sinks maps a module name to its configuration.
	Sinks map[string]Module `yaml:"sinks"`
	// AllowInsecureHTTP permits http:// URLs on non-loopback hosts for
	// endpoints that receive a token. Loopback hosts are always allowed.
	AllowInsecureHTTP bool `yaml:"allow_insecure_http"`
}

// YouTrack is the connection to the YouTrack instance.
type YouTrack struct {
	URL     string `yaml:"url"`
	Token   Secret `yaml:"token"`
	Project string `yaml:"project"`
	// MaxStaleRatio is the largest share of the managed articles one run may
	// mark as stale, between 0 and 1. Unset means the sink default (0.5).
	MaxStaleRatio float64 `yaml:"max_stale_ratio"`
}

// Module configures one collector or sink. Every module has its own block;
// keys the core does not know are kept in Settings for the module to read.
type Module struct {
	// Enabled defaults to true when omitted.
	Enabled *bool  `yaml:"enabled"`
	URL     string `yaml:"url"`
	Token   Secret `yaml:"token"`
	// Allow and Deny are allow/deny rules for the entities of a collector; see
	// filter.Match. Only collectors use them.
	Allow []filter.Match `yaml:"allow"`
	Deny  []filter.Match `yaml:"deny"`
	// Settings holds the module-specific keys.
	Settings map[string]any `yaml:",inline"`
}

// Filter builds the pipeline filter from the per-collector rules and the
// redaction settings. Rules are keyed by collector name, which must equal the
// source of the entities the collector emits.
func (c *Config) Filter() (*filter.Filter, error) {
	cfg := filter.Config{Collectors: map[string]filter.Rules{}, Redact: c.Redact}
	for name, m := range c.Collectors {
		if m.IsEnabled() {
			cfg.Collectors[name] = filter.Rules{Allow: m.Allow, Deny: m.Deny}
		}
	}
	return filter.New(cfg)
}

// IsEnabled reports whether the module is active.
func (m Module) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

// EnabledCollectors returns the names of the active collectors, sorted.
func (c *Config) EnabledCollectors() []string { return enabledNames(c.Collectors) }

// EnabledSinks returns the names of the active sinks, sorted.
func (c *Config) EnabledSinks() []string { return enabledNames(c.Sinks) }

func enabledNames(modules map[string]Module) []string {
	var names []string
	for name, m := range modules {
		if m.IsEnabled() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// ValidationError lists every problem found in a configuration.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "invalid config (%d problem(s)):", len(e.Problems))
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}

// Load reads, expands and validates the config file at path. Environment
// variables are taken from the process environment.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data, os.LookupEnv)
}

var yamlLinePrefix = regexp.MustCompile(`^line \d+: `)

// Parse expands environment references in data, decodes it strictly and
// validates the result. A failure is always a *ValidationError.
func Parse(data []byte, env LookupFunc) (*Config, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, &ValidationError{Problems: []string{"cannot parse config: " + stripLine(err.Error())}}
	}
	if doc.Kind == 0 {
		return nil, &ValidationError{Problems: []string{"config is empty"}}
	}

	problems := expandNode(&doc, "", env)

	expanded, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("re-encode config: %w", err)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		var typeErr *yaml.TypeError
		if errors.As(err, &typeErr) {
			for _, msg := range typeErr.Errors {
				problems = append(problems, redactDecodeError(stripLine(msg)))
			}
		} else {
			problems = append(problems, redactDecodeError(stripLine(err.Error())))
		}
	}
	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}

	if problems := cfg.validate(); len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return &cfg, nil
}

// decodeValueError matches yaml.v3 type errors that quote the offending value,
// such as "cannot unmarshal !!str `fake-se...` into []string". The decoder runs
// after environment expansion, so that value can be (the start of) a secret.
// Matching up to the last " into " keeps a value that itself contains a
// backtick or " into " from slipping through.
var decodeValueError = regexp.MustCompile("(?s)^cannot unmarshal (!!\\w+) `.*` into (.+)$")

// redactDecodeError replaces a quoted value in a decoder message.
func redactDecodeError(msg string) string {
	return decodeValueError.ReplaceAllString(msg, "cannot unmarshal a $1 value into $2")
}

// stripLine drops YAML line numbers: they refer to the re-encoded document,
// not to the file the user wrote.
func stripLine(msg string) string { return yamlLinePrefix.ReplaceAllString(msg, "") }
