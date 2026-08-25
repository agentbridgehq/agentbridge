package claudecode_test

import (
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/diag"
	"github.com/agentbridgehq/agentbridge/internal/importer"
	"github.com/agentbridgehq/agentbridge/internal/importer/claudecode"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

func mustLoad(t *testing.T, dir string) *importer.Result {
	t.Helper()
	root, err := safepath.NewRoot(dir)
	if err != nil {
		t.Fatalf("NewRoot(%s): %v", dir, err)
	}
	res, err := claudecode.New().Import(root)
	if err != nil {
		t.Fatalf("Import(%s): %v", dir, err)
	}
	return res
}

func TestImportManifest(t *testing.T) {
	p := mustLoad(t, "testdata/full").Plugin

	if p.Name != "deploy-tools" || p.Version != "2.1.0" {
		t.Errorf("identity = %q@%q", p.Name, p.Version)
	}
	if p.Origin.Dialect != ir.DialectClaudeCode {
		t.Errorf("dialect = %q", p.Origin.Dialect)
	}
	if p.Origin.ManifestPath != claudecode.ManifestPath {
		t.Errorf("manifest path = %q", p.Origin.ManifestPath)
	}
}

// The directory skill layout is the one thing both formats spell identically.
// If this ever needs a translation step, the premise of the bridge is weaker
// than assumed, so it is worth asserting directly.
func TestDirectorySkillsCrossUnchanged(t *testing.T) {
	res := mustLoad(t, "testdata/full")

	s, ok := res.Plugin.Skill("deploy-check")
	if !ok {
		t.Fatalf("deploy-check missing; got %+v", res.Plugin.Skills)
	}
	if s.Kind != ir.SkillDirectory {
		t.Errorf("kind = %q, want %q", s.Kind, ir.SkillDirectory)
	}
	if s.Entrypoint != "skills/deploy-check/SKILL.md" {
		t.Errorf("entrypoint = %q", s.Entrypoint)
	}
}

// Flat command files have no portable equivalent. They must load, and they
// must be flagged, because exporting one without restructuring silently drops
// a skill.
func TestFlatCommandsAreLoadedAndFlagged(t *testing.T) {
	res := mustLoad(t, "testdata/full")

	s, ok := res.Plugin.Skill("status")
	if !ok {
		t.Fatalf("flat command not loaded; got %+v", res.Plugin.Skills)
	}
	if s.Kind != ir.SkillFlatFile {
		t.Errorf("kind = %q, want %q", s.Kind, ir.SkillFlatFile)
	}
	if !hasCode(res.Diagnostics, diag.CodeSkillFlatCommand) {
		t.Errorf("expected %s, got %v", diag.CodeSkillFlatCommand, res.Diagnostics.Codes())
	}
}

// The M1-4 finding. Claude Code expands ${CLAUDE_PLUGIN_ROOT} inside
// "command"; Agent Plugins expands placeholders only in args, env values and
// cwd. The value therefore cannot be carried literally and must become a
// plugin-relative command. A converter that only renamed the placeholder would
// produce a manifest that passes schema validation and fails at launch.
func TestPluginRootInCommandIsRewritten(t *testing.T) {
	res := mustLoad(t, "testdata/full")

	s, ok := res.Plugin.MCPServer("bundled")
	if !ok {
		t.Fatal("bundled server missing")
	}
	if s.Command != "./bin/deploy-server" {
		t.Errorf("command = %q, want ./bin/deploy-server", s.Command)
	}
	if strings.Contains(s.Command, "CLAUDE_PLUGIN_ROOT") {
		t.Errorf("command still carries a Claude Code placeholder: %q", s.Command)
	}
	if !hasCode(res.Diagnostics, diag.CodeMCPCommandRewritten) {
		t.Errorf("rewrite must be reported; got %v", res.Diagnostics.Codes())
	}
}

func TestPlaceholdersRewrittenInArgsAndEnv(t *testing.T) {
	res := mustLoad(t, "testdata/full")

	s, _ := res.Plugin.MCPServer("bundled")
	joined := strings.Join(s.Args, " ")
	if !strings.Contains(joined, ir.PlaceholderPluginRoot) {
		t.Errorf("args not rewritten to the portable spelling: %v", s.Args)
	}
	if strings.Contains(joined, "CLAUDE_PLUGIN_ROOT") {
		t.Errorf("args still carry the Claude Code spelling: %v", s.Args)
	}
	if got := s.Env["CACHE_DIR"]; !strings.Contains(got, ir.PlaceholderPluginData) {
		t.Errorf("env not rewritten: %q", got)
	}
	if !hasCode(res.Diagnostics, diag.CodeMCPPlaceholderRewrit) {
		t.Errorf("expected %s, got %v", diag.CodeMCPPlaceholderRewrit, res.Diagnostics.Codes())
	}
}

// Claude Code entries need not declare a transport. Inferring it is fine;
// inferring it silently is not, because getting it wrong changes how the
// server is connected.
func TestTransportInferenceIsReported(t *testing.T) {
	res := mustLoad(t, "testdata/full")

	s, ok := res.Plugin.MCPServer("bundled")
	if !ok {
		t.Fatal("bundled server missing")
	}
	if s.Transport != ir.TransportStdio {
		t.Errorf("transport = %q, want stdio", s.Transport)
	}
	if !hasCode(res.Diagnostics, diag.CodeMCPTransportInferred) {
		t.Errorf("expected %s, got %v", diag.CodeMCPTransportInferred, res.Diagnostics.Codes())
	}

	// Claude Code's "http" maps onto the spec's "streamable-http".
	r, ok := res.Plugin.MCPServer("registry")
	if !ok {
		t.Fatal("registry server missing")
	}
	if r.Transport != ir.TransportStreamableHTTP {
		t.Errorf("transport = %q, want streamable-http", r.Transport)
	}
}

// Agent Plugins defines no WebSocket transport, so there is nothing to
// translate a ws server into. Skipping it is correct; skipping it quietly
// would not be.
func TestWebSocketServerSkippedWithReason(t *testing.T) {
	res := mustLoad(t, "testdata/full")

	if _, ok := res.Plugin.MCPServer("legacy-ws"); ok {
		t.Error("ws server should not have been imported")
	}
	if !hasCode(res.Diagnostics, diag.CodeComponentUnsupport) {
		t.Errorf("expected %s, got %v", diag.CodeComponentUnsupport, res.Diagnostics.Codes())
	}
}

// Components with no portable equivalent must be preserved and reported. This
// is the data a fidelity report is built from in M7.
func TestUnsupportedComponentsPreservedAndReported(t *testing.T) {
	res := mustLoad(t, "testdata/full")

	if res.Plugin.Native["claude-code"] == nil {
		t.Fatal("unsupported components were not preserved")
	}
	native := string(res.Plugin.Native["claude-code"])
	for _, want := range []string{"agents", "hooks", "bin"} {
		if !strings.Contains(native, want) {
			t.Errorf("%q missing from preserved components: %s", want, native)
		}
	}

	var reported []string
	for _, d := range res.Diagnostics {
		if d.Code == diag.CodeComponentUnsupport {
			reported = append(reported, d.Path)
		}
	}
	if len(reported) < 3 {
		t.Errorf("expected several unsupported-component reports, got %v", reported)
	}
}

// The manifest goes into a reverse-domain extension namespace, which the
// specification requires other clients to ignore without inspecting. That is
// what lets a Claude Code plugin survive a trip through the portable format.
func TestManifestPreservedInExtensionNamespace(t *testing.T) {
	res := mustLoad(t, "testdata/full")

	raw, ok := res.Plugin.Extensions[ir.ExtensionNamespaceClaudeCode]
	if !ok {
		t.Fatalf("manifest not preserved; namespaces: %v", res.Plugin.Extensions)
	}
	if !strings.Contains(string(raw), "displayName") {
		t.Errorf("manifest fields lost: %s", raw)
	}
}

// A plugin with no manifest is valid in this dialect: name from the directory,
// single skill from a root SKILL.md.
func TestManifestlessRootSkillPlugin(t *testing.T) {
	res := mustLoad(t, "testdata/rootskill")

	if res.Plugin.Name != "rootskill" {
		t.Errorf("name = %q, want rootskill", res.Plugin.Name)
	}
	if _, ok := res.Plugin.Skill("single"); !ok {
		t.Errorf("root SKILL.md not loaded; got %+v", res.Plugin.Skills)
	}
	if _, ok := res.Plugin.MCPServer("solo"); !ok {
		t.Errorf(".mcp.json not loaded")
	}
	if !hasCode(res.Diagnostics, diag.CodeManifestMissing) {
		t.Errorf("a directory-derived name should be reported; got %v", res.Diagnostics.Codes())
	}
}

func TestCapabilitiesInferred(t *testing.T) {
	res := mustLoad(t, "testdata/full")
	c := res.Plugin.Capabilities

	if !c.Exec || !c.Network || !c.Secrets {
		t.Errorf("capabilities = %+v; want exec, network and secrets", c.List())
	}
	// The skill body mentions ~/.aws/credentials. Whether that is benign is a
	// judgement for a human; surfacing it is not optional.
	if !hasCode(res.Diagnostics, diag.CodeSkillReadsCredsHint) {
		t.Errorf("credential reference in skill text not surfaced; got %v", res.Diagnostics.Codes())
	}
}

func TestDetect(t *testing.T) {
	for _, dir := range []string{"testdata/full", "testdata/rootskill"} {
		root, err := safepath.NewRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !claudecode.New().Detect(root) {
			t.Errorf("Detect(%s) = false", dir)
		}
	}
}

func hasCode(ds diag.Diagnostics, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}
