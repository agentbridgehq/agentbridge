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
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
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
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
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
	if _, err := registry.ApplyRemove(env, store, plugin.Name, removePlans); err != nil {
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
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
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
	if _, err := registry.ApplyRemove(env, store, plugin.Name, removePlans); err != nil {
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

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
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

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
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

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
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
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
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

// An uninstall must account for everything it leaves behind.
//
// PLUGIN_DATA is created by install because §9.1 promises a plugin's subprocess
// a writable directory. §9.1 also requires it to survive *updates* and says
// nothing about removal, which left a gap: an uninstall silently left the
// directory on disk. For every plugin that never ran a server — which is every
// skills-only plugin — that meant an empty directory left by a tool whose
// documented promise is to remove exactly what it installed and nothing else.
func TestRemoveDisposesOfTheDataDirectory(t *testing.T) {
	t.Run("an empty data directory is removed", func(t *testing.T) {
		env := fakeMachine(t, "claude-code")
		plugin, src := loadFixture(t, ccFixture)
		store := openStore(t, env)

		installFixture(t, env, store, plugin, src)

		dataDir := registry.PluginDataDir(env, plugin.Name)
		if _, err := os.Stat(dataDir); err != nil {
			t.Fatalf("install did not create the data directory: %v", err)
		}

		kept := removeFixture(t, env, store, plugin.Name)
		if kept != "" {
			t.Errorf("an empty data directory was reported as kept: %s", kept)
		}
		if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
			t.Errorf("empty data directory left behind at %s", dataDir)
		}
	})

	t.Run("a data directory with contents is kept and reported", func(t *testing.T) {
		env := fakeMachine(t, "claude-code")
		plugin, src := loadFixture(t, ccFixture)
		store := openStore(t, env)

		installFixture(t, env, store, plugin, src)

		// What a server would have written while running.
		dataDir := registry.PluginDataDir(env, plugin.Name)
		stateFile := filepath.Join(dataDir, "state.db")
		if err := os.WriteFile(stateFile, []byte("user data"), 0o644); err != nil {
			t.Fatal(err)
		}

		kept := removeFixture(t, env, store, plugin.Name)
		if kept != dataDir {
			t.Errorf("kept = %q, want the data directory %q", kept, dataDir)
		}
		// Keeping it is the right call — it is the user's data and we did not
		// write it — but only if they are told, which is what `kept` is for.
		if body, err := os.ReadFile(stateFile); err != nil || string(body) != "user data" {
			t.Errorf("a plugin's own data was destroyed by uninstall: %v", err)
		}
	})

	t.Run("data survives while another client still has the plugin", func(t *testing.T) {
		env := fakeMachine(t, "claude-code", "cursor")
		plugin, src := loadFixture(t, ccFixture)
		store := openStore(t, env)

		installFixture(t, env, store, plugin, src)
		dataDir := registry.PluginDataDir(env, plugin.Name)

		// Remove from one client only. The other still has it installed, so the
		// data directory is still in use and must not be touched.
		plans, err := registry.PlanRemove(env, store, plugin.Name,
			registry.Selection{Clients: []string{"claude-code"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := registry.ApplyRemove(env, store, plugin.Name, plans); err != nil {
			t.Fatal(err)
		}

		if len(store.ForPlugin(plugin.Name)) == 0 {
			t.Skip("the fixture did not install into a second client")
		}
		if _, err := os.Stat(dataDir); err != nil {
			t.Errorf("data removed while another client still has the plugin: %v", err)
		}
	})
}

func openStore(t *testing.T, env adapter.Env) *receipt.Store {
	t.Helper()
	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func installFixture(t *testing.T, env adapter.Env, store *receipt.Store, plugin *ir.Plugin, src *safepath.Root) {
	t.Helper()
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{}); err != nil {
		t.Fatal(err)
	}
}

func removeFixture(t *testing.T, env adapter.Env, store *receipt.Store, name string) string {
	t.Helper()
	plans, err := registry.PlanRemove(env, store, name, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := registry.ApplyRemove(env, store, name, plans)
	if err != nil {
		t.Fatal(err)
	}
	return kept
}
