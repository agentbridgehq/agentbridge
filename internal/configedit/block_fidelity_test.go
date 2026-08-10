package configedit_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentbridge/agentbridge/internal/configedit"
)

// Regression: Original() re-rendered the document instead of returning the
// bytes it read.
//
// Renderer output is normalized — blank lines collapsed, spacing regularized —
// so a file a human had touched came back different from what was on disk. Two
// things break as a result: the diff shown by --dry-run claims we are changing
// lines we are not, and the no-op check compares against the wrong baseline.
// Both undermine the one promise this package exists to keep.
func TestBlockOriginalIsVerbatim(t *testing.T) {
	content := "model = \"x\"\n\n\n" +
		configedit.BlockBegin + "\n\n" +
		"[mcp_servers.\"a.one\"]\ncommand = \"one\"\n\n\n" +
		configedit.BlockEnd + "\n\ntrailing = true\n"

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := configedit.LoadBlock(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(doc.Original()) != content {
		t.Errorf("Original() is not the file's bytes:\n--- file ---\n%q\n--- got ---\n%q",
			content, doc.Original())
	}
}

func TestBlockOriginalIsNilForMissingFile(t *testing.T) {
	doc, err := configedit.LoadBlock(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Original() != nil {
		t.Errorf("Original() = %q, want nil for a file that does not exist", doc.Original())
	}
	if doc.Existed() {
		t.Error("Existed() should be false")
	}
}

// A managed block a human has reflowed must still round-trip its sections, even
// though re-rendering will normalize the layout. Losing an entry here would
// orphan a server in a client's config with nothing left that knows to remove
// it.
func TestBlockSurvivesHumanReflow(t *testing.T) {
	content := configedit.BlockBegin + "\n\n" +
		"[mcp_servers.\"a.one\"]\n\ncommand = \"one\"\n\n" +
		"[mcp_servers.\"a.two\"]\ncommand = \"two\"\n\n" +
		configedit.BlockEnd + "\n"

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := configedit.LoadBlock(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(doc.Sections()); got != 2 {
		t.Fatalf("sections = %d (%v), want 2", got, doc.Sections())
	}
}
