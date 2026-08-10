package adapter

import (
	"fmt"
	"strings"
)

// Diff renders an operation as a unified diff.
//
// --dry-run exists so a user can see the exact bytes that would land in their
// configuration before anything is written. That is worth more than a summary:
// the whole trust argument for this tool is "we do not quietly mangle your
// files", and a diff is how that claim gets checked rather than believed.
//
// Implemented here rather than pulled in, because it is a hundred lines of
// standard algorithm and a dependency in the install path is a dependency in
// the threat model.
func Diff(op Op) string {
	switch op.Kind {
	case OpCopyTree:
		return fmt.Sprintf("copy %s -> %s\n", op.SourceDir, op.Path)
	case OpRemoveTree:
		return fmt.Sprintf("remove directory %s\n", op.Path)
	case OpRemoveFile:
		return fmt.Sprintf("remove file %s\n", op.Path)
	}

	before := splitLines(string(op.Before))
	after := splitLines(string(op.After))

	var b strings.Builder
	if op.Before == nil {
		fmt.Fprintf(&b, "--- /dev/null\n+++ %s\n", op.Path)
	} else {
		fmt.Fprintf(&b, "--- %s\n+++ %s\n", op.Path, op.Path)
	}

	hunks := unified(before, after, 3)
	if len(hunks) == 0 {
		return ""
	}
	for _, h := range hunks {
		b.WriteString(h)
	}
	return b.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

type edit struct {
	op   byte // ' ', '-', '+'
	text string
}

// unified produces unified-diff hunks with the given context.
func unified(a, b []string, context int) []string {
	edits := diffLines(a, b)

	// Locate runs of changes, then emit each with surrounding context.
	var hunks []string
	i := 0
	for i < len(edits) {
		if edits[i].op == ' ' {
			i++
			continue
		}
		start := i
		for start > 0 && edits[start-1].op == ' ' && start > i-context {
			start--
		}
		end := i
		for end < len(edits) && (edits[end].op != ' ' || hasChangeWithin(edits, end, context)) {
			end++
		}
		for end < len(edits) && edits[end].op == ' ' && end < i+context {
			end++
		}

		hunks = append(hunks, renderHunk(edits[start:end], countBefore(edits[:start]), countAfter(edits[:start])))
		i = end
	}
	return hunks
}

func hasChangeWithin(edits []edit, from, n int) bool {
	for i := from; i < len(edits) && i < from+n+1; i++ {
		if edits[i].op != ' ' {
			return true
		}
	}
	return false
}

func countBefore(edits []edit) int {
	n := 0
	for _, e := range edits {
		if e.op == ' ' || e.op == '-' {
			n++
		}
	}
	return n
}

func countAfter(edits []edit) int {
	n := 0
	for _, e := range edits {
		if e.op == ' ' || e.op == '+' {
			n++
		}
	}
	return n
}

func renderHunk(edits []edit, beforeStart, afterStart int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
		beforeStart+1, countBefore(edits), afterStart+1, countAfter(edits))
	for _, e := range edits {
		b.WriteByte(e.op)
		b.WriteString(e.text)
		b.WriteByte('\n')
	}
	return b.String()
}

// diffLines computes a line-level diff via the classic LCS table.
//
// Config files are small — tens to hundreds of lines — so the quadratic space
// is irrelevant here, and the simplicity is worth more than Myers' asymptotics.
// The guard below keeps a pathological input from allocating unreasonably.
func diffLines(a, b []string) []edit {
	const maxCells = 4_000_000
	if len(a)*len(b) > maxCells {
		// Fall back to a whole-file replacement rather than risk the
		// allocation. A diff this large is not human-reviewable anyway.
		out := make([]edit, 0, len(a)+len(b))
		for _, line := range a {
			out = append(out, edit{'-', line})
		}
		for _, line := range b {
			out = append(out, edit{'+', line})
		}
		return out
	}

	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
				continue
			}
			if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var out []edit
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, edit{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, edit{'-', a[i]})
			i++
		default:
			out = append(out, edit{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, edit{'-', a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, edit{'+', b[j]})
	}
	return out
}
