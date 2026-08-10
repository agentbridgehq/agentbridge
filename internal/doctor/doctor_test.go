package doctor_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	"github.com/agentbridge/agentbridge/internal/doctor"
	importreg "github.com/agentbridge/agentbridge/internal/importer/registry"
	"github.com/agentbridge/agentbridge/internal/safepath"
	"github.com/agentbridge/agentbridge/internal/secrets"
)

const ccFixture = "../importer/claudecode/testdata/full"

func fakeMachine(t *testing.T, clients ...string) adapter.Env {
	t.Helper()
	home := t.TempDir()
	for _, c := range clients {
		var dir string
		switch c {
		case "cursor":
			dir = ".cursor"
		case "claude-code":
			dir = ".claude/skills"
		default:
			t.Fatalf("unknown client %q", c)
		}
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return adapter.Env{HomeDir: home, GOOS: "darwin"}
}

// install puts the fixture plugin on the fake machine and returns the store.
func install(t *testing.T, env adapter.Env) *receipt.Store {
	t.Helper()

	res, err := importreg.Open(ccFixture)
	if err != nil {
		t.Fatal(err)
	}
	root, err := safepath.NewRoot(ccFixture)
	if err != nil {
		t.Fatal(err)
	}
	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}

	plans, err := adapterreg.PlanInstall(env, res.Plugin, root, adapterreg.Selection{},
		adapter.PlanOptions{AllowPlaintextSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapterreg.ApplyInstall(env, store, res.Plugin, plans, adapterreg.Provenance{}); err != nil {
		t.Fatal(err)
	}
	return store
}

func run(t *testing.T, env adapter.Env, store *receipt.Store) *doctor.Report {
	t.Helper()
	return doctor.Run(store, doctor.Options{Env: env})
}

func find(r *doctor.Report, status doctor.Status, substr string) *doctor.Check {
	for i := range r.Checks {
		if r.Checks[i].Status == status && strings.Contains(r.Checks[i].Title+r.Checks[i].Detail, substr) {
			return &r.Checks[i]
		}
	}
	return nil
}

// The whole reason the command exists: a client that never received skills
// should say so, rather than leaving the user to wonder.
func TestExplainsWhySkillsAreMissing(t *testing.T) {
	env := fakeMachine(t, "cursor")
	store := install(t, env)

	r := run(t, env, store)
	c := find(r, doctor.Info, "skills are not installed into this client")
	if c == nil {
		t.Fatalf("the most common question is unanswered: %+v", r.Checks)
	}
	if !strings.Contains(c.Detail, "not documented") {
		t.Errorf("the reason should be specific: %q", c.Detail)
	}
}

// A client update, another tool, or a hand edit can remove what was installed.
// The plugin then looks installed and does nothing at all.
func TestDetectsConfigDrift(t *testing.T) {
	env := fakeMachine(t, "cursor")
	store := install(t, env)

	configPath := filepath.Join(env.HomeDir, ".cursor", "mcp.json")
	if err := os.WriteFile(configPath, []byte("{\n  \"mcpServers\": {}\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := run(t, env, store)
	c := find(r, doctor.Fail, "no longer in the configuration")
	if c == nil {
		t.Fatalf("removed entries were not detected: %+v", r.Checks)
	}
	if c.Fix == "" {
		t.Error("a failure without a next action leaves the user where they started")
	}
}

func TestDetectsMissingConfigFile(t *testing.T) {
	env := fakeMachine(t, "cursor")
	store := install(t, env)

	if err := os.Remove(filepath.Join(env.HomeDir, ".cursor", "mcp.json")); err != nil {
		t.Fatal(err)
	}

	r := run(t, env, store)
	if find(r, doctor.Fail, "configuration file is gone") == nil {
		t.Errorf("a deleted config was not detected: %+v", r.Checks)
	}
}

func TestDetectsDeletedPackage(t *testing.T) {
	env := fakeMachine(t, "claude-code")
	store := install(t, env)

	installed := filepath.Join(env.HomeDir, ".claude", "skills", "deploy-tools")
	if err := os.RemoveAll(installed); err != nil {
		t.Fatal(err)
	}

	r := run(t, env, store)
	if find(r, doctor.Fail, "installed package is gone") == nil {
		t.Errorf("a deleted package was not detected: %+v", r.Checks)
	}
}

// An installed copy legitimately differs from its source — the Claude Code
// adapter writes a manifest on top of the copied tree — so a digest comparison
// would report every such install as modified. A check that always fires trains
// people to ignore it.
func TestDoesNotReportNormalInstallsAsModified(t *testing.T) {
	env := fakeMachine(t, "claude-code")
	store := install(t, env)

	r := run(t, env, store)
	if c := find(r, doctor.Warn, "modified"); c != nil {
		t.Errorf("a clean install was reported as modified: %+v", c)
	}
}

// "Nothing happened" is very often an executable that was never installed —
// the config is correct, the client starts, and the server dies immediately.
func TestDetectsUnlaunchableCommand(t *testing.T) {
	env := fakeMachine(t, "cursor")
	store := install(t, env)

	t.Run("absolute path that does not exist", func(t *testing.T) {
		rewriteCommand(t, env, filepath.Join(t.TempDir(), "was-uninstalled"))

		r := run(t, env, store)
		if find(r, doctor.Fail, "does not exist") == nil {
			t.Errorf("a missing binary was not detected: %+v", r.Checks)
		}
	})

	t.Run("bare command not on PATH", func(t *testing.T) {
		rewriteCommand(t, env, "definitely-not-a-real-command-xyz")

		r := run(t, env, store)
		if find(r, doctor.Fail, "not on PATH") == nil {
			t.Errorf("a command missing from PATH was not detected: %+v", r.Checks)
		}
	})
}

// rewriteCommand points the installed stdio server at a different executable,
// standing in for a binary that was uninstalled after the plugin was set up.
func rewriteCommand(t *testing.T, env adapter.Env, command string) {
	t.Helper()
	configPath := filepath.Join(env.HomeDir, ".cursor", "mcp.json")

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]any)
	servers["deploy-tools.bundled"].(map[string]any)["command"] = command

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A referenced secret that was never stored is the failure that looks least
// like a configuration problem: everything is present and correct, and the
// server dies on start with a message the user never sees.
func TestDetectsMissingSecret(t *testing.T) {
	env := fakeMachine(t, "cursor")
	store := install(t, env)

	// Rewrite the installed entry to look like a launcher invocation.
	configPath := filepath.Join(env.HomeDir, ".cursor", "mcp.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]any)
	entry := servers["deploy-tools.bundled"].(map[string]any)
	entry["args"] = []any{"run", "--secret", "TOKEN=acme/never-stored", "--", "npx"}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, out, 0o600); err != nil {
		t.Fatal(err)
	}

	r := &doctor.Report{}
	doctor.CheckSecretReferences(r, store, secrets.NewMemory(), doctor.Options{Env: env})

	c := find(r, doctor.Fail, "referenced secret is not stored")
	if c == nil {
		t.Fatalf("a missing secret was not detected: %+v", r.Checks)
	}
	if !strings.Contains(c.Fix, "agentbridge secret set acme/never-stored") {
		t.Errorf("the fix should be runnable as written: %q", c.Fix)
	}
}

func TestReportsNoClients(t *testing.T) {
	env := adapter.Env{HomeDir: t.TempDir(), GOOS: "darwin"}
	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}

	r := run(t, env, store)
	if find(r, doctor.Fail, "no agent clients detected") == nil {
		t.Errorf("an empty machine should say so: %+v", r.Checks)
	}
}

// Every failure must carry a next action; otherwise the report tells the user
// only that they have a problem, which they already knew.
func TestEveryFailureHasAFix(t *testing.T) {
	env := fakeMachine(t, "cursor")
	store := install(t, env)
	if err := os.Remove(filepath.Join(env.HomeDir, ".cursor", "mcp.json")); err != nil {
		t.Fatal(err)
	}

	r := run(t, env, store)
	for _, c := range r.Checks {
		if c.Status == doctor.Fail && c.Fix == "" {
			t.Errorf("failure without a fix: %+v", c)
		}
	}
}

func TestHealthyReportOnACleanMachine(t *testing.T) {
	env := fakeMachine(t, "cursor")
	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}

	r := run(t, env, store)
	if !r.Healthy() {
		t.Errorf("a machine with a client and no plugins should be healthy: %+v", r.Checks)
	}
}
