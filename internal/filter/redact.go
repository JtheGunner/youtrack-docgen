package filter

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/JtheGunner/youtrack-docgen/model"
)

// Placeholder replaces every removed secret value.
const Placeholder = "[REDACTED]"

// Attribute key prefixes collectors use for sensitive data (container env
// values, Kubernetes Secret data). Values below them are always redacted,
// whatever they contain. Kubernetes annotations use the "annotation." prefix
// and are redacted when their key looks secret.
var alwaysRedactedPrefixes = []string{"env.", "secret.", "secretdata."}

// alwaysRedactedKeyParts mark attributes that embed whole manifests, such as the
// kubectl last-applied annotation, which can contain Secret data or env values.
var alwaysRedactedKeyParts = []string{"last-applied-configuration"}

// Built-in patterns. The text patterns apply to every free-text value; the key
// pattern decides which attributes, labels and annotations are secret by name.
var (
	defaultKeyPattern = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|private[_-]?key|credential|passphrase|pwd)`)

	// benignAuthWords are the only words starting with "auth" that are not
	// credentials. Longer words come first so that "authority" is not
	// shortened to "author".
	benignAuthWords = []string{"authoritative", "authority", "authors", "author"}

	defaultTextPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`),
		regexp.MustCompile(`(?i)\bauthorization["']?\s*[=:]\s*["']?basic\s+[A-Za-z0-9+/._~-]+=*`),
		regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`),
		regexp.MustCompile(`\b(glpat|ghp|gho|ghs|ghu|ghr|github_pat|xox[abp])[-_][A-Za-z0-9_-]{10,}`),
		regexp.MustCompile(`\bperm:[A-Za-z0-9._=-]{10,}`),
		regexp.MustCompile(`(?i)["']?\b[\w.-]*(password|passwd|passphrase|pwd|secret|token|api[_-]?key|private[_-]?key)[\w.-]*["']?\s*[=:]\s*("[^"]*"|'[^']*'|\S+)`),
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
	s = redactURLCredentials(s)
	for _, re := range r.text {
		s = re.ReplaceAllString(s, Placeholder)
	}
	return s
}

// secretKey reports whether a value stored under key must be redacted
// regardless of its content.
func (r *Redactor) secretKey(key string) bool {
	if !isASCII(key) || containsAuth(key) {
		return true
	}
	lower := asciiLower(key)
	for _, prefix := range alwaysRedactedPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for _, part := range alwaysRedactedKeyParts {
		if strings.Contains(lower, part) {
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

// IdentityChanged reports whether the redactor would change the name or the ID
// key of e. Both are rendered as article title, table cell and diagram label,
// and the key is part of every reference, so such an entity cannot be masked:
// the filter removes it.
func (r *Redactor) IdentityChanged(e model.Entity) bool {
	if r.Text(e.Name) != e.Name {
		return true
	}
	_, _, key, err := model.ParseID(e.ID)
	return err != nil || r.Text(key) != key
}

// Entity returns a redacted copy of e. The ID and name stay untouched so
// references remain stable (an entity whose identity the redactor would change
// is removed by the filter, see IdentityChanged); labels, attributes and
// aliases are scrubbed.
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

// isASCII reports whether every byte of key is below utf8.RuneSelf.
func isASCII(key string) bool {
	for i := 0; i < len(key); i++ {
		if key[i] >= 0x80 {
			return false
		}
	}
	return true
}

// asciiLower folds only A-Z, so the result has the same length as key and
// byte offsets stay valid for both strings.
func asciiLower(key string) string {
	b := []byte(key)
	for i, c := range b {
		if isUpper(c) {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// containsAuth fails closed: "auth" anywhere in key (oauth, basicauth, authKey,
// proxyAuth, authn, ...) marks it secret, and so does any "authoriz" / "authoris"
// spelling. Only an occurrence that is a whole benign word is exempt: it must
// start and end at a word boundary. key must be ASCII.
func containsAuth(key string) bool {
	lower := asciiLower(key)
	if strings.Contains(lower, "authoriz") || strings.Contains(lower, "authoris") {
		return true
	}
	for from := 0; ; {
		i := strings.Index(lower[from:], "auth")
		if i < 0 {
			return false
		}
		i += from
		if !isBenignAuthWord(key, lower, i) {
			return true
		}
		from = i + len("auth")
	}
}

func isBenignAuthWord(key, lower string, start int) bool {
	if !startsWord(key, start) {
		return false
	}
	for _, word := range benignAuthWords {
		if strings.HasPrefix(lower[start:], word) && endsWord(key, start+len(word)) {
			return true
		}
	}
	return false
}

func isLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
func isUpper(b byte) bool  { return b >= 'A' && b <= 'Z' }
func isLower(b byte) bool  { return b >= 'a' && b <= 'z' }

// startsWord reports whether a word begins at i: start of key, a non-letter
// before it, or an upper-case letter directly after a lower-case one. A run of
// capitals is never split, so AUTHORKEY is one word.
func startsWord(key string, i int) bool {
	if i == 0 {
		return true
	}
	prev := key[i-1]
	return !isLetter(prev) || (isUpper(key[i]) && isLower(prev))
}

// endsWord reports whether a word ends before index i: end of key, a non-letter,
// or an upper-case letter directly after a lower-case one (camelCase hump).
func endsWord(key string, i int) bool {
	if i >= len(key) {
		return true
	}
	return !isLetter(key[i]) || (isUpper(key[i]) && isLower(key[i-1]))
}
