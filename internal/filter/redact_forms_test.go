package filter

import (
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

func TestRedactorTextCredentialForms(t *testing.T) {
	r, err := NewRedactor(RedactConfig{})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "s3cr3t-fake-value-42"
	leaks := []string{secret, "dXNlcjpmYWtlLXNlY3JldA==", "11FAKEFAKE0", "FAKEFAKEFAKEFAKE0000"}
	tests := []struct {
		name string
		in   string
		keep string // text that must survive; empty for none
	}{
		{"https userinfo", "see https://admin:" + secret + "@db.example.com/app", ""},
		{"postgres userinfo", "dsn postgres://app:" + secret + "@192.0.2.5:5432/main?sslmode=disable", ""},
		{"userinfo with at sign in password", "https://admin:" + secret + "@x@db.example.com/", ""},
		{"e-mail address as user name", "https://ops@example.com:" + secret + "@db.example.com/x", ""},
		{"slash in password", "https://user:" + secret + "/ss@db.example.com/x", ""},
		{"slash in password after letters", "https://user:pa/" + secret + "@db.example.com/x", ""},
		{"digits then slash in password", "https://user:1a/" + secret + "@db.example.com/x", ""},
		{"basic authorization header", "Authorization: Basic dXNlcjpmYWtlLXNlY3JldA== end", "end"},
		{"basic authorization lower case", "authorization=basic " + secret, ""},
		{"quoted basic authorization", `"Authorization": "Basic ` + secret + `"`, ""},
		{"pwd equals", "connect pwd=" + secret + " now", "now"},
		{"pwd colon", "pwd: " + secret, ""},
		{"prefixed pwd", "db_pwd=" + secret, ""},
		{"passphrase colon", "passphrase: " + secret, ""},
		{"quoted passphrase", `passphrase = "` + secret + ` with spaces"`, ""},
		{"github fine grained token", "token github_pat_11FAKEFAKE0_" + secret, ""},
		{"github user token", "ghu_FAKEFAKEFAKEFAKE0000", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Text(tt.in)
			for _, leaked := range leaks {
				if strings.Contains(got, leaked) {
					t.Fatalf("%q survived in %q", leaked, got)
				}
			}
			if !strings.Contains(got, Placeholder) {
				t.Fatalf("no placeholder in %q", got)
			}
			if tt.keep != "" && !strings.Contains(got, tt.keep) {
				t.Fatalf("%q was over-redacted: %q", tt.keep, got)
			}
		})
	}

	for _, in := range []string{
		"https://example.com:8443/path",
		"ssh://git@example.com/repo.git",
		"contact ops@example.com",
		"https://example.com/path:foo/a@b",
		"https://ops@example.com/",
		"Basic configuration is described on https://example.com/basic",
		"Authorization header is set by the proxy",
		"the working directory is pwd, see cwd: /app",
		"passphrases are rotated yearly",
		"github_pat is the prefix",
		"sslmode: require",
	} {
		t.Run("untouched/"+in, func(t *testing.T) {
			if got := r.Text(in); got != in {
				t.Fatalf("over-redacted %q to %q", in, got)
			}
		})
	}
}

func TestSecretKeysCoverPwdAndPassphrase(t *testing.T) {
	r, err := NewRedactor(RedactConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"pwd", "db.pwd", "ssh-passphrase"} {
		if !r.secretKey(key) {
			t.Errorf("key %q is not secret", key)
		}
	}
}

// An entity whose name or key the redactor would change is removed, together
// with its relations; masking would still leak through keys and references.
func TestApplyRemovesEntitiesWhoseIdentityIsRedacted(t *testing.T) {
	const secret = "fake-identity-secret-77"
	g := model.NewGraph()
	keep := entity(t, "k8s", model.KindWorkload, "api", nil, nil)
	byName := entity(t, "k8s", model.KindWorkload, "plain-key", nil, nil)
	byName.Name = "password=" + secret
	byKey := entity(t, "k8s", model.KindWorkload, "api_key="+secret, nil, nil)
	byKey.Name = "harmless"
	for _, e := range []model.Entity{keep, byName, byKey} {
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddRelation(model.Relation{From: keep.ID, To: byName.ID, Kind: model.RelMemberOf}); err != nil {
		t.Fatal(err)
	}
	f, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	if ids := out.IDs(); len(ids) != 1 || ids[0] != keep.ID {
		t.Fatalf("IDs = %v, want only %s", ids, keep.ID)
	}
	if rels := out.Relations(); len(rels) != 0 {
		t.Errorf("relations to removed entities survived: %v", rels)
	}
}

// A URL whose authority holds a ":" and whose token holds an "@" is redacted
// as a whole, whatever the password contains. Redacting the host too is the
// accepted failure.
func TestRedactorURLCredentialsFailClosed(t *testing.T) {
	r, err := NewRedactor(RedactConfig{})
	if err != nil {
		t.Fatal(err)
	}
	redacted := []struct{ name, in, want string }{
		{"at and slash in password", "https://user:p@ss/word@host/x", Placeholder},
		{"password with at and slash, dotted host after", "https://u:abc@def.ghi/jkl@host.example.com/", Placeholder},
		{"slash then at in password", "https://user:pa/ss@word", Placeholder},
		{"at then slash, no later at", "https://user:p@ss/word", Placeholder},
		{"two ats and a slash", "https://user:a@b/c@d/e@db.example.com/x", Placeholder},
		{"digits then slash", "https://user:12/abc@db.example.com/x", Placeholder},
		{"user name with dot and digits then slash", "https://user.name:12/x@host", Placeholder},
		{"empty user name", "https://:fakesecret@db.example.com/", Placeholder},
		{"single label host", "https://user:pw@host/path", Placeholder},
		{"port followed by a path with at", "https://host.example.com:8443/path/a@b", Placeholder},
		{"surrounding text", "see (https://user:pa/ss@word) and more", "see ([REDACTED] and more"},
		{"surrounding text without closing", "a https://u1:pw1@h1.example.com/ b", "a [REDACTED] b"},
		{"two urls", "a https://u1:pw1@h1/ b https://u2:p/w2@h2/ c", "a [REDACTED] b [REDACTED] c"},
		{"scheme glued to an underscore", "x_https://user:pw@host.example.com/", "x_" + Placeholder},
		{"scheme glued to a digit", "1https://user:pw@host.example.com/", "1" + Placeholder},
		{"json escaped slashes", `{"u":"https:\/\/user:pw@host.example.com\/x"}`, `{"u":"` + Placeholder},
		{"url inside a query string", "https://example.com/?next=ftp://u:pw@h.example.com", "https://example.com/?next=[REDACTED]"},
	}
	for _, tt := range redacted {
		t.Run("redacted/"+tt.name, func(t *testing.T) {
			if got := r.Text(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	for _, in := range []string{
		"https://example.com:8443/path",
		"http://localhost:8080/path",
		"ssh://git@host",
		"ssh://git@host.example.com/repo.git",
		"https://ops@example.com/",
		"https://db.example.com/x/y@abc@z",
		"https://example.com/a@b/c:d",
		"https://[2001:db8::1]:8443/path",
		"mail ops@example.com and https://example.com/",
	} {
		t.Run("untouched/"+in, func(t *testing.T) {
			if got := r.Text(in); got != in {
				t.Fatalf("over-redacted %q to %q", in, got)
			}
		})
	}
}

// Many "scheme://" occurrences in one long token must not make the scan
// quadratic.
func TestRedactURLCredentialsIsLinearOnHostileInput(t *testing.T) {
	in := strings.Repeat("a://", 200000) + "x"
	start := time.Now()
	_ = redactURLCredentials(in)
	_ = redactURLCredentials(strings.Repeat("a://b:c ", 100000))
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("scan took %v", d)
	}
}
