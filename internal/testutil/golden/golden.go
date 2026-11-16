// Package golden compares test output against golden files stored in the
// calling package's testdata directory.
//
// Run the tests with -update to rewrite the golden files from the current
// output, then review the diff before committing.
package golden

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files from the current output")

// Assert fails the test when got differs from testdata/<name>.golden.
// With -update it writes got to that file instead.
func Assert(t testing.TB, name string, got []byte) {
	t.Helper()
	if err := compare(path(name), got, *update); err != nil {
		t.Fatal(err)
	}
}

func path(name string) string {
	return filepath.Join("testdata", name+".golden")
}

func compare(goldenPath string, got []byte, update bool) error {
	if update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o750); err != nil {
			return fmt.Errorf("creating golden directory: %w", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o600); err != nil {
			return fmt.Errorf("writing golden file %s: %w", goldenPath, err)
		}
		return nil
	}

	want, err := os.ReadFile(goldenPath) //nolint:gosec // path is built from a test-controlled name
	if err != nil {
		return fmt.Errorf("reading golden file %s (run with -update to create it): %w", goldenPath, err)
	}
	if !bytes.Equal(want, got) {
		return fmt.Errorf("output differs from %s (run with -update to accept)\n--- expected\n%s\n--- actual\n%s", goldenPath, want, got)
	}
	return nil
}
