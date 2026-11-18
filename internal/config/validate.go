package config

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"

	"github.com/JtheGunner/youtrack-docgen/internal/filter"
)

var moduleName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// settingKind is the expected shape of a module-specific setting.
type settingKind int

const (
	kindString settingKind = iota
	kindStringList
)

// moduleSpec describes what the core checks for a built-in module. Modules
// with another name (for example from a private overlay) are not checked
// beyond the common fields.
type moduleSpec struct {
	settings map[string]settingKind
	// required lists the keys that must be set: "url", "token" or a setting.
	required []string
}

var collectorSpecs = map[string]moduleSpec{
	"netbox":     {required: []string{"url", "token"}},
	"kubernetes": {settings: map[string]settingKind{"paths": kindStringList}, required: []string{"paths"}},
}

var sinkSpecs = map[string]moduleSpec{
	"youtrack": {settings: map[string]settingKind{"root_article": kindString}},
	"markdown": {settings: map[string]settingKind{"dir": kindString}, required: []string{"dir"}},
}

// validate returns every problem of a decoded configuration, in a stable order.
func (c *Config) validate() []string {
	var problems []string

	if m, ok := c.Sinks["youtrack"]; ok && m.IsEnabled() {
		problems = append(problems, c.validateYouTrack()...)
	} else if c.YouTrack.URL != "" && !validURL(c.YouTrack.URL) {
		problems = append(problems, "youtrack.url: "+urlHint)
	}

	if _, err := filter.NewRedactor(c.Redact); err != nil {
		problems = append(problems, "redact: "+err.Error())
	}

	problems = append(problems, validateModules("collectors", c.Collectors, collectorSpecs)...)
	problems = append(problems, validateModules("sinks", c.Sinks, sinkSpecs)...)

	if len(c.EnabledCollectors()) == 0 {
		problems = append(problems, "at least one collector must be enabled")
	}
	if len(c.EnabledSinks()) == 0 {
		problems = append(problems, "at least one sink must be enabled")
	}
	return problems
}

const urlHint = "must be an http(s) URL with a host"

func (c *Config) validateYouTrack() []string {
	var problems []string
	switch {
	case c.YouTrack.URL == "":
		problems = append(problems, "youtrack.url: required")
	case !validURL(c.YouTrack.URL):
		problems = append(problems, "youtrack.url: "+urlHint)
	}
	if !c.YouTrack.Token.IsSet() {
		problems = append(problems, "youtrack.token: required")
	}
	if c.YouTrack.Project == "" {
		problems = append(problems, "youtrack.project: required")
	}
	if c.YouTrack.MaxStaleRatio < 0 || c.YouTrack.MaxStaleRatio > 1 {
		problems = append(problems, "youtrack.max_stale_ratio: must be between 0 and 1")
	}
	return problems
}

func validateModules(section string, modules map[string]Module, specs map[string]moduleSpec) []string {
	var problems []string
	for _, name := range slices.Sorted(maps.Keys(modules)) {
		m := modules[name]
		path := joinPath(section, name)

		if !moduleName.MatchString(name) {
			problems = append(problems, path+`: invalid module name (use lowercase letters, digits, "-" and "_", starting with a letter)`)
			continue
		}
		if !m.IsEnabled() {
			continue
		}
		if m.URL != "" && !validURL(m.URL) {
			problems = append(problems, path+".url: "+urlHint)
		}
		problems = append(problems, validateMatches(path+".allow", m.Allow)...)
		problems = append(problems, validateMatches(path+".deny", m.Deny)...)
		if spec, builtin := specs[name]; builtin {
			problems = append(problems, validateBuiltin(path, m, spec)...)
		}
	}
	return problems
}

func validateMatches(path string, matches []filter.Match) []string {
	var problems []string
	for i, m := range matches {
		if err := m.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("%s[%d]: %v", path, i, err))
		}
	}
	return problems
}

func validateBuiltin(path string, m Module, spec moduleSpec) []string {
	var problems []string

	for _, key := range slices.Sorted(maps.Keys(m.Settings)) {
		kind, known := spec.settings[key]
		if !known {
			problems = append(problems, fmt.Sprintf("%s: unknown setting %q", path, key))
			continue
		}
		if msg := checkSetting(m.Settings[key], kind); msg != "" {
			problems = append(problems, fmt.Sprintf("%s.%s: %s", path, key, msg))
		}
	}

	for _, key := range spec.required {
		if !isSet(m, key) && !hasInvalidSetting(m, key, spec) {
			problems = append(problems, fmt.Sprintf("%s.%s: required", path, key))
		}
	}
	return problems
}

// checkSetting returns a problem text when v does not have the expected shape.
func checkSetting(v any, kind settingKind) string {
	switch kind {
	case kindString:
		if s, ok := v.(string); !ok || s == "" {
			return "must be a non-empty string"
		}
	case kindStringList:
		list, ok := v.([]any)
		if !ok {
			return "must be a list of non-empty strings"
		}
		for _, item := range list {
			if s, ok := item.(string); !ok || s == "" {
				return "must be a list of non-empty strings"
			}
		}
	}
	return ""
}

// isSet reports whether the required key has a usable value.
func isSet(m Module, key string) bool {
	switch key {
	case "url":
		return m.URL != ""
	case "token":
		return m.Token.IsSet()
	}
	v, ok := m.Settings[key]
	if !ok {
		return false
	}
	if list, isList := v.([]any); isList {
		return len(list) > 0
	}
	return v != nil && v != ""
}

// hasInvalidSetting reports whether key is present but malformed, so it is
// reported once as invalid and not again as missing.
func hasInvalidSetting(m Module, key string, spec moduleSpec) bool {
	kind, known := spec.settings[key]
	if !known {
		return false
	}
	v, present := m.Settings[key]
	return present && checkSetting(v, kind) != ""
}

func validURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
