package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JtheGunner/youtrackt-docgen/internal/testutil/golden"
)

// fakeYouTrack serves an empty Knowledge Base and records every request.
type fakeYouTrack struct {
	srv *httptest.Server
	mu  sync.Mutex
	// requests holds "METHOD path" of every call.
	requests []string
}

func newFakeYouTrack(t *testing.T) *fakeYouTrack {
	t.Helper()
	f := &fakeYouTrack{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+fakeYouTrackToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/articles" {
			http.Error(w, "unexpected", http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(f.srv.Close)
	return f
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
	for _, args := range [][]string{{"diff"}, {"generate", "--dry-run"}} {
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

			for _, req := range yt.requests {
				if !strings.HasPrefix(req, "GET ") {
					t.Errorf("dry run made a write request: %s", req)
				}
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

func TestDiffRequiresYouTrackSink(t *testing.T) {
	t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
	nb := fakeNetBox(t)
	for _, args := range [][]string{{"diff"}, {"generate", "--dry-run"}} {
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
