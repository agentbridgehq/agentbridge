package registry_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/adapter/receipt"
	"github.com/agentbridgehq/agentbridge/internal/adapter/registry"
)

// installForVSCode installs the fixture on a machine that has only VS Code.
func installForVSCode(t *testing.T) (home, settings string, store *receipt.Store, plugin string) {
	t.Helper()
	env := fakeMachine(t, "vscode")
	p, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, p, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, p, plans, registry.Provenance{}); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(env.HomeDir, "Library", "Application Support", "Code", "User")
	return env.HomeDir, filepath.Join(user, "settings.json"), store, p.Name
}

// VS Code scans one level below each configured root, so the package cannot be
// dropped into a shared skills directory the way every other client takes it —
// the skills would sit one level too deep and none would be found. Registering
// the package's own skills/ directory as a root puts the skills exactly where
// the scan looks.
func TestVSCodeRegistersTheSkillsDirectoryItWillScan(t *testing.T) {
	home, settings, _, name := installForVSCode(t)

	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("settings.json was not written: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	locations, ok := doc["chat.agentSkillsLocations"].(map[string]any)
	if !ok {
		t.Fatalf("no chat.agentSkillsLocations in %s", raw)
	}
	if len(locations) != 1 {
		t.Fatalf("want exactly one registered location, got %v", locations)
	}

	for key, enabled := range locations {
		if enabled != true {
			t.Errorf("location %q registered as %v, want true", key, enabled)
		}
		// VS Code validates these keys against a pattern that rejects an
		// absolute path outright and permits a leading "~/". A key it rejects
		// is dropped silently and the skills never appear.
		if !strings.HasPrefix(key, "~/") {
			t.Errorf("location %q must be written home-relative", key)
		}
		if strings.ContainsAny(key, `\*?[]{}`) {
			t.Errorf("location %q contains a character the setting's pattern refuses", key)
		}
		if !strings.HasSuffix(key, "/skills") {
			t.Errorf("location %q should be the package's skills directory", key)
		}

		// And the layout under it must be what the one-level scan expects:
		// each immediate child holds SKILL.md directly.
		root := filepath.Join(home, strings.TrimPrefix(key, "~/"))
		children, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("registered location does not exist: %v", err)
		}
		found := 0
		for _, c := range children {
			if !c.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, c.Name(), "SKILL.md")); err == nil {
				found++
			}
		}
		if found == 0 {
			t.Errorf("no <child>/SKILL.md under %s; VS Code looks exactly one level down", root)
		}
	}
	_ = name
}

// The setting lives in settings.json while MCP servers live in mcp.json, so the
// receipt records a second configuration file. Removal has to take the
// registration back out of it, and leave everything the user wrote alone.
func TestVSCodeRemovalRestoresSettingsExactly(t *testing.T) {
	env := fakeMachine(t, "vscode")
	user := filepath.Join(env.HomeDir, "Library", "Application Support", "Code", "User")
	settings := filepath.Join(user, "settings.json")

	// Four-space indentation and a comment, as VS Code ships it.
	before := "{\n    // mine, keep this\n    \"editor.fontSize\": 13\n}\n"
	if err := os.WriteFile(settings, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	p, src := loadFixture(t, ccFixture)
	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, p, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, p, plans, registry.Provenance{}); err != nil {
		t.Fatal(err)
	}

	installed, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installed), "mine, keep this") {
		t.Errorf("the user's comment did not survive the install:\n%s", installed)
	}
	// The created object must follow the file's own indentation, not a
	// hardcoded two spaces.
	if !strings.Contains(string(installed), "\n        \"~/") {
		t.Errorf("registered location is not indented like the rest of the file:\n%s", installed)
	}

	removePlans, err := registry.PlanRemove(env, store, p.Name, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ApplyRemove(env, store, p.Name, removePlans); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Errorf("settings.json not restored\n--- before ---\n%q\n--- after ---\n%q", before, string(after))
	}
}
