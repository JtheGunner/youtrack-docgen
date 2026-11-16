package golden

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	t.Run("matching content passes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "case.golden")
		writeFile(t, path, "hello\n")

		if err := compare(path, []byte("hello\n"), false); err != nil {
			t.Fatalf("compare() = %v, want nil", err)
		}
	})

	t.Run("differing content reports expected and actual", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "case.golden")
		writeFile(t, path, "expected\n")

		err := compare(path, []byte("actual\n"), false)
		if err == nil {
			t.Fatal("compare() = nil, want mismatch error")
		}
		for _, want := range []string{"expected", "actual", "-update"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("missing golden file fails without update", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.golden")

		if err := compare(path, []byte("x"), false); err == nil {
			t.Fatal("compare() = nil, want error for missing golden file")
		}
	})

	t.Run("update creates the file, including parent directories", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "dir", "case.golden")

		if err := compare(path, []byte("fresh\n"), true); err != nil {
			t.Fatalf("compare(update) = %v, want nil", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading updated golden file: %v", err)
		}
		if string(got) != "fresh\n" {
			t.Errorf("golden file = %q, want %q", got, "fresh\n")
		}
	})

	t.Run("update overwrites stale content", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "case.golden")
		writeFile(t, path, "stale\n")

		if err := compare(path, []byte("current\n"), true); err != nil {
			t.Fatalf("compare(update) = %v, want nil", err)
		}
		if err := compare(path, []byte("current\n"), false); err != nil {
			t.Fatalf("compare() after update = %v, want nil", err)
		}
	})
}

func TestPath(t *testing.T) {
	got := path("host overview")
	want := filepath.Join("testdata", "host overview.golden")
	if got != want {
		t.Errorf("path() = %q, want %q", got, want)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
