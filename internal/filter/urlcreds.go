package filter

import (
	"regexp"
	"strings"
	"unicode"
)

var urlSchemeRE = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://`)

// redactURLCredentials replaces every URL that may carry credentials with the
// placeholder, from its scheme to the end of the whitespace-delimited token.
//
// A password may itself contain "@" and "/", so the end of the userinfo cannot
// be told reliably from the host. The rule therefore fails closed: a URL is
// redacted as a whole when the authority (the text after "://" up to the first
// "/") contains a ":" and the token contains an "@" after the scheme. That
// covers "user:password@host", passwords containing "@" or "/", and a missing
// user name.
//
// Known limitations:
//   - Redacting too much is the accepted failure: "scheme://host:8443/a@b"
//     (a port followed by a path containing "@") loses the whole URL.
//   - A URL without a ":" in its authority ("scheme://user@host/path") has no
//     password and stays readable.
//   - Unencoded whitespace inside a password ends the token, so only the part
//     before the whitespace is covered. RFC 3986 requires such characters to be
//     percent-encoded.
func redactURLCredentials(s string) string {
	locs := urlSchemeRE.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	copied := 0
	tokenEnd, lastAt := -1, -1 // of the token holding the current scheme, as absolute offsets
	for _, loc := range locs {
		if loc[0] < copied {
			continue // inside an already redacted URL
		}
		if loc[1] >= tokenEnd {
			tokenEnd, lastAt = tokenBounds(s, loc[1])
		}
		if lastAt < loc[1] || !authorityHasColon(s[loc[1]:tokenEnd]) {
			continue
		}
		b.WriteString(s[copied:loc[0]])
		b.WriteString(Placeholder)
		copied = tokenEnd
	}
	if copied == 0 {
		return s
	}
	b.WriteString(s[copied:])
	return b.String()
}

// tokenBounds returns the end of the whitespace-delimited token that holds
// offset from, and the offset of the last "@" at or after from (-1 when there
// is none).
// Scanning once per token keeps the redaction linear when a token holds many
// "scheme://" occurrences.
func tokenBounds(s string, from int) (end, lastAt int) {
	end = len(s)
	if i := strings.IndexFunc(s[from:], unicode.IsSpace); i >= 0 {
		end = from + i
	}
	lastAt = -1
	if i := strings.LastIndexByte(s[from:end], '@'); i >= 0 {
		lastAt = from + i
	}
	return end, lastAt
}

// authorityHasColon reports whether the text up to the first "/" holds a ":".
func authorityHasColon(run string) bool {
	if slash := strings.IndexByte(run, '/'); slash >= 0 {
		run = run[:slash]
	}
	return strings.Contains(run, ":")
}
