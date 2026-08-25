package registry_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/adapter"
	"github.com/agentbridgehq/agentbridge/internal/adapter/receipt"
	"github.com/agentbridgehq/agentbridge/internal/adapter/registry"
	importreg "github.com/agentbridgehq/agentbridge/internal/importer/registry"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// fakeMachine builds a home directory containing the marker directories each
// client is detected by, so detection can be exercised without depending on
// what happens to be installed on the machine running the tests.
func fakeMachine(t *testing.T, clients ...string) adapter.Env {
	t.Helper()
	home := t.TempDir()

	for _, c := range clients {
		var dirs []string
		switch c {
		case "claude-code":
			dirs = []string{".claude", ".claude/skills"}
		case "cursor":
			dirs = []string{".cursor"}
		case "vscode":
			dirs = []string{"Library/Application Support/Code/User"}
		case "codex":
			dirs = []string{".codex"}
		case "gemini-cli":
			dirs = []string{".gemini"}
		default:
			t.Fatalf("unknown client %q", c)
		}
		for _, d := range dirs {
			if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	return adapter.Env{HomeDir: home, GOOS: "darwin"}
}

func loadFixture(t *testing.T, dir string) (*ir.Plugin, *safepath.Root) {
	t.Helper()
	res, err := importreg.Open(dir)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	root, err := safepath.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return res.Plugin, root
}

const ccFixture = "../../importer/claudecode/testdata/full"

// The Claude Code fixture deliberately carries a plaintext DEPLOY_TOKEN, which
// M5 refuses to write by default. Tests exercising anything other than secret
// policy opt in, so the refusal shows up only in the tests that are about it.
var allowPlaintext = adapter.PlanOptions{AllowPlaintextSecrets: true}

func TestDetect(t *testing.T) {
	env := fakeMachine(t, "cursor", "codex")

	found := registry.Detect(env)
	ids := map[string]bool{}
	for _, inst := range found {
		ids[inst.Client.ID] = true
		if inst.Evidence == "" {
			t.Errorf("%s detected with no evidence", inst.Client.ID)
		}
	}
	if !ids["cursor"] || !ids["codex"] {
		t.Errorf("detected %v, want cursor and codex", ids)
	}
	if ids["vscode"] || ids["gemini-cli"] {
		t.Errorf("detected clients that are not installed: %v", ids)
	}
}

// Installing must never partially succeed in silence. Every target reports
// coverage, and the ones that cannot take skills say why.
func TestInstallAcrossClients(t *testing.T) {
	env := fakeMachine(t, "claude-code", "cursor", "vscode", "codex", "gemini-cli")
	plugin, src := loadFixture(t, ccFixture)

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 5 {
		t.Fatalf("got %d plans, want one per client: %v", len(plans), planIDs(plans))
	}

	byClient := map[string]*adapter.Plan{}
	for _, p := range plans {
		byClient[p.Installation.Client.ID] = p
	}

	// Claude Code takes the whole package, so skills reach full coverage.
	cc := byClient["claude-code"]
	if !cc.Fidelity.Skills.Complete() {
		t.Errorf("claude-code skills = %s, want complete", cc.Fidelity.Skills)
	}

	// Everyone else gets MCP only, and must say so rather than reporting
	// success.
	for _, id := range []string{"cursor", "vscode", "codex", "gemini-cli"} {
		p := byClient[id]
		if p.Fidelity.Skills.Carried != 0 {
			t.Errorf("%s claimed to install skills", id)
		}
		if p.Fidelity.Skills.Total == 0 {
			t.Errorf("%s did not count the skills it declined", id)
		}
		if len(p.Fidelity.Losses) == 0 {
			t.Errorf("%s dropped skills without recording a reason", id)
		}
		if p.Fidelity.MCPServers.Carried == 0 {
			t.Errorf("%s installed no MCP servers", id)
		}
	}
}

// VS Code is the odd one out twice over: the container key is "servers", and a
// streamable-http server is spelled "http". Both fail silently if wrong.
func TestVSCodeUsesItsOwnSpelling(t *testing.T) {
	env := fakeMachine(t, "vscode")
	plugin, src := loadFixture(t, ccFixture)

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Apply(plans[0]); err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	readJSON(t, plans[0].Ops[0].Path, &doc)

	servers, ok := doc["servers"].(map[string]any)
	if !ok {
		t.Fatalf("VS Code config has no \"servers\" key: %v", doc)
	}
	if _, wrong := doc["mcpServers"]; wrong {
		t.Error("wrote \"mcpServers\", which VS Code does not read")
	}

	entry, ok := servers["deploy-tools.registry"].(map[string]any)
	if !ok {
		t.Fatalf("remote server missing: %v", servers)
	}
	if entry["type"] != "http" {
		t.Errorf("type = %v, want \"http\" (VS Code's spelling of streamable-http)", entry["type"])
	}
}

// Placeholders are a contract between a plugin and a conformant client. No
// client we write config for expands them, so leaving one in produces a config
// that validates and then fails to launch.
func TestPlaceholdersAreResolvedForNonConformantTargets(t *testing.T) {
	env := fakeMachine(t, "cursor")
	plugin, src := loadFixture(t, ccFixture)

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	written := string(plans[0].Ops[0].After)

	if strings.Contains(written, "${PLUGIN_ROOT}") || strings.Contains(written, "${PLUGIN_DATA}") {
		t.Errorf("unresolved placeholder written to a client config:\n%s", written)
	}
	// The path is embedded in JSON, so on Windows its separators arrive
	// escaped. Compare against the encoded form rather than the raw string.
	encoded, err := json.Marshal(src.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(written, strings.Trim(string(encoded), `"`)) {
		t.Errorf("plugin root was not resolved to an absolute path:\n%s", written)
	}
}

// Codex is TOML, so its adapter owns a marked block and must not disturb
// anything the user wrote around it.
func TestCodexPreservesExistingTOML(t *testing.T) {
	env := fakeMachine(t, "codex")
	const existing = "model = \"gpt-5\"\n\n[mcp_servers.mine]\ncommand = \"node\"\n"
	configPath := filepath.Join(env.HomeDir, ".codex", "config.toml")
	if err := os.WriteFile(configPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	plugin, src := loadFixture(t, ccFixture)
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Apply(plans[0]); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, configPath)
	if !strings.HasPrefix(got, existing) {
		t.Errorf("existing TOML was modified:\n--- want prefix ---\n%s\n--- got ---\n%s", existing, got)
	}
	if !strings.Contains(got, `[mcp_servers."deploy-tools.bundled"]`) {
		t.Errorf("server not written:\n%s", got)
	}
}

// Re-running an install must be a no-op rather than rewriting identical bytes,
// which would churn mtimes and wake every client's file watcher.
func TestInstallIsIdempotent(t *testing.T) {
	env := fakeMachine(t, "cursor")
	plugin, src := loadFixture(t, ccFixture)

	first, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Apply(first[0]); err != nil {
		t.Fatal(err)
	}

	second, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Changed() {
		t.Errorf("re-install proposed a change:\n%s", adapter.Diff(second[0].Ops[0]))
	}
}

// The uninstall contract: what we added goes, what the user wrote stays.
func TestRemoveLeavesUserEntriesAlone(t *testing.T) {
	env := fakeMachine(t, "cursor")

	configPath := filepath.Join(env.HomeDir, ".cursor", "mcp.json")
	const userConfig = `{
  // my own server, hands off
  "mcpServers": {
    "mine": { "command": "node", "args": ["own.js"] }
  }
}
`
	if err := os.WriteFile(configPath, []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}

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

	afterInstall := readFile(t, configPath)
	if !strings.Contains(afterInstall, "deploy-tools.bundled") {
		t.Fatalf("install wrote nothing:\n%s", afterInstall)
	}

	removePlans, err := registry.PlanRemove(env, store, plugin.Name, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ApplyRemove(env, store, plugin.Name, removePlans); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, configPath)
	if strings.Contains(got, "deploy-tools") {
		t.Errorf("our entries survived removal:\n%s", got)
	}
	if !strings.Contains(got, `"mine"`) {
		t.Errorf("the user's own server was deleted:\n%s", got)
	}
	if !strings.Contains(got, "hands off") {
		t.Errorf("the user's comment was deleted:\n%s", got)
	}
	// Install then remove should restore the file exactly.
	if got != userConfig {
		t.Errorf("file not restored:\n--- want ---\n%s\n--- got ---\n%s", userConfig, got)
	}
}

// Removal is driven by receipts, never by pattern-matching. A user entry that
// happens to share our naming convention must survive.
func TestRemoveIgnoresLookalikeUserEntries(t *testing.T) {
	env := fakeMachine(t, "cursor")
	configPath := filepath.Join(env.HomeDir, ".cursor", "mcp.json")

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

	// A key that looks exactly like one of ours, but which we did not write.
	var doc map[string]any
	readJSON(t, configPath, &doc)
	doc["mcpServers"].(map[string]any)["deploy-tools.hand-written"] = map[string]any{"command": "mine"}
	writeJSON(t, configPath, doc)

	removePlans, err := registry.PlanRemove(env, store, plugin.Name, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ApplyRemove(env, store, plugin.Name, removePlans); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, configPath)
	if !strings.Contains(got, "deploy-tools.hand-written") {
		t.Errorf("removal deleted an entry we never wrote:\n%s", got)
	}
	if strings.Contains(got, "deploy-tools.bundled") {
		t.Errorf("our own entry survived:\n%s", got)
	}
}

// The full circle: a Claude Code plugin, imported to the IR, installed back
// into Claude Code, and re-imported. Skills and servers must survive, and the
// placeholder rewrite must reverse cleanly — the importer turns
// ${CLAUDE_PLUGIN_ROOT}/bin/x into ./bin/x, and the adapter must turn it back.
func TestClaudeCodeRoundTrip(t *testing.T) {
	env := fakeMachine(t, "claude-code")
	plugin, src := loadFixture(t, ccFixture)

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Apply(plans[0]); err != nil {
		t.Fatal(err)
	}

	installed := filepath.Join(env.HomeDir, ".claude", "skills", plugin.Name)

	var mcp struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	readJSON(t, filepath.Join(installed, ".mcp.json"), &mcp)

	bundled, ok := mcp.MCPServers["bundled"]
	if !ok {
		t.Fatalf("bundled server missing: %v", mcp.MCPServers)
	}
	if got := bundled["command"]; got != "${CLAUDE_PLUGIN_ROOT}/bin/deploy-server" {
		t.Errorf("command = %v, want the Claude Code placeholder form restored", got)
	}

	// Re-import the installed copy and compare against the original.
	reimported, err := importreg.Open(installed)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}

	if reimported.Plugin.Name != plugin.Name {
		t.Errorf("name = %q, want %q", reimported.Plugin.Name, plugin.Name)
	}
	if len(reimported.Plugin.Skills) != len(plugin.Skills) {
		t.Errorf("skills = %d, want %d", len(reimported.Plugin.Skills), len(plugin.Skills))
	}
	for _, want := range plugin.Skills {
		got, ok := reimported.Plugin.Skill(want.Name)
		if !ok {
			t.Errorf("skill %q lost in the round trip", want.Name)
			continue
		}
		if got.ContentHash != want.ContentHash {
			t.Errorf("skill %q content changed", want.Name)
		}
	}
	for _, want := range plugin.MCPServers {
		got, ok := reimported.Plugin.MCPServer(want.Name)
		if !ok {
			t.Errorf("server %q lost in the round trip", want.Name)
			continue
		}
		if got.ContentHash != want.ContentHash {
			t.Errorf("server %q changed across the round trip:\n  before %+v\n  after  %+v", want.Name, want, got)
		}
	}

	// Components the portable format has no field for must survive, because
	// the install copies the tree rather than reconstructing from the IR.
	for _, path := range []string{"agents/reviewer.md", "hooks/hooks.json", "bin/deploy-server"} {
		if _, err := os.Stat(filepath.Join(installed, path)); err != nil {
			t.Errorf("%s did not survive the install: %v", path, err)
		}
	}
}

func TestSelectionFilters(t *testing.T) {
	env := fakeMachine(t, "cursor", "codex", "gemini-cli")
	plugin, src := loadFixture(t, ccFixture)

	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{Clients: []string{"codex"}}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Installation.Client.ID != "codex" {
		t.Errorf("selection ignored: %v", planIDs(plans))
	}

	if _, err := registry.PlanInstall(env, plugin, src, registry.Selection{Clients: []string{"nope"}}, allowPlaintext); err == nil {
		t.Error("selecting an unknown client should fail rather than silently install nothing")
	}
}

func TestRemoveUnknownPluginFails(t *testing.T) {
	env := fakeMachine(t, "cursor")
	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.PlanRemove(env, store, "never-installed", registry.Selection{}); err == nil {
		t.Error("expected an error for a plugin that was never installed")
	}
}

// ---------------------------------------------------------------- helpers

func planIDs(plans []*adapter.Plan) []string {
	out := make([]string, len(plans))
	for i, p := range plans {
		out[i] = p.Installation.Client.ID
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
