package filter

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/JtheGunner/youtrackt-docgen/internal/model"
)

// Obviously fake secrets seeded into the fixtures.
const (
	fakePassword = "fake-password-not-real-123"
	fakeBearer   = "Bearer FAKEFAKEFAKEtoken0000"
	fakeAWS      = "AKIAFAKEFAKEFAKEFAKE"
	fakePEM      = "-----BEGIN RSA PRIVATE KEY-----\nFAKEFAKEFAKEKEYBODY\n-----END RSA PRIVATE KEY-----"
	fakeEnv      = "fake-env-value-not-real"
	fakeSecret   = "ZmFrZS1zZWNyZXQ="
	fakeAnnot    = "fake-annotation-credential"
	fakeCustom   = "CORP-FAKE-9999"
)

func entity(t *testing.T, source string, kind model.Kind, key string, labels, attrs map[string]string) model.Entity {
	t.Helper()
	id, err := model.NewID(source, kind, key)
	if err != nil {
		t.Fatal(err)
	}
	return model.Entity{ID: id, Kind: kind, Name: key, Source: source, Labels: labels, Attrs: attrs}
}

func TestRulesPermits(t *testing.T) {
	host := entity(t, "netbox", model.KindHost, "web01", map[string]string{
		LabelTag: "prod, public", LabelRole: "Web", LabelTenant: "acme", "env": "production",
	}, nil)
	ns := entity(t, "k8s", model.KindNamespace, "kube-system", nil, nil)
	pod := entity(t, "k8s", model.KindWorkload, "api", map[string]string{LabelNamespace: "team-a"}, nil)

	tests := []struct {
		name   string
		rules  Rules
		entity model.Entity
		want   bool
	}{
		{"no rules pass everything", Rules{}, host, true},
		{"deny by kind", Rules{Deny: []Match{{Kinds: []string{"host"}}}}, host, false},
		{"deny by tag in list", Rules{Deny: []Match{{Tags: []string{"public"}}}}, host, false},
		{"deny by role case-insensitive", Rules{Deny: []Match{{Roles: []string{"web"}}}}, host, false},
		{"deny by tenant glob", Rules{Deny: []Match{{Tenants: []string{"ac*"}}}}, host, false},
		{"deny by label", Rules{Deny: []Match{{Labels: map[string]string{"env": "prod*"}}}}, host, false},
		{"deny by namespace", Rules{Deny: []Match{{Namespaces: []string{"team-a"}}}}, pod, false},
		{"namespace entity matches its own name", Rules{Deny: []Match{{Namespaces: []string{"kube-*"}}}}, ns, false},
		{"deny requires all fields", Rules{Deny: []Match{{Kinds: []string{"host"}, Roles: []string{"db"}}}}, host, true},
		{"allow miss excludes", Rules{Allow: []Match{{Tenants: []string{"other"}}}}, host, false},
		{"allow hit includes", Rules{Allow: []Match{{Tenants: []string{"acme"}}}}, host, true},
		{"deny beats allow", Rules{
			Allow: []Match{{Kinds: []string{"host"}}},
			Deny:  []Match{{Tags: []string{"prod"}}},
		}, host, false},
		{"empty match denies nothing", Rules{Deny: []Match{{}}}, host, true},
		{"empty match allows nothing", Rules{Allow: []Match{{}}}, host, false},
		{"missing label does not match", Rules{Deny: []Match{{Labels: map[string]string{"absent": "*"}}}}, host, true},
		{"malformed glob never matches", Rules{Deny: []Match{{Kinds: []string{"[host"}}}}, host, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rules.permits(tt.entity); got != tt.want {
				t.Fatalf("permits = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRedactorText(t *testing.T) {
	r, err := NewRedactor(RedactConfig{DenyPatterns: []string{`CORP-[A-Z]+-\d+`}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		in     string
		secret string
	}{
		{"assignment", "login with password=" + fakePassword + " now", fakePassword},
		{"colon assignment", "api_key: " + fakePassword, fakePassword},
		{"bearer header", "Authorization: " + fakeBearer, "FAKEFAKEFAKEtoken0000"},
		{"aws key", "key " + fakeAWS + " leaked", fakeAWS},
		{"pem block", "before\n" + fakePEM + "\nafter", "FAKEFAKEFAKEKEYBODY"},
		{"jwt", "t=eyJfakefakeAAA.eyJfakefakeBBB.fakefakeCCCCCC", "eyJfakefakeAAA"},
		{"github token", "ghp_FAKEFAKEFAKEFAKE0000", "FAKEFAKEFAKEFAKE0000"},
		{"youtrack token", "perm:ZmFrZQ==.NDI=.fakefakefake", "fakefakefake"},
		{"custom pattern", "see " + fakeCustom, fakeCustom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Text(tt.in)
			if strings.Contains(got, tt.secret) {
				t.Fatalf("secret survived: %q", got)
			}
			if !strings.Contains(got, Placeholder) {
				t.Fatalf("no placeholder in %q", got)
			}
		})
	}

	t.Run("plain text untouched", func(t *testing.T) {
		in := "Runs the public website on 192.0.2.10"
		if got := r.Text(in); got != in {
			t.Fatalf("got %q", got)
		}
	})
}

func TestRedactorTextQuotedKeys(t *testing.T) {
	r, err := NewRedactor(RedactConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]string{
		"json":         `{"password": "` + fakePassword + `"}`,
		"json compact": `{"db":{"token":"` + fakePassword + `"}}`,
		"single quote": `{'token': '` + fakePassword + `'}`,
		"yaml quoted":  `"api_key": ` + fakePassword,
	} {
		t.Run(name, func(t *testing.T) {
			if got := r.Text(in); strings.Contains(got, fakePassword) {
				t.Fatalf("secret survived: %q", got)
			}
		})
	}
}

func TestRedactorSecretKeys(t *testing.T) {
	r, err := NewRedactor(RedactConfig{SecretKeys: []string{`^internal-.*`}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		key  string
		want bool
	}{
		{"annotation.example.com/token", true},
		{"annotation.example.com/auth", true},
		{"annotation.example.com/auth-token", true},
		{"annotation.nginx.ingress/auth-secret", true},
		{"annotation.example.com/Authorization", true},
		{"annotation.kubectl.kubernetes.io/last-applied-configuration", true},
		{"env.ANYTHING", true},
		{"internal-notes", true},
		{"oauth", true},
		{"basicauth", true},
		{"authkey", true},
		{"authKey", true},
		{"proxyAuth", true},
		{"authn", true},
		{"authz", true},
		{"auths", true},
		{"auth", true},
		{"authorization", true},
		{"x-auth-token", true},
		{"author", false},
		{"Author", false},
		{"authors", false},
		{"authority", false},
		{"authoritative", false},
		{"annotation.example.com/author", false},
		{"annotation.example.com/author-name", false},
		{"annotation.authorKey", false},
		{"annotation.author", false},
		{"annotation.authors", false},
		{"annotation.example.com/authority", false},
		{"annotation.example.com/owner", false},
		{"description", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := r.secretKey(tt.key); got != tt.want {
				t.Fatalf("secretKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

// TestApplyIsTheRedactionEntryPoint pins the pipeline contract: every entity
// that leaves Apply has gone through the redactor, even when no rule is
// configured. Sink-level tests for renderers and sinks build on this.
func TestApplyIsTheRedactionEntryPoint(t *testing.T) {
	f, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	g := model.NewGraph()
	e := entity(t, "k8s", model.KindWorkload, "api", nil,
		map[string]string{"env.TOKEN": fakeEnv, "note": "password=" + fakePassword})
	if err := g.AddEntity(e); err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := out.Entity(e.ID)
	for k, v := range got.Attrs {
		if strings.Contains(v, fakeEnv) || strings.Contains(v, fakePassword) {
			t.Errorf("attr %q leaked: %q", k, v)
		}
	}
}

func TestMatchValidate(t *testing.T) {
	tests := []struct {
		name    string
		match   Match
		wantErr bool
	}{
		{"valid", Match{Kinds: []string{"host"}, Labels: map[string]string{"a": "b*"}}, false},
		{"empty", Match{}, true},
		{"bad kind glob", Match{Kinds: []string{"[x"}}, true},
		{"bad label glob", Match{Labels: map[string]string{"a": "[x"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.match.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewRejectsInvalidPatterns(t *testing.T) {
	for name, cfg := range map[string]RedactConfig{
		"deny pattern": {DenyPatterns: []string{"("}},
		"secret key":   {SecretKeys: []string{"("}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(Config{Redact: cfg}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func seededGraph(t *testing.T) *model.Graph {
	t.Helper()
	g := model.NewGraph()
	add := func(e model.Entity) model.Entity {
		t.Helper()
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	web := entity(t, "netbox", model.KindHost, "web01",
		map[string]string{LabelTag: "prod", LabelTenant: "acme", "note": "token=" + fakePassword},
		map[string]string{"description": "uses " + fakeBearer + " and " + fakeAWS, "pem": fakePEM})
	web.Aliases = []string{"web.example.com", "password=" + fakePassword}
	web.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.10")}
	web = add(web)

	hidden := add(entity(t, "netbox", model.KindHost, "lab01", map[string]string{LabelTenant: "lab"}, nil))

	api := add(entity(t, "k8s", model.KindWorkload, "api",
		map[string]string{LabelNamespace: "team-a"},
		map[string]string{
			"env.DB_HOST":                  fakeEnv,
			"env.PLAIN":                    fakeEnv,
			"secret.password":              fakeSecret,
			"secretdata.tls.key":           fakeSecret,
			"annotation.example.com/token": fakeAnnot,
			"annotation.example.com/owner": "team-a",
			"annotation.note":              "contact; secret: " + fakePassword,
			"annotation.kubectl.kubernetes.io/last-applied-configuration": "{\"x\":\"harmless\"}",
		}))
	kube := add(entity(t, "k8s", model.KindNamespace, "kube-system", nil, nil))

	for _, r := range []model.Relation{
		{From: api.ID, To: web.ID, Kind: model.RelDependsOn},
		{From: api.ID, To: kube.ID, Kind: model.RelMemberOf},
		{From: hidden.ID, To: web.ID, Kind: model.RelDependsOn},
	} {
		if err := g.AddRelation(r); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func TestApplyFiltersAndRedacts(t *testing.T) {
	f, err := New(Config{
		Collectors: map[string]Rules{
			"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}},
			"k8s":    {Deny: []Match{{Namespaces: []string{"kube-*"}}}},
		},
		Redact: RedactConfig{DenyPatterns: []string{`CORP-[A-Z]+-\d+`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	in := seededGraph(t)
	out, err := f.Apply(in)
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	var dump strings.Builder
	for _, kind := range allKinds {
		for _, e := range out.ByKind(kind) {
			ids = append(ids, string(e.ID))
			fmt.Fprintf(&dump, "%+v\n", e)
		}
	}
	slices.Sort(ids)
	want := []string{"k8s:workload:api", "netbox:host:web01"}
	if !slices.Equal(ids, want) {
		t.Fatalf("entities = %v, want %v", ids, want)
	}
	if rels := out.Relations(); len(rels) != 1 || rels[0].To != "netbox:host:web01" {
		t.Fatalf("relations = %v", rels)
	}

	for _, secret := range []string{
		fakePassword, "FAKEFAKEFAKEtoken0000", fakeAWS, "FAKEFAKEFAKEKEYBODY",
		fakeEnv, fakeSecret, fakeAnnot, fakeCustom,
	} {
		if strings.Contains(dump.String(), secret) {
			t.Errorf("secret %q reached the output:\n%s", secret, dump.String())
		}
	}

	api, _ := out.Entity("k8s:workload:api")
	if api.Attrs["annotation.example.com/owner"] != "team-a" {
		t.Errorf("harmless annotation lost: %v", api.Attrs)
	}
	if api.Attrs["env.DB_HOST"] != Placeholder {
		t.Errorf("env value not redacted: %v", api.Attrs)
	}
	web, _ := out.Entity("netbox:host:web01")
	if web.Addresses[0] != netip.MustParseAddr("192.0.2.10") || web.Aliases[0] != "web.example.com" {
		t.Errorf("harmless data changed: %+v", web)
	}

	// The input graph must stay untouched.
	orig, _ := in.Entity("k8s:workload:api")
	if orig.Attrs["env.DB_HOST"] != fakeEnv {
		t.Error("Apply mutated the input graph")
	}
}

func TestApplyWithoutRulesKeepsEverything(t *testing.T) {
	f, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(seededGraph(t))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(out.ByKind(model.KindHost)) + len(out.ByKind(model.KindWorkload)) + len(out.ByKind(model.KindNamespace)); n != 4 {
		t.Fatalf("entities = %d, want 4", n)
	}
	if n := len(out.Relations()); n != 3 {
		t.Fatalf("relations = %d, want 3", n)
	}
}

func TestAllKindsCoversEveryKind(t *testing.T) {
	g := model.NewGraph()
	for _, kind := range allKinds {
		if err := g.AddEntity(entity(t, "s", kind, "x", nil, nil)); err != nil {
			t.Fatal(err)
		}
	}
	f, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range allKinds {
		if len(out.ByKind(kind)) != 1 {
			t.Errorf("kind %s lost", kind)
		}
	}
}
