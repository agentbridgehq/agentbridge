package registry_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
	"github.com/agentbridge/agentbridge/internal/adapter/registry"
)

// Regression: removing a whole-package install did nothing at all.
//
// Removal operations carry no Before content, and the no-op test treated a nil
// Before as "nothing to do" — so Apply skipped the removal and the plan
// reported "already up to date". The plugin stayed installed and the CLI said
// it had been removed. Every other client's removal edits a config file and was
// unaffected, which is exactly why this survived: the test suite covered the
// common path and not the one that mattered most.
func TestRemoveActuallyDeletesPackage(t *testing.T) {
	env := fakeMachine(t, "claude-code")
	plugin, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{}); err != nil {
		t.Fatal(err)
	}

	installed := filepath.Join(env.HomeDir, ".claude", "skills", plugin.Name)
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("install did not create the package: %v", err)
	}

	removePlans, err := registry.PlanRemove(env, store, plugin.Name, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if !removePlans[0].Changed() {
		t.Error("a removal that will delete an existing package must not report as unchanged")
	}
	if err := registry.ApplyRemove(store, plugin.Name, removePlans); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Errorf("package still present at %s after removal", installed)
	}
	if len(store.ForPlugin(plugin.Name)) != 0 {
		t.Error("receipt not cleared after removal")
	}
}

// Removing something that is not there must still be a no-op rather than an
// error, since a user may have deleted it by hand.
func TestRemoveMissingPackageIsNoOp(t *testing.T) {
	env := fakeMachine(t, "claude-code")
	plugin, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{}); err != nil {
		t.Fatal(err)
	}

	// The user deletes it themselves.
	if err := os.RemoveAll(filepath.Join(env.HomeDir, ".claude", "skills", plugin.Name)); err != nil {
		t.Fatal(err)
	}

	removePlans, err := registry.PlanRemove(env, store, plugin.Name, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if removePlans[0].Changed() {
		t.Error("removing an absent package should report as unchanged")
	}
	if err := registry.ApplyRemove(store, plugin.Name, removePlans); err != nil {
		t.Errorf("removing an absent package must not error: %v", err)
	}
}

// Spec 9.1 requires every plugin subprocess to receive PLUGIN_ROOT and
// PLUGIN_DATA. Claude Code supplies its own CLAUDE_-prefixed variables and
// knows nothing about the portable names, so a spec-conformant plugin reading
// them would find them unset.
func TestClaudeCodeReceivesSpecEnvVars(t *testing.T) {
	env := fakeMachine(t, "claude-code")
	plugin, src := loadFixture(t, ccFixture)

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Apply(plans[0]); err != nil {
		t.Fatal(err)
	}

	var mcp struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	raw, err := os.ReadFile(filepath.Join(env.HomeDir, ".claude", "skills", plugin.Name, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &mcp); err != nil {
		t.Fatal(err)
	}

	got := mcp.MCPServers["bundled"].Env
	if got["PLUGIN_ROOT"] != "${CLAUDE_PLUGIN_ROOT}" {
		t.Errorf("PLUGIN_ROOT = %q, want it mapped onto Claude Code's placeholder", got["PLUGIN_ROOT"])
	}
	if got["PLUGIN_DATA"] != "${CLAUDE_PLUGIN_DATA}" {
		t.Errorf("PLUGIN_DATA = %q", got["PLUGIN_DATA"])
	}
	// The plugin's own env must survive alongside them.
	if got["DEPLOY_TOKEN"] == "" {
		t.Errorf("the plugin's own env was lost: %v", got)
	}
}

// Same requirement for the clients we write config files for.
func TestNonConformantClientsReceiveSpecEnvVars(t *testing.T) {
	env := fakeMachine(t, "cursor")
	plugin, src := loadFixture(t, ccFixture)

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	written := string(plans[0].Ops[0].After)

	for _, want := range []string{"PLUGIN_ROOT", "PLUGIN_DATA"} {
		if !strings.Contains(written, want) {
			t.Errorf("%s not provided to the subprocess:\n%s", want, written)
		}
	}
	// And resolved, not left as placeholders the client cannot expand.
	if strings.Contains(written, "${PLUGIN_ROOT}") {
		t.Errorf("placeholder left unresolved:\n%s", written)
	}
}

// Spec 7.2.1: an omitted cwd means the plugin root. A client that does not know
// the plugin exists would otherwise use its own working directory.
func TestDefaultWorkingDirectoryIsMadeExplicit(t *testing.T) {
	env := fakeMachine(t, "cursor")
	plugin, src := loadFixture(t, ccFixture)

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		MCPServers map[string]struct {
			Cwd string `json:"cwd"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(plans[0].Ops[0].After, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.MCPServers["deploy-tools.bundled"].Cwd; got != src.Path() {
		t.Errorf("cwd = %q, want the plugin root %q", got, src.Path())
	}
}

// Spec 9.1 requires PLUGIN_DATA to exist and be writable before a subprocess
// starts, and to survive plugin updates — which is why it lives outside every
// client's directory.
func TestPluginDataDirectoryIsCreated(t *testing.T) {
	env := fakeMachine(t, "cursor")
	plugin, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{}); err != nil {
		t.Fatal(err)
	}

	dataDir := registry.PluginDataDir(env, plugin.Name)
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("PLUGIN_DATA was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("PLUGIN_DATA is not a directory")
	}
	if strings.Contains(dataDir, ".cursor") {
		t.Error("PLUGIN_DATA must not live inside a client's directory, or a client reinstall destroys it")
	}
}
