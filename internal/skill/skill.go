// Package skill parses SKILL.md files.
//
// The Agent Skills specification owns this format; Agent Plugins defines only
// where skills are discovered. This package therefore parses permissively and
// preserves the frontmatter whole rather than imposing a schema on fields it
// does not own — a skill carrying frontmatter keys we have never heard of must
// still load and must survive a round trip intact.
package skill

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/agentbridgehq/agentbridge/internal/diag"
	"github.com/agentbridgehq/agentbridge/internal/ir"
)

// Parsed is the result of reading a skill's Markdown file.
type Parsed struct {
	// Name from frontmatter, empty if absent. The caller decides the fallback,
	// because the rule differs by dialect.
	Name string
	// Description from frontmatter, empty if absent.
	Description string
	// Frontmatter is the full decoded YAML block, preserved as-is.
	Frontmatter map[string]any
	// Body is the Markdown after the frontmatter block. Held only long enough
	// for capability inference; it is not stored in the IR.
	Body string
	// ContentHash is the digest of the entire raw file, frontmatter included.
	ContentHash string
	// HasFrontmatter records whether a frontmatter block was present at all.
	HasFrontmatter bool
}

var frontmatterDelim = []byte("---")

// Parse reads a SKILL.md file's bytes.
//
// A file with no frontmatter is valid and parses to a body-only result; the
// Agent Skills specification governs whether that is acceptable for a given
// skill, not this package. Malformed YAML produces an error diagnostic and a
// body-only result, so one broken skill degrades to "loaded without metadata"
// rather than taking the plugin down.
func Parse(raw []byte) (*Parsed, diag.Diagnostics) {
	var ds diag.Diagnostics
	p := &Parsed{ContentHash: ir.HashBytes(raw)}

	fm, body, found := splitFrontmatter(raw)
	p.Body = string(body)
	if !found {
		return p, ds
	}
	p.HasFrontmatter = true

	var meta map[string]any
	if err := yaml.Unmarshal(fm, &meta); err != nil {
		ds.Add(diag.Error, diag.CodeSkillInvalidFrontmat, "",
			"frontmatter is not valid YAML: %v", err)
		return p, ds
	}
	if meta == nil {
		// An empty frontmatter block is well-formed; treat it as no metadata.
		return p, ds
	}

	p.Frontmatter = meta
	p.Name = stringField(meta, "name")
	p.Description = stringField(meta, "description")
	return p, ds
}

// splitFrontmatter separates a leading `---` delimited YAML block from the
// body. It tolerates a UTF-8 BOM and both LF and CRLF line endings, since
// plugins are authored on every platform and a BOM from a Windows editor
// should not silently cost a skill its name.
func splitFrontmatter(raw []byte) (frontmatter, body []byte, found bool) {
	b := bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	line, rest, ok := nextLine(b)
	if !ok || !bytes.Equal(bytes.TrimRight(line, "\r"), frontmatterDelim) {
		return nil, raw, false
	}

	var fm bytes.Buffer
	for {
		line, next, ok := nextLine(rest)
		if !ok {
			// Unterminated frontmatter block. Treat the whole file as body:
			// guessing where the author meant it to end would be worse than
			// reporting no metadata.
			return nil, raw, false
		}
		if bytes.Equal(bytes.TrimRight(line, "\r"), frontmatterDelim) {
			return fm.Bytes(), next, true
		}
		fm.Write(bytes.TrimRight(line, "\r"))
		fm.WriteByte('\n')
		rest = next
	}
}

// nextLine splits off the first line, returning the remainder. ok is false
// when there is nothing left to read.
func nextLine(b []byte) (line, rest []byte, ok bool) {
	if len(b) == 0 {
		return nil, nil, false
	}
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return b, nil, true
	}
	return b[:i], b[i+1:], true
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// DirName derives a skill name from its directory basename, the fallback both
// dialects use when frontmatter carries no name.
func DirName(dir string) string {
	dir = strings.TrimSuffix(dir, "/")
	if i := strings.LastIndexAny(dir, "/\\"); i >= 0 {
		return dir[i+1:]
	}
	return dir
}

// Describe renders a short identification of a skill for diagnostics.
func Describe(name, path string) string {
	if name == "" {
		return fmt.Sprintf("skill at %s", path)
	}
	return fmt.Sprintf("skill %q (%s)", name, path)
}
