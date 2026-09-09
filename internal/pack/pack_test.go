package pack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	claudecodeimp "github.com/agentbridgehq/agentbridge/internal/importer/claudecode"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// plugin returns a plugin exercising the parts of packing that can go wrong:
// a skill, a stdio server with a relative command, and both placeholders in
// the three places a client is required to expand them.
func plugin() *ir.Plugin {
	return &ir.Plugin{
		Name:        "example",
		Version:     "1.2.3",
		Description: "a plugin",
		Skills: []ir.Skill{
			{Name: "alpha", Kind: ir.SkillDirectory, Dir: "skills/alpha", Entrypoint: "skills/alpha/SKILL.md"},
		},
		MCPServers: []ir.MCPServer{{
			Name:      "db",
			Transport: ir.TransportStdio,
			Command:   "./bin/server",
			Args:      []string{"--root", ir.PlaceholderPluginRoot},
			Env:       map[string]string{"CACHE": ir.PlaceholderPluginData + "/cache"},
		}},
	}
}

func build(t *testing.T, p *ir.Plugin, clients ...string) map[string][]byte {
	t.Helper()
	files, _, err := Build(p, clients)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range files {
		out[f.Path] = f.Content
	}
	return out
}

// TestPackedPackageCarriesEveryVendorManifest is the claim the command makes.
func TestPackedPackageCarriesEveryVendorManifest(t *testing.T) {
	got := build(t, plugin())
	for _, want := range []string{
		".claude-plugin/plugin.json",
		".codex-plugin/plugin.json",
		".cursor-plugin/plugin.json",
		".mcp.json",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("pack did not write %s", want)
		}
	}
}

// TestClaudeCodeGetsItsOwnPlaceholderSpelling is the reason pack writes a
// second .mcp.json instead of pointing Claude Code at the package's own.
//
// The failure this prevents is silent. Claude Code passes an unrecognized
// placeholder through as literal text, so a package that kept ${PLUGIN_ROOT}
// would produce a server whose command contains a dollar sign and a brace, and
// the only symptom would be a plugin that does not work.
func TestClaudeCodeGetsItsOwnPlaceholderSpelling(t *testing.T) {
	raw := build(t, plugin())[".mcp.json"]
	if raw == nil {
		t.Fatal("no .mcp.json written")
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			Cwd     string            `json:"cwd"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	srv, ok := doc.MCPServers["db"]
	if !ok {
		t.Fatal("server db missing")
	}

	// The portable spelling must not survive anywhere Claude Code expands.
	surfaces := append([]string{srv.Command, srv.Cwd}, srv.Args...)
	for k, v := range srv.Env {
		// PLUGIN_ROOT and PLUGIN_DATA are env *names* required by §9.1; their
		// values are the ones that must be translated.
		surfaces = append(surfaces, k+"="+v)
	}
	for _, s := range surfaces {
		if strings.Contains(s, ir.PlaceholderPluginRoot) || strings.Contains(s, ir.PlaceholderPluginData) {
			t.Errorf("portable placeholder survived into Claude Code's dialect: %q", s)
		}
	}

	if want := "${CLAUDE_PLUGIN_ROOT}/bin/server"; srv.Command != want {
		t.Errorf("command = %q, want %q", srv.Command, want)
	}
	// §9.1: every plugin subprocess receives these two names.
	if srv.Env["PLUGIN_ROOT"] == "" || srv.Env["PLUGIN_DATA"] == "" {
		t.Errorf("§9.1 env names missing: %v", srv.Env)
	}
	// §7.2.1's default working directory.
	if srv.Cwd == "" {
		t.Error("cwd was not defaulted")
	}
}

// TestNoServersMeansNoMCPFile keeps pack from writing an empty file into a
// package that never asked for one. A skills-only plugin should gain three
// manifests and nothing else.
func TestNoServersMeansNoMCPFile(t *testing.T) {
	p := plugin()
	p.MCPServers = nil
	got := build(t, p)
	if _, ok := got[".mcp.json"]; ok {
		t.Error("wrote .mcp.json for a plugin with no servers")
	}
	if len(got) != 3 {
		t.Errorf("wrote %d files, want 3", len(got))
	}
}

// TestPackingIsIdempotent is the property that makes the output committable:
// running pack on an already-packed package changes nothing, so it does not
// show up in a diff or touch mtimes for a build cache.
func TestPackingIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	files, _, err := Build(plugin(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	first, err := Plan(dir, files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, c := range first {
		if c.State != Created {
			t.Errorf("%s: first run state = %s, want created", c.Path, c.State)
		}
	}
	if err := Apply(dir, first); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	second, err := Plan(dir, files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, c := range second {
		if c.State != Unchanged {
			t.Errorf("%s: second run state = %s, want unchanged", c.Path, c.State)
		}
	}
	if (Result{Changes: second}).Written() {
		t.Error("a second pack reports work to do")
	}
}

// TestStaleManifestIsDetected is what --check exists for: somebody edits a
// generated file by hand, or changes plugin.json without re-packing, and CI
// says so instead of the package quietly disagreeing with itself.
func TestStaleManifestIsDetected(t *testing.T) {
	dir := t.TempDir()
	files, _, err := Build(plugin(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	changes, _ := Plan(dir, files)
	if err := Apply(dir, changes); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	target := filepath.Join(dir, ".codex-plugin", "plugin.json")
	if err := os.WriteFile(target, []byte(`{"name":"edited-by-hand"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	after, err := Plan(dir, files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !(Result{Changes: after}).Written() {
		t.Fatal("a hand-edited manifest was not detected as stale")
	}
	for _, c := range after {
		if c.Path == ".codex-plugin/plugin.json" && c.State != Updated {
			t.Errorf("state = %s, want updated", c.State)
		}
	}
}

// TestPlanNeverWrites keeps --dry-run and --check honest. Planning is what
// both of them run, so if planning could write, neither flag would mean
// anything.
func TestPlanNeverWrites(t *testing.T) {
	dir := t.TempDir()
	files, _, err := Build(plugin(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := Plan(dir, files); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Plan created %d entries in a directory it was only asked to inspect", len(entries))
	}
}

// TestUnknownClientIsRefused stops a typo in --client from looking like a
// client that needed nothing, which would be reported as success.
func TestUnknownClientIsRefused(t *testing.T) {
	if _, _, err := Build(plugin(), []string{"claude-code", "clyde"}); err == nil {
		t.Fatal("an unknown client was accepted")
	}
}

// TestClientSelectionIsOrderIndependent means two authors who write the same
// set of clients in a different order commit the same bytes.
func TestClientSelectionIsOrderIndependent(t *testing.T) {
	a := build(t, plugin(), "cursor", "codex")
	b := build(t, plugin(), "codex", "cursor")
	if len(a) != len(b) {
		t.Fatalf("%d files vs %d", len(a), len(b))
	}
	for path, content := range a {
		if string(b[path]) != string(content) {
			t.Errorf("%s differs by the order clients were named", path)
		}
	}
}

// TestNameIsRequired refuses to write a manifest naming nothing. Every one of
// these formats keys on the name, so an empty one produces three files that
// look installable and are not.
func TestNameIsRequired(t *testing.T) {
	p := plugin()
	p.Name = ""
	if _, _, err := Build(p, nil); err == nil {
		t.Fatal("packed a plugin with no name")
	}
}

// TestEveryWrittenPathStaysInsideThePackage. The paths are constants today, so
// this is a guard on future edits rather than on present input: pack writes
// into a directory the user named, and nothing it writes may resolve outside
// it.
func TestEveryWrittenPathStaysInsideThePackage(t *testing.T) {
	files, _, err := Build(plugin(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "/") || strings.Contains(f.Path, "..") {
			t.Errorf("path escapes the package: %q", f.Path)
		}
		if strings.Contains(f.Path, `\`) {
			t.Errorf("path is not slash-separated: %q", f.Path)
		}
	}
}

// TestCursorManifestNamesTheSpecFile. Cursor is pointed at the package's own
// mcp.json rather than given a translated copy, which is only correct while
// nothing needs rewriting for it. If that ever changes, this test is the place
// it should fail.
func TestCursorManifestNamesTheSpecFile(t *testing.T) {
	raw := build(t, plugin(), "cursor")[".cursor-plugin/plugin.json"]
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["mcpServers"] != "./mcp.json" {
		t.Errorf("mcpServers = %v, want ./mcp.json", m["mcpServers"])
	}
	if m["skills"] != "./skills/" {
		t.Errorf("skills = %v, want ./skills/", m["skills"])
	}
}

// TestUnmeasuredBehaviourIsReportedNotGuessed. Cursor's expansion of the
// specification's placeholders inside a package has not been measured, and the
// output says so. This is the project's standing rule in test form: silence
// must not read as confirmation.
func TestUnmeasuredBehaviourIsReportedNotGuessed(t *testing.T) {
	_, gaps, err := Build(plugin(), []string{"cursor"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(gaps) == 0 {
		t.Fatal("packing for Cursor reported no gap despite unmeasured placeholder expansion")
	}

	// A plugin whose servers need no expansion has nothing to be uncertain
	// about, and should not be warned at.
	p := plugin()
	p.MCPServers = []ir.MCPServer{{Name: "remote", Transport: ir.TransportStreamableHTTP, URL: "https://example.com/mcp"}}
	_, gaps, err = Build(p, []string{"cursor"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("warned about placeholder expansion for a plugin that uses no placeholders: %v", gaps)
	}
}

// TestClaudeCodeOutputRoundTripsBackToThePortableForm is the strongest check
// available that pack's translation is correct, and it does not depend on
// having Claude Code installed.
//
// pack writes the package in Claude Code's dialect; the Claude Code importer
// exists to read that dialect back into the IR. If the two agree, then every
// rewrite pack performed — the relative command into ${CLAUDE_PLUGIN_ROOT}, the
// placeholder spellings, the §9.1 env names, the §7.2.1 working directory — is
// reversible, which is only true if it was right. A silent mistranslation
// would show up here as a server that comes back different from the one that
// went in.
func TestClaudeCodeOutputRoundTripsBackToThePortableForm(t *testing.T) {
	dir := t.TempDir()

	// The portable package, as an author would write it.
	writeFile(t, dir, "plugin.json", `{
	  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
	  "name": "roundtrip",
	  "version": "1.0.0"
	}`)
	writeFile(t, dir, "mcp.json", `{
	  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
	  "mcpServers": {}
	}`)

	original := plugin()
	original.Name = "roundtrip"
	files, _, err := Build(original, []string{ClaudeCode})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	changes, err := Plan(dir, files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := Apply(dir, changes); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	root, err := safepath.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := claudecodeimp.New().Import(root)
	if err != nil {
		t.Fatalf("reading pack's own output back as a Claude Code plugin: %v", err)
	}
	if len(result.Plugin.MCPServers) != 1 {
		t.Fatalf("recovered %d servers, want 1", len(result.Plugin.MCPServers))
	}

	got := result.Plugin.MCPServers[0]
	want := original.MCPServers[0]

	if got.Command != want.Command {
		t.Errorf("command did not survive the round trip: %q -> %q", want.Command, got.Command)
	}
	if strings.Join(got.Args, " ") != strings.Join(want.Args, " ") {
		t.Errorf("args did not survive: %v -> %v", want.Args, got.Args)
	}
	for k, v := range want.Env {
		if got.Env[k] != v {
			t.Errorf("env %s did not survive: %q -> %q", k, v, got.Env[k])
		}
	}
	// §7.2.1's default is added by packing and must come back as the portable
	// placeholder, not as Claude Code's spelling.
	if got.Cwd != ir.PlaceholderPluginRoot {
		t.Errorf("cwd = %q, want %q", got.Cwd, ir.PlaceholderPluginRoot)
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
