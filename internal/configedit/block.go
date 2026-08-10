package configedit

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Marker lines delimiting the region this tool owns. They are deliberately
// verbose: someone opening the file months later needs to know what wrote this
// and that hand-edits inside it will be lost.
const (
	BlockBegin = "# >>> agentbridge: managed block, do not edit by hand >>>"
	BlockEnd   = "# <<< agentbridge: managed block end <<<"
)

// BlockDoc is a line-oriented config file in which this tool owns a single
// marker-delimited region.
//
// Used for TOML (Codex). No comment-preserving TOML editor exists that is worth
// the dependency and the licence review, and hand-rolling one is a poor trade
// for a single client. Owning a clearly-labelled region instead means every
// byte outside it is preserved exactly — which is the property that actually
// matters — and the user can see at a glance what is ours.
//
// Inside the block, content is organized into named sections. A section starts
// at a table header line and runs to the next header or the end of the block,
// so an entry can be added or removed without disturbing its neighbours.
type BlockDoc struct {
	path string
	// raw is the file exactly as read. Original returns it verbatim rather
	// than re-rendering, because a human may have added blank lines or
	// reflowed the managed block, and re-rendering would report those as
	// changes we are about to make when we are not.
	raw     []byte
	existed bool
	// before and after are the file content outside the managed block.
	before, after []byte
	hadBlock      bool
	sections      map[string][]string
	order         []string
}

// LoadBlock reads a file that may contain a managed block. A missing file
// yields an empty document.
func LoadBlock(path string) (*BlockDoc, error) {
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		return &BlockDoc{path: path, sections: map[string][]string{}}, nil
	default:
		return nil, err
	}

	d := &BlockDoc{path: path, raw: raw, existed: true, sections: map[string][]string{}}

	beginIdx := indexOfLine(raw, BlockBegin)
	if beginIdx < 0 {
		d.before = raw
		return d, nil
	}
	endIdx := indexOfLine(raw, BlockEnd)
	if endIdx < 0 || endIdx < beginIdx {
		// A begin marker with no end means a previous write was interrupted or
		// the file was hand-edited. Refusing is safer than guessing where the
		// block was meant to stop and deleting user content.
		return nil, fmt.Errorf("%s: managed block has no %q terminator; "+
			"remove the block by hand and re-run", path, BlockEnd)
	}

	d.hadBlock = true
	d.before = raw[:beginIdx]
	d.after = raw[endIdx+len(BlockEnd):]
	d.after = bytes.TrimPrefix(d.after, []byte("\n"))

	body := string(raw[beginIdx+len(BlockBegin) : endIdx])
	d.parseSections(body)
	return d, nil
}

// Path returns the file's path.
func (d *BlockDoc) Path() string { return d.path }

// Existed reports whether the file was present on disk.
func (d *BlockDoc) Existed() bool { return d.existed }

// Original returns the file's bytes as loaded, or nil if it did not exist.
func (d *BlockDoc) Original() []byte {
	if !d.existed {
		return nil
	}
	return bytes.Clone(d.raw)
}

// Sections returns the managed section names in file order.
func (d *BlockDoc) Sections() []string { return append([]string(nil), d.order...) }

// SetSection adds or replaces a managed section. Content is the section's
// lines, header included.
func (d *BlockDoc) SetSection(name string, content []string) {
	if _, exists := d.sections[name]; !exists {
		d.order = append(d.order, name)
	}
	d.sections[name] = content
}

// DeleteSection removes a managed section. Removing an absent section is a
// no-op so that uninstall is idempotent.
func (d *BlockDoc) DeleteSection(name string) {
	if _, exists := d.sections[name]; !exists {
		return
	}
	delete(d.sections, name)
	for i, n := range d.order {
		if n == name {
			d.order = append(d.order[:i], d.order[i+1:]...)
			break
		}
	}
}

// Bytes renders the document. When the managed block becomes empty it is
// removed entirely, along with the markers — an uninstall should leave no
// trace, not an empty labelled region.
func (d *BlockDoc) Bytes() []byte { return d.render(len(d.sections) > 0) }

func (d *BlockDoc) render(withBlock bool) []byte {
	var buf bytes.Buffer
	buf.Write(d.before)

	if !withBlock {
		out := buf.Bytes()
		out = append(out, d.after...)
		return normalizeTrailing(out)
	}

	if buf.Len() > 0 && !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		buf.WriteByte('\n')
	}
	if buf.Len() > 0 && !bytes.HasSuffix(buf.Bytes(), []byte("\n\n")) {
		buf.WriteByte('\n')
	}

	buf.WriteString(BlockBegin)
	buf.WriteByte('\n')
	// Sorted so the file does not churn between runs on map ordering.
	names := append([]string(nil), d.order...)
	sort.Strings(names)
	for i, name := range names {
		if i > 0 {
			buf.WriteByte('\n')
		}
		for _, line := range d.sections[name] {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	buf.WriteString(BlockEnd)
	buf.WriteByte('\n')

	if len(d.after) > 0 {
		buf.Write(d.after)
	}
	return normalizeTrailing(buf.Bytes())
}

// parseSections splits a managed block's body into named sections keyed by
// their table header line.
func (d *BlockDoc) parseSections(body string) {
	var current string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" && current == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			current = trimmed
			d.order = append(d.order, current)
			d.sections[current] = []string{line}
			continue
		}
		if current == "" {
			continue
		}
		d.sections[current] = append(d.sections[current], line)
	}
	// Trailing blank lines belong to the block's layout, not to a section.
	for name, lines := range d.sections {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		d.sections[name] = lines
	}
}

func normalizeTrailing(b []byte) []byte {
	b = bytes.TrimRight(b, "\n")
	if len(b) == 0 {
		return nil
	}
	return append(b, '\n')
}

// indexOfLine finds a marker that occupies a whole line, so a marker string
// appearing inside a user's comment or string value is not mistaken for the
// real thing.
func indexOfLine(raw []byte, marker string) int {
	m := []byte(marker)
	from := 0
	for {
		i := bytes.Index(raw[from:], m)
		if i < 0 {
			return -1
		}
		abs := from + i
		atLineStart := abs == 0 || raw[abs-1] == '\n'
		end := abs + len(m)
		atLineEnd := end == len(raw) || raw[end] == '\n' || raw[end] == '\r'
		if atLineStart && atLineEnd {
			return abs
		}
		from = abs + len(m)
	}
}

// TOMLString renders a value as a TOML basic string.
func TOMLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TOMLStringArray renders a list of strings as a TOML array.
func TOMLStringArray(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = TOMLString(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// TOMLInlineTable renders a string map as a TOML inline table with sorted keys.
func TOMLInlineTable(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s = %s", TOMLString(k), TOMLString(m[k]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}
