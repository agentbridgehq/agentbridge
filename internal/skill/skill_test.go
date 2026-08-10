package skill_test

import (
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/diag"
	"github.com/agentbridge/agentbridge/internal/skill"
)

func TestParseFrontmatter(t *testing.T) {
	p, ds := skill.Parse([]byte("---\nname: db-review\ndescription: Review SQL\nextra: kept\n---\nBody text.\n"))

	if len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
	if p.Name != "db-review" || p.Description != "Review SQL" {
		t.Errorf("name=%q description=%q", p.Name, p.Description)
	}
	// The Agent Skills specification owns this format. Filtering frontmatter to
	// a set of keys we happen to know would silently drop fields on a round
	// trip.
	if p.Frontmatter["extra"] != "kept" {
		t.Errorf("unknown frontmatter key dropped: %v", p.Frontmatter)
	}
	if strings.TrimSpace(p.Body) != "Body text." {
		t.Errorf("body = %q", p.Body)
	}
}

func TestParseWithoutFrontmatter(t *testing.T) {
	p, ds := skill.Parse([]byte("# Just markdown\n"))

	if len(ds) != 0 {
		t.Errorf("a file without frontmatter is valid; got %v", ds)
	}
	if p.HasFrontmatter || p.Name != "" {
		t.Errorf("parsed = %+v", p)
	}
	if !strings.Contains(p.Body, "Just markdown") {
		t.Errorf("body = %q", p.Body)
	}
}

// One skill with broken YAML must degrade to "loaded without metadata" rather
// than taking anything else down with it.
func TestParseInvalidYAMLIsIsolated(t *testing.T) {
	p, ds := skill.Parse([]byte("---\nname: [unclosed\n---\nBody.\n"))

	if !ds.HasErrors() {
		t.Fatalf("expected an error diagnostic, got %v", ds)
	}
	if ds[0].Code != diag.CodeSkillInvalidFrontmat {
		t.Errorf("code = %q", ds[0].Code)
	}
	if p == nil || p.ContentHash == "" {
		t.Error("a parse failure must still yield a hashable result")
	}
}

// Plugins are authored on every platform. A BOM from a Windows editor or CRLF
// line endings must not cost a skill its name.
func TestParseToleratesBOMAndCRLF(t *testing.T) {
	raw := "\xEF\xBB\xBF---\r\nname: windows-authored\r\n---\r\nBody.\r\n"
	p, ds := skill.Parse([]byte(raw))

	if len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
	if p.Name != "windows-authored" {
		t.Errorf("name = %q", p.Name)
	}
}

// Guessing where an unterminated block was meant to end would be worse than
// reporting no metadata, since the guess silently changes what the agent reads.
func TestParseUnterminatedFrontmatterIsTreatedAsBody(t *testing.T) {
	p, _ := skill.Parse([]byte("---\nname: x\nno closing delimiter\n"))

	if p.HasFrontmatter {
		t.Error("unterminated frontmatter should not be parsed as metadata")
	}
	if !strings.Contains(p.Body, "no closing delimiter") {
		t.Errorf("body = %q", p.Body)
	}
}

func TestParseEmptyFrontmatterBlock(t *testing.T) {
	p, ds := skill.Parse([]byte("---\n---\nBody.\n"))

	if len(ds) != 0 {
		t.Errorf("an empty block is well-formed; got %v", ds)
	}
	if p.Name != "" || len(p.Frontmatter) != 0 {
		t.Errorf("parsed = %+v", p)
	}
}

// A non-string name would otherwise be coerced into something surprising.
func TestParseIgnoresNonStringName(t *testing.T) {
	p, _ := skill.Parse([]byte("---\nname: 42\n---\nBody.\n"))

	if p.Name != "" {
		t.Errorf("name = %q, want empty so the caller applies its fallback", p.Name)
	}
}

func TestContentHashCoversWholeFile(t *testing.T) {
	a, _ := skill.Parse([]byte("---\nname: x\n---\nBody.\n"))
	b, _ := skill.Parse([]byte("---\nname: x\n---\nDifferent body.\n"))
	c, _ := skill.Parse([]byte("---\nname: y\n---\nBody.\n"))

	if a.ContentHash == b.ContentHash {
		t.Error("hash ignored a body change")
	}
	if a.ContentHash == c.ContentHash {
		t.Error("hash ignored a frontmatter change")
	}
}

func TestDirName(t *testing.T) {
	for in, want := range map[string]string{
		"skills/db-review":  "db-review",
		"skills/db-review/": "db-review",
		"db-review":         "db-review",
	} {
		if got := skill.DirName(in); got != want {
			t.Errorf("DirName(%q) = %q, want %q", in, got, want)
		}
	}
}
