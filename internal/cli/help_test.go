package cli

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestCommandHelpHasNoLiteralBackslashN(t *testing.T) {
	root := newRootCmd(io.Discard, io.Discard, time.Now)
	for _, cmd := range root.Commands() {
		if strings.Contains(cmd.Long, `\n`) {
			t.Errorf("%s: Long help contains a literal backslash-n: %q", cmd.Name(), cmd.Long)
		}
	}
}
