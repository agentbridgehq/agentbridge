package configedit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/configedit"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The single most important property in this package. VS Code ships its
// configuration with comments and users add their own; a tool that eats them
// on first install does not get a second one.
func TestJSONPreservesCommentsAndFormatting(t *testing.T) {
	const original = `{
  // Servers I set up by hand. Do not remove.
  "servers": {
    /* the important one */
    "my-own": {
      "command": "node",   // trailing comment
      "args": ["server.js"]
    }
  },
  "unrelated": true
}
`
	path := writeTemp(t, "mcp.json", original)

	doc, err := configedit.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set([]string{"servers", "added"}, map[string]any{"command": "npx"}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	got := string(out)
	for _, want := range []string{
		"// Servers I set up by hand. Do not remove.",
		"/* the important one */",
		"// trailing comment",
		`"my-own"`,
		`"unrelated": true`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q from the file:\n%s", want, got)
		}
	}
	if !strings.Contains(got, `"added"`) {
		t.Errorf("new entry not written:\n%s", got)
	}
	// Key order is a deliberate choice by whoever wrote the file.
	if strings.Index(got, `"my-own"`) > strings.Index(got, `"added"`) {
		t.Errorf("existing key was reordered:\n%s", got)
	}
}

// Setting a value and removing it again must leave the file exactly as it was.
// If this fails, some formatting detail is being normalized on the way through.
func TestJSONSetThenDeleteIsByteIdentical(t *testing.T) {
	const original = `{
  // keep me
  "servers": {
    "existing": { "command": "node" }
  }
}
`
	path := writeTemp(t, "mcp.json", original)

	doc, err := configedit.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set([]string{"servers", "temp"}, map[string]any{"command": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := doc.Delete([]string{"servers", "temp"}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != original {
		t.Errorf("round trip changed the file:\n--- want ---\n%s\n--- got ---\n%s", original, out)
	}
}

func TestJSONCreatesMissingParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.json")

	doc, err := configedit.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Existed() {
		t.Error("a missing file should not report as existing")
	}
	if err := doc.Set([]string{"a", "b", "c"}, "v"); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"a"`, `"b"`, `"c"`, `"v"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Deleting something that is not there must succeed. Uninstall has to be
// idempotent, not least because the user may have removed the entry by hand.
func TestJSONDeleteMissingIsNoOp(t *testing.T) {
	path := writeTemp(t, "mcp.json", "{\n  \"servers\": {}\n}\n")

	doc, err := configedit.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Delete([]string{"servers", "nope"}); err != nil {
		t.Errorf("deleting an absent key returned %v", err)
	}
	if err := doc.Delete([]string{"nothing", "here"}); err != nil {
		t.Errorf("deleting under an absent parent returned %v", err)
	}
}

// Keys containing "/" or "~" are legal JSON and must not break the pointer
// used to address them.
func TestJSONHandlesPointerEscaping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "escape.json")

	doc, err := configedit.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a/b", "c~d", "e~/f"} {
		if err := doc.Set([]string{"servers", key}, "v"); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
		ok, err := doc.Has([]string{"servers", key})
		if err != nil || !ok {
			t.Errorf("Has(%q) = %v, %v", key, ok, err)
		}
		if err := doc.Delete([]string{"servers", key}); err != nil {
			t.Errorf("Delete(%q): %v", key, err)
		}
	}
}

func TestJSONEmptyFile(t *testing.T) {
	path := writeTemp(t, "empty.json", "")

	doc, err := configedit.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set([]string{"servers", "x"}, map[string]any{"command": "y"}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"servers"`) {
		t.Errorf("got:\n%s", out)
	}
}

// ------------------------------------------------------------- block editor

func TestBlockLeavesUserContentUntouched(t *testing.T) {
	const original = `# My Codex config
model = "gpt-5"

[mcp_servers.mine]
command = "node"
args = ["server.js"]
`
	path := writeTemp(t, "config.toml", original)

	doc, err := configedit.LoadBlock(path)
	if err != nil {
		t.Fatal(err)
	}
	doc.SetSection(`[mcp_servers."acme.db"]`, []string{
		`[mcp_servers."acme.db"]`,
		`command = "npx"`,
	})
	got := string(doc.Bytes())

	if !strings.HasPrefix(got, original) {
		t.Errorf("user content was modified:\n--- want prefix ---\n%s\n--- got ---\n%s", original, got)
	}
	for _, want := range []string{configedit.BlockBegin, `[mcp_servers."acme.db"]`, configedit.BlockEnd} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestBlockUpsertAndRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	doc, err := configedit.LoadBlock(path)
	if err != nil {
		t.Fatal(err)
	}
	doc.SetSection(`[mcp_servers."a.one"]`, []string{`[mcp_servers."a.one"]`, `command = "one"`})
	doc.SetSection(`[mcp_servers."a.two"]`, []string{`[mcp_servers."a.two"]`, `command = "two"`})

	if err := os.WriteFile(path, doc.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := configedit.LoadBlock(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Sections()) != 2 {
		t.Fatalf("sections = %v, want 2", reopened.Sections())
	}

	reopened.DeleteSection(`[mcp_servers."a.one"]`)
	got := string(reopened.Bytes())
	if strings.Contains(got, `"a.one"`) {
		t.Errorf("deleted section still present:\n%s", got)
	}
	if !strings.Contains(got, `"a.two"`) {
		t.Errorf("surviving section was lost:\n%s", got)
	}
}

// An uninstall should leave no trace, not an empty labelled region.
func TestBlockRemovedEntirelyWhenEmpty(t *testing.T) {
	const original = "model = \"gpt-5\"\n"
	path := writeTemp(t, "config.toml", original)

	doc, err := configedit.LoadBlock(path)
	if err != nil {
		t.Fatal(err)
	}
	doc.SetSection(`[mcp_servers."a.one"]`, []string{`[mcp_servers."a.one"]`, `command = "one"`})
	if err := os.WriteFile(path, doc.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := configedit.LoadBlock(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.DeleteSection(`[mcp_servers."a.one"]`)

	got := string(reopened.Bytes())
	if strings.Contains(got, configedit.BlockBegin) {
		t.Errorf("empty block left behind:\n%s", got)
	}
	if got != original {
		t.Errorf("file not restored:\n--- want ---\n%s\n--- got ---\n%s", original, got)
	}
}

// A half-written block means a previous run died or someone edited by hand.
// Guessing where it was meant to end risks deleting the user's content.
func TestBlockUnterminatedIsRefused(t *testing.T) {
	path := writeTemp(t, "config.toml", "model = \"x\"\n"+configedit.BlockBegin+"\n[mcp_servers.a]\n")

	if _, err := configedit.LoadBlock(path); err == nil {
		t.Fatal("expected an error for an unterminated managed block")
	}
}

// A marker appearing inside a user's string value must not be mistaken for the
// real thing.
func TestBlockMarkerMustBeOwnLine(t *testing.T) {
	content := "note = \"" + configedit.BlockBegin + " inside a string\"\n"
	path := writeTemp(t, "config.toml", content)

	doc, err := configedit.LoadBlock(path)
	if err != nil {
		t.Fatalf("marker inside a string was treated as a real marker: %v", err)
	}
	if len(doc.Sections()) != 0 {
		t.Errorf("sections = %v, want none", doc.Sections())
	}
}

func TestTOMLEncoding(t *testing.T) {
	if got := configedit.TOMLString(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("TOMLString = %s", got)
	}
	if got := configedit.TOMLStringArray([]string{"a", "b"}); got != `["a", "b"]` {
		t.Errorf("TOMLStringArray = %s", got)
	}
	// Sorted, so the file does not churn on Go's map ordering.
	if got := configedit.TOMLInlineTable(map[string]string{"z": "1", "a": "2"}); got != `{ "a" = "2", "z" = "1" }` {
		t.Errorf("TOMLInlineTable = %s", got)
	}
}
