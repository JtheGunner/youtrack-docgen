package config

import (
	"fmt"
	"maps"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/internal/filter"
	"github.com/JtheGunner/youtrack-docgen/model"
)

var moduleName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// settingKind is the expected shape of a module-specific setting.
type settingKind int

const (
	kindString settingKind = iota
	kindStringList
	kindPathSources
)

// moduleSpec describes what the core checks for a built-in module. Modules
// with another name (for example from a private overlay) are not checked
// beyond the common fields.
type moduleSpec struct {
	settings map[string]settingKind
	// required lists the keys that must be set: "url", "token" or a setting.
	required []string
	// usesURL and usesToken tell whether the module reads its own url and
	// token; a value for a module that does not is reported as ineffective.
	usesURL, usesToken bool
	// match describes which rule fields a collector provides; nil when the
	// core does not know the entities of the module.
	match *matchSpec
}

// matchSpec lists what the entities of one collector carry, so rule fields it
// never fills are caught instead of silently never matching.
type matchSpec struct {
	kinds      []model.Kind
	tags       bool
	roles      bool
	tenants    bool
	namespaces bool
	// labelKey reports whether the collector can emit a label with this key.
	labelKey func(string) bool
}

var collectorSpecs = map[string]moduleSpec{
	"netbox": {
		required: []string{"url", "token"}, usesURL: true, usesToken: true,
		match: &matchSpec{
			kinds: []model.Kind{model.KindSite, model.KindNetwork, model.KindIPAddress, model.KindHost, model.KindInterface},
			tags:  true, roles: true, tenants: true,
			labelKey: func(k string) bool {
				return k == filter.LabelRole || k == filter.LabelTenant || strings.HasPrefix(k, filter.LabelTagPrefix)
			},
		},
	},
	"kubernetes": {
		settings: map[string]settingKind{"paths": kindPathSources}, required: []string{"paths"},
		match: &matchSpec{
			kinds:      []model.Kind{model.KindCluster, model.KindHost, model.KindNamespace, model.KindWorkload, model.KindService, model.KindIngress},
			namespaces: true,
			labelKey: func(k string) bool {
				return k == filter.LabelNamespace || strings.HasPrefix(k, "label.")
			},
		},
	},
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

	if !c.DiagramFormat().Valid() {
		problems = append(problems, "diagrams: must be \"text\" or \"mermaid\"")
	}

	if _, err := filter.NewRedactor(c.Redact); err != nil {
		problems = append(problems, "redact: "+err.Error())
	}

	problems = append(problems, validateModules("collectors", c.Collectors, collectorSpecs, c.AllowInsecureHTTP)...)
	problems = append(problems, validateModules("sinks", c.Sinks, sinkSpecs, c.AllowInsecureHTTP)...)

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
	case !c.AllowInsecureHTTP && c.YouTrack.Token.IsSet() && insecureTokenURL(c.YouTrack.URL):
		problems = append(problems, "youtrack.url: "+insecureHint)
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

func validateModules(section string, modules map[string]Module, specs map[string]moduleSpec, allowInsecure bool) []string {
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
		spec, builtin := specs[name]
		problems = append(problems, validateConnection(path, m, spec, builtin, allowInsecure)...)
		if section == "collectors" {
			problems = append(problems, validateMatches(path+".allow", m.Allow, spec.match)...)
			problems = append(problems, validateMatches(path+".deny", m.Deny, spec.match)...)
		} else if len(m.Allow) > 0 || len(m.Deny) > 0 {
			problems = append(problems, path+".allow/deny: have no effect on a sink; rules apply to collectors only")
		}
		if builtin {
			problems = append(problems, validateBuiltin(path, m, spec)...)
		}
	}
	return problems
}

// validateConnection checks url and token. A built-in module that does not
// read them reports a set value as ineffective.
func validateConnection(modPath string, m Module, spec moduleSpec, builtin, allowInsecure bool) []string {
	var problems []string
	switch {
	case builtin && !spec.usesURL && m.URL != "":
		problems = append(problems, modPath+".url: has no effect; this module does not use it")
	case m.URL != "" && !validURL(m.URL):
		problems = append(problems, modPath+".url: "+urlHint)
	case !allowInsecure && m.Token.IsSet() && insecureTokenURL(m.URL):
		problems = append(problems, modPath+".url: "+insecureHint)
	}
	if builtin && !spec.usesToken && m.Token.IsSet() {
		problems = append(problems, modPath+".token: has no effect; this module does not use it")
	}
	return problems
}

// validateMatches checks every rule. spec is nil for modules whose entities
// the core does not know; then only the kinds are checked against the model.
func validateMatches(rulePath string, matches []filter.Match, spec *matchSpec) []string {
	var problems []string
	for i, m := range matches {
		if err := m.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("%s[%d]: %v", rulePath, i, err))
			continue
		}
		for _, msg := range checkMatchFields(m, spec) {
			problems = append(problems, fmt.Sprintf("%s[%d]: %s", rulePath, i, msg))
		}
	}
	return problems
}

// checkMatchFields reports the fields of m that can never match.
func checkMatchFields(m filter.Match, spec *matchSpec) []string {
	var problems []string
	valid := filter.AllKinds()
	if spec != nil {
		valid = spec.kinds
	}
	for _, pattern := range m.Kinds {
		if !slices.ContainsFunc(valid, func(k model.Kind) bool { return globMatches(pattern, string(k)) }) {
			problems = append(problems, fmt.Sprintf("kinds: %q matches no kind; valid kinds: %s", pattern, kindList(valid)))
		}
	}
	if spec == nil {
		return problems
	}
	check := func(field string, provided bool, used bool) {
		if used && !provided {
			problems = append(problems, field+": this collector does not provide it, so the rule can never match")
		}
	}
	check("tags", spec.tags, len(m.Tags) > 0)
	check("roles", spec.roles, len(m.Roles) > 0)
	check("tenants", spec.tenants, len(m.Tenants) > 0)
	check("namespaces", spec.namespaces, len(m.Namespaces) > 0)
	for _, key := range slices.Sorted(maps.Keys(m.Labels)) {
		if !spec.labelKey(key) {
			problems = append(problems, fmt.Sprintf("labels: this collector never sets a label %q, so the rule can never match", key))
		}
	}
	return problems
}

func kindList(kinds []model.Kind) string {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// globMatches compares case-insensitively, like the filter does.
func globMatches(pattern, value string) bool {
	ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(value))
	return err == nil && ok
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
	case kindPathSources:
		if _, err := ParsePathSources(v); err != nil {
			return err.Error()
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

const insecureHint = "uses http:// and would send the token unencrypted; use https:// or set allow_insecure_http: true (loopback hosts such as localhost are always allowed)"

// insecureTokenURL reports whether raw is a plain-http URL on a non-loopback
// host. Such a URL must not receive a token unless explicitly allowed.
func insecureTokenURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err != nil || !ip.IsLoopback()
}

func validURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
