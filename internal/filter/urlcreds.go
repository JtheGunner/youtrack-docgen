package filter

import (
	"regexp"
	"strings"
	"unicode"
)

// The separators of a URL can be written in escaped forms: percent-encoded
// (also more than once), as HTML entities, as JSON unicode escapes, or with
// backslashes from JSON that was embedded one or more times. The redaction
// matches these forms wherever it looks for the plain character.
const (
	slashPattern = `(?:\\*/|\\+u002[Ff]|%(?:25)*2[Ff]|&#(?:47|[xX]0*2[Ff]);|&sol;)`
	colonPattern = `(?::|\\+u003[Aa]|%(?:25)*3[Aa]|&#(?:58|[xX]0*3[Aa]);|&colon;)`
	atPattern    = `(?:@|\\+u0040|%(?:25)*40|&#(?:64|[xX]0*40);|&commat;)`
)

// The scheme needs no word boundary before it: "x_https://" and "1https://"
// must not hide credentials. Escaped separators count as well.
var (
	urlSchemeRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*` + colonPattern + slashPattern + slashPattern)
	slashRE     = regexp.MustCompile(slashPattern)
	colonRE     = regexp.MustCompile(colonPattern)
	atRE        = regexp.MustCompile(atPattern)
)

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
//   - Separators in a form that the patterns above do not list (for example
//     HTML entities with leading zeros, or an unusual charset) are not
//     recognised.
//   - A scheme-relative URL ("//user:password@host") has no scheme to find and
//     is not redacted.
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
// offset from, and the offset of the last "@" (in any escaped form) at or after
// from (-1 when there is none).
// Scanning once per token keeps the redaction linear when a token holds many
// "scheme://" occurrences.
func tokenBounds(s string, from int) (end, lastAt int) {
	end = len(s)
	if i := strings.IndexFunc(s[from:], unicode.IsSpace); i >= 0 {
		end = from + i
	}
	lastAt = -1
	if all := atRE.FindAllStringIndex(s[from:end], -1); len(all) > 0 {
		lastAt = from + all[len(all)-1][0]
	}
	return end, lastAt
}

// authorityHasColon reports whether the text up to the first "/" (in any
// escaped form) holds a ":" (in any escaped form).
func authorityHasColon(run string) bool {
	if loc := slashRE.FindStringIndex(run); loc != nil {
		run = run[:loc[0]]
	}
	return colonRE.MatchString(run)
}
