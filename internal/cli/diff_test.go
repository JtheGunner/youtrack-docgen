package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JtheGunner/youtrack-docgen/internal/testutil/golden"
)

// fakeYouTrack is an in-memory Knowledge Base. It records every request and
// every request body.
type fakeYouTrack struct {
	srv *httptest.Server
	mu  sync.Mutex
	// requests holds "METHOD path" of every call.
	requests []string
	// bodies holds the raw body of every write call.
	bodies []string
	// articles is the content of the fake Knowledge Base.
	articles []map[string]any
	// projects is what the project list returns.
	projects []map[string]any
	// tags is the list of tags known to the fake.
	tags   []map[string]any
	nextID int
}

func newFakeYouTrack(t *testing.T) *fakeYouTrack {
	t.Helper()
	f := &fakeYouTrack{projects: []map[string]any{{"id": fakeProjectID, "shortName": "INFRA", "name": "Infrastructure"}}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+fakeYouTrackToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			f.bodies = append(f.bodies, string(raw))
			_ = json.Unmarshal(raw, &body)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/admin/projects":
			_ = json.NewEncoder(w).Encode(f.projects)
		case r.Method == http.MethodGet && r.URL.Path == "/api/articles":
			_ = json.NewEncoder(w).Encode(f.articles)
		case r.Method == http.MethodPost && r.URL.Path == "/api/articles":
			f.nextID++
			body["id"] = "1-" + strconv.Itoa(f.nextID)
			f.articles = append(f.articles, body)
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == http.MethodGet && r.URL.Path == "/api/tags":
			_ = json.NewEncoder(w).Encode(f.tags)
		case r.Method == http.MethodPost && r.URL.Path == "/api/tags":
			body["id"] = "6-" + strconv.Itoa(len(f.tags)+1)
			f.tags = append(f.tags, body)
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/articles/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/articles/")
			for _, a := range f.articles {
				if a["id"] == id {
					for k, v := range body {
						a[k] = v
					}
					_ = json.NewEncoder(w).Encode(a)
					return
				}
			}
			http.Error(w, "no such article", http.StatusNotFound)
		default:
			http.Error(w, "unexpected", http.StatusTeapot)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// writes returns the recorded requests that are not reads.
func (f *fakeYouTrack) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, req := range f.requests {
		if !strings.HasPrefix(req, "GET ") {
			out = append(out, req)
		}
	}
	return out
}

func diffConfig(netboxURL, youtrackURL, outDir string) string {
	return "youtrack:\n  url: " + youtrackURL + "\n  token: ${YOUTRACK_TOKEN}\n  project: INFRA\n" +
		generateConfig(netboxURL, outDir, "") + "  youtrack: {}\n"
}

func runCLI(t *testing.T, cfg string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := newRootCmd(&out, &errOut, func() time.Time { return generatedAt })
	cmd.SetArgs(append(args, "--config", writeConfig(t, cfg)))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestDiffAndDryRunPrintDiffAndWriteNothing(t *testing.T) {
	for _, args := range [][]string{{"diff"}, {"publish", "--dry-run"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
			t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
			nb := fakeNetBox(t)
			yt := newFakeYouTrack(t)
			out := filepath.Join(t.TempDir(), "out")

			stdout, stderr, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, out), args...)
			if err != nil {
				t.Fatalf("error = %v\nstderr: %s", err, stderr)
			}
			golden.Assert(t, "diff-empty-kb", []byte(stdout))

			if w := yt.writes(); len(w) > 0 {
				t.Errorf("dry run made write requests: %v", w)
			}
			if len(yt.requests) == 0 {
				t.Error("dry run never read from YouTrack")
			}
			if files := readDirIfExists(out); len(files) > 0 {
				t.Errorf("dry run wrote files: %v", files)
			}
		})
	}
}

func TestDiffNeverPrintsSeededSecrets(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)

	stdout, stderr, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "diff")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{seededSecret, fakeNetBoxToken, fakeYouTrackToken} {
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("secret %q reached the output", secret)
		}
	}
}

// Article text is written by other people. Control characters in it must be
// shown escaped and never reach the terminal raw.
func TestDiffEscapesControlCharactersFromYouTrack(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	yt.articles = []map[string]any{{
		"id":      "1-1",
		"summary": "Infra \x1b]0;pwned\x07title",
		"content": "<!-- docgen:doc overview:root -->\n\nmanual \x1b[2J\rtext\u009b\n",
	}}

	stdout, _, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "diff")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range stdout {
		if r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r <= 0x9f)) {
			t.Fatalf("raw control character %U in the output", r)
		}
	}
	if !strings.Contains(stdout, `-Title: Infra \x1b]0;pwned\x07title`) || !strings.Contains(stdout, `manual \x1b[2J\rtext\x9b`) {
		t.Errorf("escaped article text missing from the output:\n%s", stdout)
	}
}

func TestDiffRequiresYouTrackSink(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	nb := fakeNetBox(t)
	for _, args := range [][]string{{"diff"}, {"publish", "--dry-run"}} {
		_, _, err := runCLI(t, generateConfig(nb.URL, t.TempDir(), ""), args...)
		if err == nil || !strings.Contains(err.Error(), "enable the youtrack sink") {
			t.Errorf("%v: error = %v, want a hint to enable the youtrack sink", args, err)
		}
	}
}

func TestDiffReportsUnreachableYouTrack(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	t.Setenv("YOUTRACK_TOKEN", fakeYouTrackToken)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	yt.srv.Close()

	_, _, err := runCLI(t, diffConfig(nb.URL, yt.srv.URL, t.TempDir()), "diff")
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), fakeYouTrackToken) {
		t.Error("error leaks the YouTrack token")
	}
}

// readDirIfExists lists the files of dir, or nothing when it does not exist.
func readDirIfExists(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
