package diff

import "testing"

func TestUnified(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		want     string
	}{
		{"identical", "a\nb\n", "a\nb\n", ""},
		{"all new", "", "a\nb\n", "--- old\n+++ new\n@@ -0,0 +1,2 @@\n+a\n+b\n"},
		{"all removed", "a\n", "", "--- old\n+++ new\n@@ -1,1 +0,0 @@\n-a\n"},
		{
			"change in the middle with context",
			"1\n2\n3\n4\n5\n6\n7\n8\n9\n",
			"1\n2\n3\n4\nFIVE\n6\n7\n8\n9\n",
			"--- old\n+++ new\n@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+FIVE\n 6\n 7\n 8\n",
		},
		{
			"two distant changes make two hunks",
			"a\n1\n2\n3\n4\n5\n6\n7\n8\nb\n",
			"A\n1\n2\n3\n4\n5\n6\n7\n8\nB\n",
			"--- old\n+++ new\n@@ -1,4 +1,4 @@\n-a\n+A\n 1\n 2\n 3\n@@ -7,4 +7,4 @@\n 6\n 7\n 8\n-b\n+B\n",
		},
		{"missing trailing newline is added", "a", "a\nb", "--- old\n+++ new\n@@ -1,1 +1,2 @@\n a\n+b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Unified("old", "new", tt.old, tt.new, 3); got != tt.want {
				t.Errorf("Unified() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
