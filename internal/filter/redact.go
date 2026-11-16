package filter

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/JtheGunner/youtrackt-docgen/internal/model"
)

// Placeholder replaces every removed secret value.
const Placeholder = "[REDACTED]"

// Attribute key prefixes collectors use for sensitive data (container env
// values, Kubernetes Secret data). Values below them are always redacted,
// whatever they contain. Kubernetes annotations use the "annotation." prefix
// and are redacted when their key looks secret.
var alwaysRedactedPrefixes = []string{"env.", "secret.", "secretdata."}

// Built-in patterns. The text patterns apply to every free-text value; the key
// pattern decides which attributes, labels and annotations are secret by name.
var (
	defaultKeyPattern = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|private[_-]?key|credential|auth)`)

	defaultTextPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`),
		regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`),
		regexp.MustCompile(`\b(glpat|ghp|gho|ghs|xox[abp])[-_][A-Za-z0-9_-]{10,}`),
		regexp.MustCompile(`\bperm:[A-Za-z0-9._=-]{10,}`),
		regexp.MustCompile(`(?i)\b[\w.-]*(password|passwd|secret|token|api[_-]?key|private[_-]?key)[\w.-]*\s*[=:]\s*("[^"]*"|'[^']*'|\S+)`),
	}
)

// RedactConfig tunes redaction. The built-in patterns always apply; the lists
// here only add to them.
type RedactConfig struct {
	// DenyPatterns are extra regular expressions removed from free text.
	DenyPatterns []string `yaml:"deny_patterns"`
	// SecretKeys are extra regular expressions; attributes and annotations
	// whose key matches have their value redacted.
	SecretKeys []string `yaml:"secret_keys"`
}

// Redactor removes secrets from entities and free text.
type Redactor struct {
	text []*regexp.Regexp
	keys []*regexp.Regexp
}

// NewRedactor compiles the built-in and configured patterns.
func NewRedactor(cfg RedactConfig) (*Redactor, error) {
	r := &Redactor{
		text: append([]*regexp.Regexp(nil), defaultTextPatterns...),
		keys: []*regexp.Regexp{defaultKeyPattern},
	}
	for _, p := range cfg.DenyPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("filter: invalid deny pattern %q: %w", p, err)
		}
		r.text = append(r.text, re)
	}
	for _, p := range cfg.SecretKeys {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("filter: invalid secret key pattern %q: %w", p, err)
		}
		r.keys = append(r.keys, re)
	}
	return r, nil
}

// Text replaces every secret-looking fragment of s with Placeholder.
func (r *Redactor) Text(s string) string {
	for _, re := range r.text {
		s = re.ReplaceAllString(s, Placeholder)
	}
	return s
}

// secretKey reports whether a value stored under key must be redacted
// regardless of its content.
func (r *Redactor) secretKey(key string) bool {
	lower := strings.ToLower(key)
	for _, prefix := range alwaysRedactedPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for _, re := range r.keys {
		if re.MatchString(key) {
			return true
		}
	}
	return false
}

// Entity returns a redacted copy of e. The ID and name stay untouched so
// references remain stable; labels, attributes and aliases are scrubbed.
func (r *Redactor) Entity(e model.Entity) model.Entity {
	e.Labels = r.scrub(e.Labels)
	e.Attrs = r.scrub(e.Attrs)
	if e.Aliases != nil {
		aliases := make([]string, len(e.Aliases))
		for i, a := range e.Aliases {
			aliases[i] = r.Text(a)
		}
		e.Aliases = aliases
	}
	return e
}

func (r *Redactor) scrub(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if r.secretKey(k) {
			out[k] = Placeholder
			continue
		}
		out[k] = r.Text(v)
	}
	return out
}
