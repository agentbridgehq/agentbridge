package configedit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/configedit"
)

// Preserving the user's formatting is only half the job. What we add has to be
// legible too, or the file is technically intact and practically ruined.
func TestJSONInsertedValueIsFormatted(t *testing.T) {
	const original = `{
  "mcpServers": {
    "mine": { "command": "node" }
  }
}
`
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := configedit.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set([]string{"mcpServers", "added"}, map[string]any{
		"command": "npx",
		"args":    []string{"a", "b"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// The new member starts its own line at the siblings' indent...
	if !strings.Contains(got, "\n    \"added\": {") {
		t.Errorf("new member is not indented like its siblings:\n%s", got)
	}
	// ...and its contents are expanded rather than crammed onto one line.
	if !strings.Contains(got, "\n      \"command\": \"npx\"") {
		t.Errorf("inserted value was not formatted:\n%s", got)
	}
	if strings.Contains(got, `{"command":"npx"`) {
		t.Errorf("inserted value is compact JSON:\n%s", got)
	}
}

// A file indented with tabs should get tabs back, not our preference.
func TestJSONAdoptsExistingIndentStyle(t *testing.T) {
	const original = "{\n\t\"mcpServers\": {\n\t\t\"mine\": { \"command\": \"node\" }\n\t}\n}\n"
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := configedit.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set([]string{"mcpServers", "added"}, map[string]any{"command": "npx"}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "\n\t\t\"added\":") {
		t.Errorf("tab indentation not adopted:\n%s", out)
	}
}

// Creating a config from nothing must still produce something a human can read
// and edit afterwards.
func TestJSONNewFileIsReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")

	doc, err := configedit.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set([]string{"servers", "db"}, map[string]any{"command": "npx"}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	got := string(out)
	if strings.Count(got, "\n") < 3 {
		t.Errorf("a created config should not be one long line:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("file does not end with a newline:\n%q", got)
	}
}
