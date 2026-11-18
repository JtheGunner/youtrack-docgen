package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"no error", nil, 0},
		{"differences", errChanges, 2},
		{"wrapped differences", wrapErr(errChanges), 2},
		{"other error", context.Canceled, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode = %d, want %d", got, tt.want)
			}
		})
	}
}

func wrapErr(err error) error { return &wrapped{err} }

type wrapped struct{ err error }

func (w *wrapped) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }

func TestDiffExitCodeFlag(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	yt := newFakeYouTrack(t)
	cfg := diffConfig(nb.URL, yt.srv.URL, t.TempDir())

	t.Run("default stays zero on changes", func(t *testing.T) {
		if _, _, err := runCLI(t, cfg, "diff"); err != nil {
			t.Fatalf("diff error = %v, want nil", err)
		}
	})
	t.Run("changes give exit status 2 and no error text", func(t *testing.T) {
		stdout, stderr, err := runCLI(t, cfg, "diff", "--exit-code")
		if got := ExitCode(err); got != 2 {
			t.Fatalf("exit code = %d (err %v), want 2", got, err)
		}
		if !strings.Contains(stdout, "sink youtrack (dry run)") {
			t.Errorf("stdout lacks the summary:\n%s", stdout)
		}
		if strings.Contains(stderr, "Error") {
			t.Errorf("stderr = %q, want no error text for plain differences", stderr)
		}
	})
	t.Run("no changes give exit status 0", func(t *testing.T) {
		if _, _, err := runCLI(t, cfg, "publish"); err != nil {
			t.Fatal(err)
		}
		if _, stderr, err := runCLI(t, cfg, "diff", "--exit-code"); err != nil {
			t.Fatalf("exit code = %d (err %v, stderr %s), want 0", ExitCode(err), err, stderr)
		}
	})
}

func TestDiffExitCodeFlagKeepsRealErrorsAtOne(t *testing.T) {
	publishEnv(t)
	nb := fakeNetBox(t)
	cfg := diffConfig(nb.URL, "http://127.0.0.1:1", t.TempDir())
	_, _, err := runCLI(t, cfg, "diff", "--exit-code")
	if got := ExitCode(err); got != 1 {
		t.Errorf("exit code = %d (err %v), want 1", got, err)
	}
}

// TestSignalsAbortGenerate sends SIGTERM to the process while a collector
// waits for NetBox and expects the run to stop without leaving output behind.
func TestSignalsAbortGenerate(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Setenv("NETBOX_TOKEN", fakeNetBoxToken)
			started := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				select {
				case started <- struct{}{}:
				default:
				}
				<-r.Context().Done()
			}))
			t.Cleanup(srv.Close)
			out := filepath.Join(t.TempDir(), "out")

			var stdout, stderr bytes.Buffer
			root := newRootCmd(&stdout, &stderr, time.Now)
			root.SetArgs([]string{"generate", "--config", writeConfig(t, generateConfig(srv.URL, out, ""))})
			done := make(chan error, 1)
			go func() { done <- executeWithSignals(root) }()

			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("the collector never reached NetBox")
			}
			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("generate error = nil, want an abort error")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the run did not stop after the signal")
			}
			if files := readDirIfExists(out); len(files) > 0 {
				t.Errorf("aborted run left files behind: %v", files)
			}
		})
	}
}
