package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/testutil/golden"
)

const seededSecret = "hunter2-seeded-fake-secret"

// fixtureDir holds the synthetic NetBox API responses of the collector tests.
var fixtureDir = filepath.Join("..", "collector", "netbox", "testdata")

var generatedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fakeNetBox serves the NetBox fixtures. Every empty description is replaced
// by one that carries a seeded secret, so a leak into the output is visible.
func fakeNetBox(t *testing.T) *httptest.Server {
	t.Helper()
	files := map[string]string{
		"/api/dcim/sites/":                      "sites.json",
		"/api/ipam/vlans/":                      "vlans.json",
		"/api/ipam/prefixes/":                   "prefixes.json",
		"/api/virtualization/virtual-machines/": "vms.json",
		"/api/dcim/interfaces/":                 "device-interfaces.json",
		"/api/virtualization/interfaces/":       "vm-interfaces.json",
		"/api/ipam/ip-addresses/":               "ip-addresses.json",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token "+fakeNetBoxToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		name, ok := files[r.URL.Path]
		if r.URL.Path == "/api/dcim/devices/" {
			name, ok = "devices-1.json", true
			if r.URL.Query().Get("offset") == "2" {
				name = "devices-2.json"
			}
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		body, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := strings.ReplaceAll(string(body), `"description": ""`, `"description": "login with password=`+seededSecret+`"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, strings.ReplaceAll(out, "{{BASE}}", "http://"+r.Host))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func generateConfig(netboxURL, outDir, extra string) string {
	return fmt.Sprintf(`
collectors:
  netbox:
    url: %s
    token: ${NETBOX_TOKEN}
%s
sinks:
  markdown:
    dir: %s
`, netboxURL, extra, outDir)
}

func runGenerate(t *testing.T, cfg string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := newRootCmd(&out, &errOut, func() time.Time { return generatedAt })
	cmd.SetArgs([]string{"generate", "--config", writeConfig(t, cfg)})
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func readDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = body
	}
	return files
}

func TestGenerateWritesExpectedDirectory(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeNetBox(t)
	out := filepath.Join(t.TempDir(), "out")

	stdout, stderr, err := runGenerate(t, generateConfig(srv.URL, out, ""))
	if err != nil {
		t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "sink markdown: wrote") {
		t.Errorf("stdout = %q, want a summary line", stdout)
	}

	files := readDir(t, out)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	golden.Assert(t, "generate-files", []byte(strings.Join(names, "\n")+"\n"))
	for _, name := range names {
		golden.Assert(t, "generate-"+strings.TrimSuffix(name, ".md"), files[name])
	}
}

func TestGenerateNeverWritesSeededSecrets(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeNetBox(t)
	out := filepath.Join(t.TempDir(), "out")

	stdout, stderr, err := runGenerate(t, generateConfig(srv.URL, out, ""))
	if err != nil {
		t.Fatalf("generate error = %v", err)
	}
	all := stdout + stderr
	for name, body := range readDir(t, out) {
		all += name + string(body)
	}
	for _, secret := range []string{seededSecret, fakeNetBoxToken} {
		if strings.Contains(all, secret) {
			t.Errorf("secret %q reached the output", secret)
		}
	}
}

func TestGenerateAppliesDenyRules(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeNetBox(t)
	out := filepath.Join(t.TempDir(), "out")
	deny := "    deny:\n      - kinds: [host]\n        roles: [server]\n"

	if _, stderr, err := runGenerate(t, generateConfig(srv.URL, out, deny)); err != nil {
		t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
	}
	files := readDir(t, out)
	if _, ok := files["netbox-host-srv01-example-com.md"]; ok {
		t.Error("the denied host still has a page")
	}
	if _, ok := files["netbox-host-sw01-example-com.md"]; !ok {
		t.Error("the permitted host has no page")
	}
	if strings.Contains(string(files["index.md"]), "srv01.example.com") {
		t.Error("index.md still lists the denied host")
	}
}

// The deny rules must match what the NetBox collector really emits: tags and
// tenants come from the fixtures, not from hand-built labels.
func TestGenerateAppliesTagAndTenantDenyRules(t *testing.T) {
	tests := []struct {
		name       string
		deny       string
		deniedPage string
		keptPage   string
	}{
		{"tag", "    deny:\n      - kinds: [host]\n        tags: [prod]\n",
			"netbox-host-srv01-example-com.md", "netbox-host-sw01-example-com.md"},
		{"tenant", "    deny:\n      - kinds: [host]\n        tenants: [example-*]\n",
			"netbox-host-sw01-example-com.md", "netbox-host-srv01-example-com.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
			srv := fakeNetBox(t)
			out := filepath.Join(t.TempDir(), "out")

			if _, stderr, err := runGenerate(t, generateConfig(srv.URL, out, tt.deny)); err != nil {
				t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
			}
			files := readDir(t, out)
			if _, ok := files[tt.deniedPage]; ok {
				t.Errorf("%s is denied but still has a page", tt.deniedPage)
			}
			if _, ok := files[tt.keptPage]; !ok {
				t.Errorf("%s is not denied but has no page", tt.keptPage)
			}
		})
	}
}

func TestGenerateSkipsYouTrackSinkWithNote(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
	srv := fakeNetBox(t)
	out := filepath.Join(t.TempDir(), "out")
	cfg := "youtrack:\n  url: https://youtrack.example.com\n  token: ${YOUTRACK_TOKEN}\n  project: INFRA\n" +
		generateConfig(srv.URL, out, "") + "  youtrack: {}\n"

	_, stderr, err := runGenerate(t, cfg)
	if err != nil {
		t.Fatalf("generate error = %v", err)
	}
	if !strings.Contains(stderr, "skipping the youtrack sink") {
		t.Errorf("stderr = %q, want a skip note", stderr)
	}
	if strings.Contains(stderr, fakeYouTrackToken) {
		t.Error("stderr leaks the YouTrack token")
	}
}

func TestGenerateFailures(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeNetBox(t)
	out := filepath.Join(t.TempDir(), "out")

	tests := []struct {
		name    string
		cfg     string
		wantErr string
	}{
		{
			name:    "unsupported collector",
			cfg:     generateConfig(srv.URL, out, "  kubernetes:\n    paths: [./k8s]"),
			wantErr: `collector "kubernetes"`,
		},
		{
			name:    "unreachable source",
			cfg:     generateConfig("http://127.0.0.1:1", out, ""),
			wantErr: "collector netbox",
		},
		{
			name:    "only the youtrack sink",
			cfg:     "youtrack:\n  url: https://youtrack.example.com\n  token: x\n  project: INFRA\ncollectors:\n  netbox:\n    url: " + srv.URL + "\n    token: ${NETBOX_TOKEN}\nsinks:\n  youtrack: {}\n",
			wantErr: "no sink",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runGenerate(t, tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			if _, statErr := os.Stat(out); statErr == nil {
				t.Error("output directory was created despite the failure")
			}
		})
	}
}

func TestGenerateRequiresValidConfig(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", "")
	_, _, err := runGenerate(t, generateConfig("https://netbox.example.com", t.TempDir(), ""))
	if err == nil || !strings.Contains(err.Error(), "NETBOX_TOKEN") {
		t.Fatalf("error = %v, want a missing NETBOX_TOKEN report", err)
	}
}

// A denied host must vanish everywhere: its interfaces and IP addresses are
// removed too, so its name never reaches a file, stdout or stderr.
func TestGenerateHidesDeniedHostFromDependents(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	srv := fakeNetBox(t)
	out := filepath.Join(t.TempDir(), "out")
	deny := "    deny:\n      - kinds: [host]\n        roles: [server]\n"

	stdout, stderr, err := runGenerate(t, generateConfig(srv.URL, out, deny))
	if err != nil {
		t.Fatalf("generate error = %v\nstderr: %s", err, stderr)
	}
	outputs := map[string]string{"stdout": stdout, "stderr": stderr}
	for name, body := range readDir(t, out) {
		outputs[name] = string(body)
	}
	for name, body := range outputs {
		for _, denied := range []string{"srv01"} {
			if strings.Contains(strings.ToLower(name+"\n"+body), denied) {
				t.Errorf("%s reveals the denied host %q:\n%s", name, denied, body)
			}
		}
	}
}
