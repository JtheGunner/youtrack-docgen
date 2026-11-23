package filter

import (
	"strings"
	"testing"
)

// A URL with credentials is redacted when its separators are written in an
// escaped form: percent-encoded (also twice), HTML entities, or with several
// backslashes from nested JSON.
func TestRedactorURLCredentialsWithEscapedSeparators(t *testing.T) {
	r, err := NewRedactor(RedactConfig{})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "s3cr3t-fake-value-42"
	tests := []struct {
		name string
		in   string
	}{
		{"percent encoded slashes", "dsn https:%2F%2Fadmin:" + secret + "@db.example.com/app"},
		{"lower case percent encoding", "dsn https:%2f%2fadmin:" + secret + "@db.example.com/app"},
		{"twice percent encoded slashes", "dsn https:%252F%252Fadmin:" + secret + "@db.example.com/app"},
		{"decimal entity slashes", "dsn https:&#47;&#47;admin:" + secret + "@db.example.com/app"},
		{"hex entity slashes", "dsn https:&#x2F;&#x2F;admin:" + secret + "@db.example.com/app"},
		{"named entity slashes", "dsn https:&sol;&sol;admin:" + secret + "@db.example.com/app"},
		{"doubly escaped json slashes", `dsn https:\\/\\/admin:` + secret + `@db.example.com/app`},
		{"mixed slash forms", "dsn https:%2F/admin:" + secret + "@db.example.com/app"},
		{"encoded colon in userinfo", "dsn https://admin%3A" + secret + "@db.example.com/app"},
		{"entity colon in userinfo", "dsn https://admin&#58;" + secret + "@db.example.com/app"},
		{"encoded at sign", "dsn https://admin:" + secret + "%40db.example.com/app"},
		{"entity at sign", "dsn https://admin:" + secret + "&#64;db.example.com/app"},
		{"encoded scheme colon", "dsn https%3A%2F%2Fadmin:" + secret + "@db.example.com/app"},
		{"json unicode escapes", `dsn https:\u002f\u002fadmin:` + secret + `@db.example.com/app`},
		{"json unicode escaped colon and at", `dsn https://admin\u003a` + secret + `\u0040db.example.com/app`},
		{"percent encoded slash in user name", "dsn https://org%2Fuser:" + secret + "@db.example.com/app"},
		{"entity slash in user name", "dsn https://org&#47;user:" + secret + "@db.example.com/app"},
		{"hex entity slash in user name", "dsn https://org&#x2F;user:" + secret + "@db.example.com/app"},
		{"json unicode slash in user name", `dsn https://org\u002fuser:` + secret + `@db.example.com/app`},
		{"escaped scheme with encoded slash in user name", "dsn https:%2F%2Forg%2Fuser:" + secret + "@db.example.com/app"},
		{"everything encoded", "dsn https%3A%2F%2Fadmin%3A" + secret + "%40db.example.com%2Fapp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Text(tt.in)
			if strings.Contains(got, secret) {
				t.Fatalf("the secret survived in %q", got)
			}
			if !strings.Contains(got, Placeholder) {
				t.Fatalf("no placeholder in %q", got)
			}
		})
	}
}

// Escaped separators in text without credentials stay readable.
func TestRedactorKeepsEscapedURLsWithoutCredentials(t *testing.T) {
	r, err := NewRedactor(RedactConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{
		"see https:%2F%2Fdocs.example.com%2Fpage",
		"see https:&#47;&#47;docs.example.com/page",
		"see https:%2F%2Fdocs.example.com:8443%2Fpage",
		"mail to ops%40example.com",
	} {
		if got := r.Text(in); got != in {
			t.Errorf("Text(%q) = %q, want it unchanged", in, got)
		}
	}
}
