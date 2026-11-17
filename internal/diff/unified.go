// Package diff renders line-based unified diffs.
package diff

import (
	"fmt"
	"strings"
)

type op struct {
	kind byte // ' ', '-' or '+'
	text string
}

// Unified returns a unified diff from oldText to newText, or "" when the
// texts have the same lines. oldName and newName label the two file headers
// and context is the number of unchanged lines kept around each change.
func Unified(oldName, newName, oldText, newText string, context int) string {
	ops := diffLines(splitLines(oldText), splitLines(newText))
	hunks := hunkRanges(ops, context)
	if len(hunks) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	// start positions of every op in the old and new text (1-based)
	oldPos, newPos := make([]int, len(ops)+1), make([]int, len(ops)+1)
	o, n := 1, 1
	for i, p := range ops {
		oldPos[i], newPos[i] = o, n
		if p.kind != '+' {
			o++
		}
		if p.kind != '-' {
			n++
		}
	}
	for _, h := range hunks {
		var oldCount, newCount int
		for _, p := range ops[h[0]:h[1]] {
			if p.kind != '+' {
				oldCount++
			}
			if p.kind != '-' {
				newCount++
			}
		}
		oldStart, newStart := oldPos[h[0]], newPos[h[0]]
		if oldCount == 0 {
			oldStart--
		}
		if newCount == 0 {
			newStart--
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, p := range ops[h[0]:h[1]] {
			b.WriteByte(p.kind)
			b.WriteString(p.text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// hunkRanges returns [start, end) op ranges: every change plus context lines,
// with ranges that touch or overlap merged.
func hunkRanges(ops []op, context int) [][2]int {
	var out [][2]int
	for i, p := range ops {
		if p.kind == ' ' {
			continue
		}
		start, end := max(i-context, 0), min(i+context+1, len(ops))
		if last := len(out) - 1; last >= 0 && start <= out[last][1] {
			out[last][1] = end
			continue
		}
		out = append(out, [2]int{start, end})
	}
	return out
}

// diffLines computes a shortest edit script with a longest-common-subsequence
// table after trimming the common prefix and suffix.
func diffLines(a, b []string) []op {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	midA, midB := a[prefix:len(a)-suffix], b[prefix:len(b)-suffix]

	var ops []op
	for _, l := range a[:prefix] {
		ops = append(ops, op{' ', l})
	}
	ops = append(ops, lcsOps(midA, midB)...)
	for _, l := range a[len(a)-suffix:] {
		ops = append(ops, op{' ', l})
	}
	return ops
}

func lcsOps(a, b []string) []op {
	// lcs[i][j] is the LCS length of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, op{'+', b[j]})
	}
	return ops
}
