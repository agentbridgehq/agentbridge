package registry_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/adapter/receipt"
	"github.com/agentbridgehq/agentbridge/internal/adapter/registry"
)

// installOpencode installs the shared fixture into a machine that has only
// opencode on it, and returns the resolved config together with the home.
func installOpencode(t *testing.T) (map[string]any, string, string) {
	t.Helper()
	env := fakeMachine(t, "opencode")
	plugin, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(env.HomeDir, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("opencode config was not written: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("opencode config is not valid JSON: %v", err)
	}
	return cfg, env.HomeDir, plugin.Name
}

// server returns the managed entry for one of the fixture's servers.
func server(t *testing.T, cfg map[string]any, name string) map[string]any {
	t.Helper()
	mcp, ok := cfg["mcp"].(map[string]any)
	if !ok {
		t.Fatalf(`config has no "mcp" object; opencode reads servers from "mcp", not "mcpServers": %v`, cfg)
	}
	entry, ok := mcp[name].(map[string]any)
	if !ok {
		t.Fatalf("no server %q in %v", name, mcp)
	}
	return entry
}

// opencode accepts an "env" key without complaint and then discards it, so a
// server configured from opencode's own documented example starts with none of
// its environment. That silently costs a plugin PLUGIN_ROOT and PLUGIN_DATA,
// which §9.1 requires it to receive. The published schema calls the key
// "environment"; this test is the reason the adapter does not follow the prose.
func TestOpencodeEnvironmentKeyIsNotEnv(t *testing.T) {
	cfg, _, name := installOpencode(t)
	entry := server(t, cfg, name+".bundled")

	if _, wrong := entry["env"]; wrong {
		t.Error(`wrote "env", which opencode accepts and then silently drops; the key is "environment"`)
	}
	environment, ok := entry["environment"].(map[string]any)
	if !ok {
		t.Fatalf(`no "environment" object on the server entry: %v`, entry)
	}
	for _, required := range []string{"PLUGIN_ROOT", "PLUGIN_DATA"} {
		if environment[required] == nil {
			t.Errorf("environment is missing %s, which spec 9.1 requires every plugin subprocess to receive", required)
		}
	}
}

// opencode takes the executable and its arguments as one array, and requires a
// type discriminator of "local" or "remote" rather than the portable
// stdio/streamable-http spellings.
func TestOpencodeServerShape(t *testing.T) {
	cfg, _, name := installOpencode(t)

	stdio := server(t, cfg, name+".bundled")
	if got := stdio["type"]; got != "local" {
		t.Errorf(`stdio transport encoded as type %v, want "local"`, got)
	}
	if _, split := stdio["args"]; split {
		t.Error(`wrote a separate "args" key; opencode carries the command and its arguments in one array`)
	}
	command, ok := stdio["command"].([]any)
	if !ok {
		t.Fatalf("command is not an array: %v", stdio["command"])
	}
	if len(command) != 3 {
		t.Errorf("command = %v, want the executable followed by its two arguments", command)
	}

	remote := server(t, cfg, name+".registry")
	if got := remote["type"]; got != "remote" {
		t.Errorf(`http transport encoded as type %v, want "remote"`, got)
	}
	if remote["url"] == nil {
		t.Error("remote server has no url")
	}
}

// opencode's skill loader scans configured skill directories recursively for
// **/SKILL.md, so a plugin package installed whole is discovered — which makes
// it the only client besides Claude Code that takes skills at a location its
// vendor documents.
func TestOpencodeCarriesSkills(t *testing.T) {
	_, home, name := installOpencode(t)

	skill := filepath.Join(home, ".config", "opencode", "skills", name, "skills", "deploy-check", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("skill was not installed where opencode scans for it: %v", err)
	}
}

// Removal dispatches on the config keys a receipt recorded, so a plugin that
// installed a server never reaches PlanRemove. If the package removal lived
// only there, every plugin with both skills and servers — which is most of
// them — would leave its skills behind and go on being loaded after uninstall.
func TestOpencodeRemoveTakesBothConfigAndPackage(t *testing.T) {
	env := fakeMachine(t, "opencode")
	plugin, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{}); err != nil {
		t.Fatal(err)
	}

	pkg := filepath.Join(env.HomeDir, ".config", "opencode", "skills", plugin.Name)
	if _, err := os.Stat(pkg); err != nil {
		t.Fatalf("install did not create the package: %v", err)
	}

	removePlans, err := registry.PlanRemove(env, store, plugin.Name, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ApplyRemove(env, store, plugin.Name, removePlans); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(pkg); !os.IsNotExist(err) {
		t.Errorf("package still present at %s after removal", pkg)
	}
	raw, err := os.ReadFile(filepath.Join(env.HomeDir, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if mcp, ok := cfg["mcp"].(map[string]any); ok && len(mcp) != 0 {
		t.Errorf("managed server entries survived removal: %v", mcp)
	}
}
