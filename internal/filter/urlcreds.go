package filter

import (
	"regexp"
	"strings"
	"unicode"
)

var urlSchemeRE = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://`)

// redactURLCredentials replaces the scheme and the user:password part of every
// URL that carries credentials, up to and including the "@" that ends the
// userinfo; the host stays readable. "://" is followed by a run of
// non-whitespace characters, and the credentials end at the last "@" that
// still belongs to the userinfo.
//
// The userinfo may itself contain "@" and "/", so the end is found with a
// host heuristic: a host is plausible when it has a dot, a port, or is
// localhost or a bracketed IPv6 literal. While the text after a candidate "@"
// is not a plausible host and another "@" follows, the credentials extend to
// that "@" (fail closed).
//
// Known limitations, both of which err on the side of redacting:
//   - "scheme://user:pw@host/path" with a single-label host and no port is
//     indistinguishable from the password "pw@host" followed by a path, so
//     everything after the "@" is removed. Give the host a dot or a port.
//   - "scheme://host:8443/path/a@b" with a single-label host such as "host"
//     is indistinguishable from the user "host" and the password "8443/path/a",
//     so it is redacted; with a dotted host or localhost it is left alone.
//
// A user name that contains a dot and a password that starts with digits
// followed by "/" ("user.name:12/x@host") looks like host and port and is not
// redacted (known limitation).
func redactURLCredentials(s string) string {
	locs := urlSchemeRE.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	copied := 0
	for _, loc := range locs {
		if loc[0] < copied {
			continue // inside an already redacted URL
		}
		run := s[loc[1]:]
		if i := strings.IndexFunc(run, unicode.IsSpace); i >= 0 {
			run = run[:i]
		}
		end := credentialsEnd(run)
		if end < 0 {
			continue
		}
		b.WriteString(s[copied:loc[0]])
		b.WriteString(Placeholder)
		copied = loc[1] + end
	}
	if copied == 0 {
		return s
	}
	b.WriteString(s[copied:])
	return b.String()
}

// credentialsEnd returns the length of the credentials prefix of run (the text
// after "://", up to whitespace), or -1 when run carries none.
func credentialsEnd(run string) int {
	authority := run
	if slash := strings.IndexByte(run, '/'); slash >= 0 {
		authority = run[:slash]
	}
	var at int
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		if !strings.Contains(authority[:i], ":") {
			return -1 // a user name without password
		}
		at = i
	} else {
		colon := strings.IndexByte(authority, ':')
		if colon < 0 || strings.HasPrefix(authority, "[") {
			return -1
		}
		before, after := authority[:colon], authority[colon+1:]
		if plausibleHostName(before) && (after == "" || isDigits(after)) {
			return -1 // host and port
		}
		next := strings.IndexByte(run[len(authority):], '@')
		if next < 0 {
			return -1
		}
		at = len(authority) + next
	}
	extended := false
	for {
		rest := run[at+1:]
		host, _, hasPath := strings.Cut(rest, "/")
		// An "@" inside the host segment is more userinfo, never part of a host.
		if next := strings.LastIndexByte(host, '@'); next >= 0 {
			at += 1 + next
			extended = true
			continue
		}
		if plausibleHost(host) {
			break
		}
		if next := strings.IndexByte(rest, '@'); next >= 0 {
			at += 1 + next
			extended = true
			continue
		}
		if !extended && hasPath {
			return len(run)
		}
		break
	}
	return at + 1
}

func plausibleHostName(s string) bool {
	return strings.Contains(s, ".") || strings.EqualFold(s, "localhost")
}

// plausibleHost accepts a host with a dot, a port, or a bracketed IPv6 literal.
func plausibleHost(s string) bool {
	if strings.HasPrefix(s, "[") {
		return true
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 && isDigits(s[i+1:]) {
		return true
	}
	return plausibleHostName(s)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
