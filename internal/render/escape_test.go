package render

import (
	"strings"
	"testing"

	"github.com/JtheGunner/youtrack-docgen/model"
)

func TestInlineEscapesMarkdownAndHTML(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"script tag", "<script>alert(1)</script>", `&lt;script&gt;alert\(1\)&lt;/script&gt;`},
		{"link", "[x](http://evil.example.com)", `\[x\]\(http://evil.example.com\)`},
		{"image", "![i](u)", `\!\[i\]\(u\)`},
		{"backticks", "`code`", "\\`code\\`"},
		{"emphasis and heading", "*b* _i_ # h", `\*b\* \_i\_ \# h`},
		{"table break", "a | b\n| c", `a \| b \| c`},
		{"entity", "&lt;", "&amp;lt;"},
		{"backslash", `a\b`, `a\\b`},
		{"hostname untouched", "web01.example.com", "web01.example.com"},
		{"ip untouched", "192.0.2.10", "192.0.2.10"},
		{"hyphen and colon untouched", "db-01: 2001:db8::1", "db-01: 2001:db8::1"},
	}
	for _, tt := range tests {
		if got := inline(tt.in); got != tt.want {
			t.Errorf("%s: inline(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestSafeURLIsMarkdownSafe(t *testing.T) {
	got := safeURL("https://example.com/a_(b)/[c]/d e/`x`")
	for _, bad := range []string{"(", ")", "[", "]", "_", "`", " "} {
		if strings.Contains(got, bad) {
			t.Errorf("safeURL result %q contains %q", got, bad)
		}
	}
	if !strings.HasPrefix(got, "https://example.com/a%5F%28b%29/") {
		t.Errorf("unexpected result %q", got)
	}
}

func TestRenderEscapesHostileCollectorValues(t *testing.T) {
	g := model.NewGraph()
	hostile := model.Entity{
		ID: "netbox:host:evil", Kind: model.KindHost, Source: "netbox",
		Name:      "<script>alert(1)</script>",
		Aliases:   []string{"[x](http://evil.example.com)", "![i](u)", "`tick`"},
		Addresses: addrs("192.0.2.10"),
		Labels:    map[string]string{"role": "<img src=x onerror=alert(1)>"},
		Attrs: map[string]string{
			"fqdn":        "a|b.example.com",
			"description": "line\n\n# injected heading\n<b>bold</b>",
			"url":         "https://example.com/p_(q)",
		},
	}
	service := model.Entity{ID: "k8s:service:svc", Kind: model.KindService, Source: "k8s", Name: "<i>svc</i>|x"}
	for _, e := range []model.Entity{hostile, service} {
		if err := g.AddEntity(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddRelation(model.Relation{From: service.ID, To: hostile.ID, Kind: model.RelRunsOn}); err != nil {
		t.Fatal(err)
	}

	out := string(dump(Render(g, Options{GeneratedAt: generatedAt})))
	for _, raw := range []string{"<script", "<img", "<b>", "<i>", "](", "![", "\n# injected", "`tick`", "p_(q)"} {
		if strings.Contains(out, raw) {
			t.Errorf("output contains unescaped %q", raw)
		}
	}
	if !strings.Contains(out, "192.0.2.10") {
		t.Error("IP address is not plain text")
	}
}
