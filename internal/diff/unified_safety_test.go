package diff

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestUnifiedEscapesControlCharacters(t *testing.T) {
	got := Unified("a/\x1b[31mname", "b/x", "keep\tTab\n", "\x1b[2Jevil\r\u009b\x7f\n", 3)
	for _, r := range got {
		if r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r <= 0x9f)) {
			t.Fatalf("raw control character %U in output %q", r, got)
		}
	}
	for _, want := range []string{`a/\x1b[31mname`, `+\x1b[2Jevil\r\x9b\x7f`, "-keep\tTab"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not contain %q", got, want)
		}
	}
}

func TestUnifiedFallsBackForHugeInputs(t *testing.T) {
	const lines = 5000 // 25M cells, above the limit
	var oldText, newText strings.Builder
	for i := range lines {
		fmt.Fprintf(&oldText, "old %d\n", i)
		fmt.Fprintf(&newText, "new %d\n", i)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got := Unified("old", "new", oldText.String(), newText.String(), 3)
	runtime.ReadMemStats(&after)

	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 16<<20 {
		t.Errorf("allocated %d bytes, want far less than the LCS table would need", alloc)
	}
	want := fmt.Sprintf("@@ -1,%d +1,%d @@", lines, lines)
	if !strings.Contains(got, want) || !strings.Contains(got, "-old 0\n") || !strings.Contains(got, "+new 4999\n") {
		t.Errorf("fallback output is not a full replace hunk (len %d)", len(got))
	}
}

func TestUnifiedFallbackKeepsCommonPrefixAndSuffix(t *testing.T) {
	var oldText, newText strings.Builder
	oldText.WriteString("head\n")
	newText.WriteString("head\n")
	for i := range 3000 {
		fmt.Fprintf(&oldText, "o%d\n", i)
		fmt.Fprintf(&newText, "n%d\n", i)
	}
	oldText.WriteString("tail\n")
	newText.WriteString("tail\n")
	got := Unified("old", "new", oldText.String(), newText.String(), 1)
	if !strings.Contains(got, "\n head\n-o0\n") || !strings.HasSuffix(got, "+n2999\n tail\n") {
		t.Errorf("prefix/suffix context lost")
	}
}
