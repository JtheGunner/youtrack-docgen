package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestExpandString(t *testing.T) {
	env := map[string]string{
		"HOST":  "example.com",
		"EMPTY": "",
		"TOKEN": "fake-token",
	}
	lookup := func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}

	tests := []struct {
		name         string
		in           string
		want         string
		wantProblems []string
	}{
		{name: "no variables", in: "plain text", want: "plain text"},
		{name: "single variable", in: "https://${HOST}/api", want: "https://example.com/api"},
		{name: "two variables", in: "${HOST}:${TOKEN}", want: "example.com:fake-token"},
		{name: "default unused when set", in: "${HOST:-fallback}", want: "example.com"},
		{name: "default used when unset", in: "${MISSING:-fallback}", want: "fallback"},
		{name: "default used when empty", in: "${EMPTY:-fallback}", want: "fallback"},
		{name: "empty default", in: "a${MISSING:-}b", want: "ab"},
		{name: "default may contain colon and dash", in: "${MISSING:-a:-b}", want: "a:-b"},
		{name: "escaped dollar", in: "cost: $$5", want: "cost: $5"},
		{name: "escaped variable", in: "$${HOST}", want: "${HOST}"},
		{name: "lone dollar is literal", in: "a$b", want: "a$b"},
		{name: "trailing dollar is literal", in: "a$", want: "a$"},
		{
			name:         "missing variable",
			in:           "${MISSING}",
			want:         "",
			wantProblems: []string{"environment variable MISSING is not set or empty"},
		},
		{
			name:         "empty variable counts as missing",
			in:           "${EMPTY}",
			wantProblems: []string{"environment variable EMPTY is not set or empty"},
		},
		{
			name: "all missing variables are reported",
			in:   "${A}-${HOST}-${B}",
			wantProblems: []string{
				"environment variable A is not set or empty",
				"environment variable B is not set or empty",
			},
		},
		{
			name:         "unterminated reference",
			in:           "${HOST",
			wantProblems: []string{`unterminated variable reference in "${HOST"`},
		},
		{
			name:         "invalid variable name",
			in:           "${1BAD}",
			wantProblems: []string{`invalid variable name "1BAD"`},
		},
		{
			name:         "empty variable name",
			in:           "${}",
			wantProblems: []string{`invalid variable name ""`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problems := expandString(tt.in, lookup)
			if !reflect.DeepEqual(problems, tt.wantProblems) {
				t.Fatalf("problems = %v, want %v", problems, tt.wantProblems)
			}
			if len(tt.wantProblems) == 0 && got != tt.want {
				t.Errorf("expandString(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExpandStringProblemsNeverContainValues(t *testing.T) {
	lookup := func(name string) (string, bool) { return "super-secret-value", name == "SET" }
	_, problems := expandString("${SET}${MISSING}", lookup)
	for _, p := range problems {
		if p == "" || strings.Contains(p, "super-secret-value") {
			t.Errorf("problem leaks a value: %q", p)
		}
	}
}
