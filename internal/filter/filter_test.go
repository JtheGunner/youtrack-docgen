package filter

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/model"
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
		LabelTagPrefix + "prod": "true", LabelTagPrefix + "public": "true", LabelRole: "Web", LabelTenant: "acme", "env": "production",
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
		{"AUTHORIZATION", true},
		{"HTTP_AUTHORIZATION", true},
		{"AUTHORKEY", true},
		{"AUTHORISATION", true},
		{"Authorization", true},
		{"authorizationHeader", true},
		{"AUTH", true},
		{"OAUTH_TOKEN", true},
		{"ınternal-auth", true},
		{"İAUTHx", true},
		{"größe", true},
		{"author", false},
		{"AUTHOR", false},
		{"AUTHORS", false},
		{"AuthorKey", false},
		{"HTTP_AUTHOR", false},
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
		map[string]string{LabelTagPrefix + "prod": "true", LabelTenant: "acme", "note": "token=" + fakePassword},
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

func TestApplyDeniesSameAsTwinsAndDNSNamedIPs(t *testing.T) {
	const denied = "lab01.example.com"
	g := model.NewGraph()
	add := func(e model.Entity) model.Entity {
		t.Helper()
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	lab := add(entity(t, "netbox", model.KindHost, denied, map[string]string{LabelTenant: "lab"}, nil))
	twinHost := entity(t, "k8s", model.KindHost, "lab01-node", nil, nil)
	twinHost.Aliases = []string{denied}
	twin := add(twinHost)
	twinNIC := add(entity(t, "k8s", model.KindInterface, "lab01-node-eth0", nil, nil))
	web := add(entity(t, "netbox", model.KindHost, "web01.example.com", map[string]string{LabelTenant: "acme"}, nil))
	webNIC := add(entity(t, "netbox", model.KindInterface, "web01-eth0", nil, nil))
	twinOnly := add(entity(t, "k8s", model.KindIPAddress, "192.0.2.20", nil, nil))
	dnsOnly := add(entity(t, "netbox", model.KindIPAddress, "192.0.2.21", nil, map[string]string{"dns_name": "LAB01.example.com"}))
	dnsShared := add(entity(t, "netbox", model.KindIPAddress, "192.0.2.22", nil, map[string]string{"dns_name": denied}))
	for _, r := range []model.Relation{
		{From: twin.ID, To: lab.ID, Kind: model.RelSameAs}, // twin -> denied: either direction must work
		{From: twinNIC.ID, To: twin.ID, Kind: model.RelMemberOf},
		{From: webNIC.ID, To: web.ID, Kind: model.RelMemberOf},
		{From: twinOnly.ID, To: twinNIC.ID, Kind: model.RelMemberOf},
		{From: dnsShared.ID, To: webNIC.ID, Kind: model.RelMemberOf},
	} {
		if err := g.AddRelation(r); err != nil {
			t.Fatal(err)
		}
	}

	f, err := New(Config{Collectors: map[string]Rules{"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []model.ID{lab.ID, twin.ID, twinNIC.ID, twinOnly.ID, dnsOnly.ID} {
		if _, ok := out.Entity(id); ok {
			t.Errorf("%s should have been removed", id)
		}
	}
	for _, id := range []model.ID{web.ID, webNIC.ID, dnsShared.ID} {
		if _, ok := out.Entity(id); !ok {
			t.Errorf("%s should have survived", id)
		}
	}
	for _, kind := range allKinds {
		for _, e := range out.ByKind(kind) {
			if text := strings.ToLower(fmt.Sprintf("%+v", e)); strings.Contains(text, denied) || strings.Contains(text, "lab01") {
				t.Errorf("denied name leaked into %s: %+v", e.ID, e)
			}
		}
	}
}

func TestApplyHidesDeniedHostFromDependents(t *testing.T) {
	const denied = "lab01.example.com"
	g := model.NewGraph()
	add := func(e model.Entity) model.Entity {
		t.Helper()
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	lab := entity(t, "netbox", model.KindHost, denied, map[string]string{LabelTenant: "lab"}, nil)
	lab.Aliases = []string{"lab-alias.example.com"}
	lab = add(lab)
	web := add(entity(t, "netbox", model.KindHost, "web01.example.com", map[string]string{LabelTenant: "acme"}, nil))
	nicLab := add(entity(t, "netbox", model.KindInterface, "lab01-eth0", nil, nil))
	nicWeb := add(entity(t, "netbox", model.KindInterface, "web01-eth0", nil, nil))

	shared := entity(t, "netbox", model.KindIPAddress, "192.0.2.10",
		map[string]string{"dns": "LAB01.Example.COM", "other": "keep-me"},
		map[string]string{"description": "primary of Lab01.example.com and lab-alias.example.com"})
	shared.Aliases = []string{"Lab01.Example.Com", "lab-alias.example.com", "web.example.com"}
	shared = add(shared)
	only := add(entity(t, "netbox", model.KindIPAddress, "192.0.2.11", map[string]string{"dns": denied}, nil))
	unrelated := entity(t, "netbox", model.KindIPAddress, "192.0.2.12",
		map[string]string{"dns": "web01.example.com"}, map[string]string{"description": "no denied name"})
	unrelated.Aliases = []string{"web.example.com"}
	unrelated = add(unrelated)

	for _, r := range []model.Relation{
		{From: nicLab.ID, To: lab.ID, Kind: model.RelMemberOf},
		{From: nicWeb.ID, To: web.ID, Kind: model.RelMemberOf},
		{From: shared.ID, To: nicLab.ID, Kind: model.RelMemberOf},
		{From: shared.ID, To: nicWeb.ID, Kind: model.RelMemberOf},
		{From: only.ID, To: nicLab.ID, Kind: model.RelMemberOf},
		{From: unrelated.ID, To: nicWeb.ID, Kind: model.RelMemberOf},
	} {
		if err := g.AddRelation(r); err != nil {
			t.Fatal(err)
		}
	}

	f, err := New(Config{Collectors: map[string]Rules{"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		id         model.ID
		survives   bool
		wantAttrs  map[string]string
		wantLabels map[string]string
		wantAlias  []string
	}{
		{"denied host removed", lab.ID, false, nil, nil, nil},
		{"denied interface removed", nicLab.ID, false, nil, nil, nil},
		{"ip used only by denied host removed", only.ID, false, nil, nil, nil},
		{"surviving interface kept", nicWeb.ID, true, nil, nil, nil},
		{
			"shared ip kept and masked", shared.ID, true,
			map[string]string{"description": "primary of " + Placeholder + " and " + Placeholder},
			map[string]string{"dns": Placeholder, "other": "keep-me"},
			[]string{"web.example.com"},
		},
		{
			"unrelated ip untouched", unrelated.ID, true,
			map[string]string{"description": "no denied name"},
			map[string]string{"dns": "web01.example.com"},
			[]string{"web.example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := out.Entity(tt.id)
			if ok != tt.survives {
				t.Fatalf("survives = %v, want %v", ok, tt.survives)
			}
			if !ok || tt.wantAttrs == nil {
				return
			}
			if fmt.Sprint(got.Attrs) != fmt.Sprint(tt.wantAttrs) {
				t.Errorf("Attrs = %v, want %v", got.Attrs, tt.wantAttrs)
			}
			if fmt.Sprint(got.Labels) != fmt.Sprint(tt.wantLabels) {
				t.Errorf("Labels = %v, want %v", got.Labels, tt.wantLabels)
			}
			if !slices.Equal(got.Aliases, tt.wantAlias) {
				t.Errorf("Aliases = %v, want %v", got.Aliases, tt.wantAlias)
			}
		})
	}

	for _, kind := range allKinds {
		for _, e := range out.ByKind(kind) {
			text := strings.ToLower(fmt.Sprintf("%+v", e))
			if strings.Contains(text, denied) || strings.Contains(text, "lab-alias") {
				t.Errorf("denied name leaked into %s: %+v", e.ID, e)
			}
		}
	}

	// Masking must not mutate the input graph.
	in, _ := g.Entity(shared.ID)
	if in.Labels["dns"] != "LAB01.Example.COM" || len(in.Aliases) != 3 || !strings.Contains(in.Attrs["description"], "Lab01") {
		t.Errorf("input graph was mutated: %+v", in)
	}
}

func TestApplyScrubsDeniedHostFromAllEntityKinds(t *testing.T) {
	const (
		deniedName = "lab01.example.com"
		deniedAddr = "192.0.2.50"
		keptAddr   = "198.51.100.7"
	)
	g := model.NewGraph()
	add := func(e model.Entity) model.Entity {
		t.Helper()
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	lab := entity(t, "netbox", model.KindHost, deniedName, map[string]string{LabelTenant: "lab"}, nil)
	lab.Addresses = []netip.Addr{netip.MustParseAddr(deniedAddr)}
	lab = add(lab)

	ingress := entity(t, "k8s", model.KindIngress, "shop/web", nil, map[string]string{"hosts": "Lab01.Example.com,shop.example.com"})
	ingress.Aliases = []string{"LAB01.example.com", "shop.example.com"}
	ingress = add(ingress)

	service := entity(t, "k8s", model.KindService, "shop/edge", nil, map[string]string{"external_ips": deniedAddr + "," + keptAddr})
	service.Addresses = []netip.Addr{netip.MustParseAddr(deniedAddr), netip.MustParseAddr(keptAddr)}
	service = add(service)

	workload := add(entity(t, "k8s", model.KindWorkload, "shop/Deployment/api",
		map[string]string{"node": deniedName, "tier": "web"}, map[string]string{"note": "pinned to " + deniedName + " at " + deniedAddr}))

	otherHost := entity(t, "netbox", model.KindHost, "web01.example.com", map[string]string{LabelTenant: "acme"}, nil)
	otherHost.Aliases = []string{deniedName, "web.example.com"}
	otherHost = add(otherHost)

	f, err := New(Config{Collectors: map[string]Rules{"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := out.Entity(lab.ID); ok {
		t.Fatalf("%s should have been removed", lab.ID)
	}
	for _, e := range []model.Entity{ingress, service, workload, otherHost} {
		if _, ok := out.Entity(e.ID); !ok {
			t.Errorf("%s must be masked, not removed", e.ID)
		}
	}
	for _, kind := range allKinds {
		for _, e := range out.ByKind(kind) {
			text := strings.ToLower(fmt.Sprintf("%+v", e))
			if strings.Contains(text, deniedName) || strings.Contains(text, "lab01") || strings.Contains(text, deniedAddr) {
				t.Errorf("denied host leaked into %s: %+v", e.ID, e)
			}
		}
	}
	svc, _ := out.Entity(service.ID)
	if !slices.Contains(svc.Addresses, netip.MustParseAddr(keptAddr)) || len(svc.Addresses) != 1 {
		t.Errorf("service addresses = %v, want only %s", svc.Addresses, keptAddr)
	}
	if !strings.Contains(svc.Attrs["external_ips"], keptAddr) {
		t.Errorf("unrelated address was lost: %v", svc.Attrs)
	}
	wl, _ := out.Entity(workload.ID)
	if wl.Labels["tier"] != "web" {
		t.Errorf("unrelated label changed: %v", wl.Labels)
	}

	// Masking must not mutate the input graph.
	in, _ := g.Entity(ingress.ID)
	if !strings.Contains(in.Attrs["hosts"], "Lab01") || len(in.Aliases) != 2 {
		t.Errorf("input graph was mutated: %+v", in)
	}
}

func TestApplyAnchorsDeniedNamesAtHostnameBoundaries(t *testing.T) {
	g := model.NewGraph()
	add := func(e model.Entity) model.Entity {
		t.Helper()
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	add(entity(t, "netbox", model.KindHost, "db", map[string]string{LabelTenant: "lab"}, nil))
	svc := add(entity(t, "k8s", model.KindService, "shop/edge", nil,
		map[string]string{"upstream": "db2.example.com", "primary": "DB.example.com", "mongodb": "mongodb.example.com"}))

	f, err := New(Config{Collectors: map[string]Rules{"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out.Entity(svc.ID)
	if !ok {
		t.Fatal("service must survive")
	}
	want := map[string]string{
		"upstream": "db2.example.com",
		"primary":  Placeholder + ".example.com",
		"mongodb":  "mongodb.example.com",
	}
	if fmt.Sprint(got.Attrs) != fmt.Sprint(want) {
		t.Errorf("Attrs = %v, want %v", got.Attrs, want)
	}
}

func TestApplyScrubsDeniedAddressesInEveryTextForm(t *testing.T) {
	g := model.NewGraph()
	denied := entity(t, "netbox", model.KindHost, "lab01.example.com", map[string]string{LabelTenant: "lab"}, nil)
	denied.Addresses = []netip.Addr{
		netip.MustParseAddr("192.0.2.50"),
		netip.MustParseAddr("2001:db8::50"),
		netip.MustParseAddr("::ffff:198.51.100.9"),
	}
	if err := g.AddEntity(denied); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, in, want string }{
		{"ipv4 with port", "192.0.2.50:8080", Placeholder + ":8080"},
		{"url with port and path", "http://192.0.2.50:8080/x", "http://" + Placeholder + ":8080/x"},
		{"cidr", "192.0.2.50/32", Placeholder + "/32"},
		{"list", "192.0.2.50,192.0.2.50", Placeholder + "," + Placeholder},
		{"ipv4 mapped into ipv6", "::ffff:192.0.2.50", Placeholder},
		{"ipv4 mapped, hex form", "::ffff:c000:232", Placeholder},
		{"denied 4in6 address, hex form", "::FFFF:C633:6409", Placeholder},
		{"uppercase and non-canonical ipv6", "2001:DB8:0:0:0:0:0:50", Placeholder},
		{"partly compressed ipv6", "2001:db8:0:0::50", Placeholder},
		{"ipv6 with zone", "2001:db8::50%eth0", Placeholder + "%eth0"},
		{"trailing sentence dot", "reachable at 192.0.2.50.", "reachable at " + Placeholder + "."},
		{"bracketed ipv6 with port", "[2001:db8::50]:443", "[" + Placeholder + "]:443"},
		{"expanded ipv6", "2001:0db8:0000:0000:0000:0000:0000:0050", Placeholder},
		{"denied 4in6 address written as ipv4", "198.51.100.9:80", Placeholder + ":80"},
		{"hex letter right before an ipv4 address", "a192.0.2.50", "a" + Placeholder},
		// The last six zero groups and the address form the IPv4-compatible literal
		// ::192.0.2.50, which is replaced as a whole; hiding more is fine, leaking is not.
		{"oversized token with a trailing address", strings.Repeat("0:", 40) + "192.0.2.50", strings.Repeat("0:", 34) + Placeholder},
		{"oversized token with a leading address", "192.0.2.50:" + strings.Repeat("0:", 40), Placeholder + ":" + strings.Repeat("0:", 40)},
		{"nat64 embedding", "64:ff9b::c000:232", Placeholder},
		{"nat64 embedding in dotted form", "64:ff9b::192.0.2.50", Placeholder},
		{"ipv6 after a key and colon", "ip:2001:db8::50", "ip:" + Placeholder},
		{"ipv6 after a leading colon", ":2001:db8::50", ":" + Placeholder},
		{"ipv6 before a trailing colon", "2001:db8::50:", Placeholder + ":"},
		{"ipv6 between colons", "x:2001:db8::50:y", "x:" + Placeholder + ":y"},
		{"hex letter right before an ipv6 address", "a2001:db8::50", "a" + Placeholder},
		{"several hex digits before an ipv6 address", "cafe2001:db8::50", "cafe" + Placeholder},
		{"hex digits after an ipv6 address that is not a valid address", "2001:db8::50g", Placeholder + "g"},
		{"invalid longer ipv4 hides the denied part", "192.0.2.500", Placeholder + "0"},
		{"shorter ipv4 stays", "192.0.2.5", "192.0.2.5"},
		{"invalid number around an ipv4 address hides the denied part", "1192.0.2.50", "1" + Placeholder},
		{"a different valid ipv6 address stays", "2001:db8::50ab", "2001:db8::50ab"},
		{"longer ipv6 stays", "2001:db8::500", "2001:db8::500"},
	}
	attrs := make(map[string]string, len(tests))
	for i, tt := range tests {
		attrs[fmt.Sprintf("k%02d", i)] = tt.in
	}
	svc := entity(t, "k8s", model.KindService, "shop/edge", nil, attrs)
	if err := g.AddEntity(svc); err != nil {
		t.Fatal(err)
	}

	f, err := New(Config{Collectors: map[string]Rules{"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out.Entity(svc.ID)
	if !ok {
		t.Fatal("service must survive")
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if v := got.Attrs[fmt.Sprintf("k%02d", i)]; v != tt.want {
				t.Errorf("scrub(%q) = %q, want %q", tt.in, v, tt.want)
			}
		})
	}
}

// Scrubbing must stay cheap on hostile free text: a long run of digits or hex
// digits next to an address must not make the scan quadratic.
func TestScrubTextStaysLinearOnLongRuns(t *testing.T) {
	d := dependents{addrs: map[netip.Addr]bool{netip.MustParseAddr("192.0.2.50"): true}}
	for name, in := range map[string]string{
		"digits":           strings.Repeat("1", 20000) + ".",
		"hex and colons":   strings.Repeat("a", 20000) + ":",
		"digits then addr": strings.Repeat("1", 20000) + "192.0.2.50",
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			got := d.scrubText(in)
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("scrubText took %v on %d characters", elapsed, len(in))
			}
			if strings.Contains(got, "192.0.2.50") {
				t.Errorf("denied address leaked")
			}
		})
	}
}

func TestApplyScrubsAliasesAndMapKeysOfDeniedHosts(t *testing.T) {
	g := model.NewGraph()
	denied := entity(t, "netbox", model.KindHost, "lab01.example.com", map[string]string{LabelTenant: "lab"}, nil)
	denied.Addresses = []netip.Addr{netip.MustParseAddr("192.0.2.50")}
	if err := g.AddEntity(denied); err != nil {
		t.Fatal(err)
	}
	svc := entity(t, "k8s", model.KindService, "shop/edge",
		map[string]string{"via.192.0.2.50": "x", "keep": "y"},
		map[string]string{"uplink.lab01.example.com": "10.0.0.1", "plain": "ok"})
	svc.Aliases = []string{"192.0.2.50", "[2001:db8::1]", "keep.example.com"}
	if err := g.AddEntity(svc); err != nil {
		t.Fatal(err)
	}

	f, err := New(Config{Collectors: map[string]Rules{"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out.Entity(svc.ID)
	if !ok {
		t.Fatal("service must survive")
	}
	if want := []string{"[2001:db8::1]", "keep.example.com"}; !slices.Equal(got.Aliases, want) {
		t.Errorf("Aliases = %v, want %v", got.Aliases, want)
	}
	wantAttrs := map[string]string{"uplink." + Placeholder: "10.0.0.1", "plain": "ok"}
	if fmt.Sprint(got.Attrs) != fmt.Sprint(wantAttrs) {
		t.Errorf("Attrs = %v, want %v", got.Attrs, wantAttrs)
	}
	wantLabels := map[string]string{"via." + Placeholder: "x", "keep": "y"}
	if fmt.Sprint(got.Labels) != fmt.Sprint(wantLabels) {
		t.Errorf("Labels = %v, want %v", got.Labels, wantLabels)
	}
}

// Secrets are recognised by their original key: scrubbing a denied hostname out
// of a key must never turn a secret key into an innocent-looking one.
func TestApplyRedactsSecretKeysBeforeScrubbingDeniedNames(t *testing.T) {
	g := model.NewGraph()
	denied := entity(t, "netbox", model.KindHost, "token", map[string]string{LabelTenant: "lab"}, nil)
	if err := g.AddEntity(denied); err != nil {
		t.Fatal(err)
	}
	svc := entity(t, "k8s", model.KindService, "shop/edge", nil,
		map[string]string{"api_token": "fake-secret-value", "plain": "ok"})
	if err := g.AddEntity(svc); err != nil {
		t.Fatal(err)
	}

	f, err := New(Config{Collectors: map[string]Rules{"netbox": {Deny: []Match{{Tenants: []string{"lab"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Apply(g)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out.Entity(svc.ID)
	if !ok {
		t.Fatal("service must survive")
	}
	for k, v := range got.Attrs {
		if strings.Contains(v, "fake-secret-value") {
			t.Errorf("secret leaked under scrubbed key %q: %q", k, v)
		}
	}
	if got.Attrs["plain"] != "ok" {
		t.Errorf("plain attr = %q, want ok", got.Attrs["plain"])
	}
}
