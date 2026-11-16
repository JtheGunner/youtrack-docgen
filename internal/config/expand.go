package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// LookupFunc reports the value of an environment variable.
type LookupFunc func(name string) (value string, ok bool)

// expandString replaces ${VAR} and ${VAR:-default} references in s.
//
//   - ${VAR} needs VAR to be set and non-empty, otherwise it is a problem.
//   - ${VAR:-default} uses default when VAR is unset or empty. The default ends
//     at the first "}".
//   - "$$" yields a literal "$"; a "$" before anything else stays as it is.
//
// All problems are returned, never only the first. Problem texts name the
// variable but never contain a value.
func expandString(s string, lookup LookupFunc) (string, []string) {
	var (
		out      strings.Builder
		problems []string
	)
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			out.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '$':
			out.WriteByte('$')
			i++
		case '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				problems = append(problems, fmt.Sprintf("unterminated variable reference in %q", s[i:]))
				return out.String(), problems
			}
			value, problem := expandReference(s[i+2:i+end], lookup)
			if problem != "" {
				problems = append(problems, problem)
			}
			out.WriteString(value)
			i += end
		default:
			out.WriteByte('$')
		}
	}
	return out.String(), problems
}

// expandReference resolves the text between "${" and "}".
func expandReference(body string, lookup LookupFunc) (value, problem string) {
	name, fallback, hasFallback := strings.Cut(body, ":-")
	if !validVariableName(name) {
		return "", fmt.Sprintf("invalid variable name %q", name)
	}
	if v, ok := lookup(name); ok && v != "" {
		return v, ""
	}
	if hasFallback {
		return fallback, ""
	}
	return "", fmt.Sprintf("environment variable %s is not set or empty", name)
}

// expandNode expands the string scalars below n in place and returns the
// problems found, each prefixed with the config path of the offending value.
// Map keys and comments are never expanded.
func expandNode(n *yaml.Node, path string, env LookupFunc) []string {
	var problems []string
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			problems = append(problems, expandNode(c, path, env)...)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			child := joinPath(path, n.Content[i].Value)
			problems = append(problems, expandNode(n.Content[i+1], child, env)...)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			problems = append(problems, expandNode(c, fmt.Sprintf("%s[%d]", path, i), env)...)
		}
	case yaml.ScalarNode:
		if n.ShortTag() != "!!str" || !strings.Contains(n.Value, "$") {
			break
		}
		value, probs := expandString(n.Value, env)
		for _, p := range probs {
			problems = append(problems, path+": "+p)
		}
		n.Value = value
		if n.Style == 0 {
			// A plain scalar is resolved again, so "${ON:-true}" becomes a bool.
			n.Tag = ""
		}
	}
	return problems
}

// joinPath appends key to a config path, quoting keys that are not plain words.
func joinPath(base, key string) string {
	if !simpleKey.MatchString(key) {
		key = strconv.Quote(key)
	}
	if base == "" {
		return key
	}
	return base + "." + key
}

var simpleKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validVariableName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		letter := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		digit := r >= '0' && r <= '9'
		if !letter && (i == 0 || !digit) {
			return false
		}
	}
	return true
}
