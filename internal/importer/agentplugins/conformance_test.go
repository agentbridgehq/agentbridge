package agentplugins_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/diag"
	"github.com/agentbridgehq/agentbridge/internal/importer"
	"github.com/agentbridgehq/agentbridge/internal/importer/agentplugins"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// Tests for requirements taken directly from Agent Plugins Specification
// v1.0.0. Each names the section it enforces, so a spec revision can be traced
// to the tests it invalidates.
//
// The recurring theme is that most of these cannot be expressed in JSON Schema,
// which is why the specification says its text governs where the two disagree
// (§7.2.1). A client that validates only against the published schema will load
// several plugins the spec forbids.

// §5.2 and §8.1: a non-object `extensions` is one of exactly two non-fatal
// schema violations. The client must report it, ignore the field, and carry on.
// The canonical schema would make it fatal, so this is a case where following
// the schema literally makes a client non-conformant.
func TestNonObjectExtensionsIsNonFatal(t *testing.T) {
	res, err := load(t, "testdata/nonobject-extensions")
	if err != nil {
		t.Fatalf("a non-object extensions field must not reject the plugin: %v", err)
	}
	if res.Plugin.Name != "ext-not-object" {
		t.Errorf("name = %q", res.Plugin.Name)
	}
	if len(res.Plugin.Extensions) != 0 {
		t.Errorf("the invalid field should have been ignored, got %v", res.Plugin.Extensions)
	}
	if !hasCode(res.Diagnostics, diag.CodeManifestBadExtensions) {
		t.Errorf("the field must be reported; got %v", res.Diagnostics.Codes())
	}
}

// §5.2: a client that does not support the declared version "MUST reject the
// plugin and SHOULD report the unsupported version".
func TestUnsupportedSpecVersionRejected(t *testing.T) {
	_, err := load(t, "testdata/unsupported-version")
	if err == nil {
		t.Fatal("a plugin targeting an unsupported version must be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported Agent Plugins version") {
		t.Errorf("error should name the problem: %v", err)
	}
	if !strings.Contains(err.Error(), "1.0.0") {
		t.Errorf("error should report the version we do support: %v", err)
	}
}

// §7.2.1: command "MUST be either a bare executable name or a plugin-relative
// path beginning with ./". The spec's own example calls ../bin/server invalid.
// §9.2 additionally forbids placeholder expansion in command, so a placeholder
// there would be launched literally.
func TestCommandMustBeBareOrPluginRelative(t *testing.T) {
	res := mustLoad(t, "testdata/badcommand")

	for _, name := range []string{"abs", "escape", "barepath", "placeholder"} {
		if _, ok := res.Plugin.MCPServer(name); ok {
			t.Errorf("server %q has an invalid command and should have been skipped", name)
		}
	}
	if _, ok := res.Plugin.MCPServer("ok"); !ok {
		t.Error("a bare command must still be accepted")
	}
	if !hasCode(res.Diagnostics, diag.CodeMCPInvalidCommand) {
		t.Errorf("expected %s; got %v", diag.CodeMCPInvalidCommand, res.Diagnostics.Codes())
	}
}

// §7.2.1: the url "MUST NOT contain user information or a fragment", and
// non-loopback endpoints must use HTTPS. None of this is in the schema.
func TestURLFormRules(t *testing.T) {
	res := mustLoad(t, "testdata/badurl")

	for _, name := range []string{"userinfo", "fragment"} {
		if _, ok := res.Plugin.MCPServer(name); ok {
			t.Errorf("server %q has an invalid url form and should have been skipped", name)
		}
	}
	if _, ok := res.Plugin.MCPServer("ok"); !ok {
		t.Error("a valid https url was rejected")
	}
	// The spec explicitly permits plain HTTP to a loopback host.
	if _, ok := res.Plugin.MCPServer("loopback"); !ok {
		t.Error("http to localhost is permitted by §7.2.1 and must not be rejected")
	}
	if !hasCode(res.Diagnostics, diag.CodeMCPInvalidURLForm) {
		t.Errorf("expected %s; got %v", diag.CodeMCPInvalidURLForm, res.Diagnostics.Codes())
	}
}

// §7.2.1: "Header names are case-insensitive; an entry containing the same
// header name more than once under different casing is invalid."
func TestDuplicateHeaderCasingIsInvalid(t *testing.T) {
	res := mustLoad(t, "testdata/badheaders")

	if _, ok := res.Plugin.MCPServer("dupcase"); ok {
		t.Error("headers differing only in case make the entry invalid")
	}
	if _, ok := res.Plugin.MCPServer("ok"); !ok {
		t.Error("a valid header set was rejected")
	}
	if !hasCode(res.Diagnostics, diag.CodeMCPDuplicateHeader) {
		t.Errorf("expected %s; got %v", diag.CodeMCPDuplicateHeader, res.Diagnostics.Codes())
	}
}

// §9.2: an env entry named PLUGIN_ROOT or PLUGIN_DATA "makes that server
// configuration invalid". The client supplies both itself.
func TestReservedEnvNamesInvalidateServer(t *testing.T) {
	res := mustLoad(t, "testdata/reservedenv")

	if _, ok := res.Plugin.MCPServer("reserved"); ok {
		t.Error("a reserved env name must invalidate the server entry")
	}
	if _, ok := res.Plugin.MCPServer("ok"); !ok {
		t.Error("the sibling server must still load — failures are isolated per entry")
	}
}

// §6.2: a fixed component location that exists but is the wrong filesystem kind
// makes that component type invalid, while other component types keep loading.
func TestWrongFilesystemKindAtFixedLocation(t *testing.T) {
	t.Run("skills is not a directory", func(t *testing.T) {
		res := mustLoad(t, "testdata/skills-not-dir")
		if len(res.Plugin.Skills) != 0 {
			t.Errorf("skills = %v, want none", res.Plugin.Skills)
		}
		if !hasCode(res.Diagnostics, diag.CodeSkillsNotDirectory) {
			t.Errorf("expected %s; got %v", diag.CodeSkillsNotDirectory, res.Diagnostics.Codes())
		}
	})

	// Built at runtime rather than checked in, because the fixture this needs
	// is a *directory* named mcp.json with nothing in it — and git cannot store
	// an empty directory. As a testdata fixture it survived locally and
	// vanished in every clone, so the test passed here and failed for everyone
	// else. The neighbouring TestSkillMDMustBeRegularFile already builds its
	// fixture this way for the same reason.
	t.Run("mcp.json is not a regular file", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "plugin.json",
			`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"mcp-not-file"}`)
		write(t, dir, "skills/s/SKILL.md", "---\nname: s\n---\nbody\n")
		if err := os.MkdirAll(filepath.Join(dir, "mcp.json"), 0o755); err != nil {
			t.Fatal(err)
		}

		res := mustLoad(t, dir)
		if len(res.Plugin.MCPServers) != 0 {
			t.Errorf("servers = %v, want none", res.Plugin.MCPServers)
		}
		if !hasCode(res.Diagnostics, diag.CodeMCPNotRegularFile) {
			t.Errorf("expected %s; got %v", diag.CodeMCPNotRegularFile, res.Diagnostics.Codes())
		}
		// The other component type must be unaffected.
		if len(res.Plugin.Skills) != 1 {
			t.Errorf("skills should still load; got %v", res.Plugin.Skills)
		}
	})
}

// §7.1: a skill is a directory containing "a path named exactly SKILL.md that
// resolves to a regular file".
func TestSkillMDMustBeRegularFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "plugin.json",
		`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kinds"}`)
	if err := os.MkdirAll(filepath.Join(dir, "skills", "bogus", "SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := safepath.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := agentplugins.New().Import(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Plugin.Skills) != 0 {
		t.Errorf("a directory named SKILL.md is not a skill; got %v", res.Plugin.Skills)
	}
	if !hasCode(res.Diagnostics, diag.CodeSkillNotRegularFile) {
		t.Errorf("expected %s; got %v", diag.CodeSkillNotRegularFile, res.Diagnostics.Codes())
	}
}

// §7.2.1: cwd must be exactly ${PLUGIN_DATA}, or begin ${PLUGIN_DATA}/ — a
// value like "${PLUGIN_DATA}x" is not one of the three permitted forms — and a
// ${PLUGIN_DATA}-rooted value must stay inside the data directory.
func TestCwdFormsAndContainment(t *testing.T) {
	cases := map[string]bool{
		"./work":                true,
		"${PLUGIN_ROOT}":        true,
		"${PLUGIN_ROOT}/work":   true,
		"${PLUGIN_DATA}":        true,
		"${PLUGIN_DATA}/cache":  true,
		"${PLUGIN_DATA}/../out": false,
		"${PLUGIN_ROOT}/../out": false,
		"work":                  false,
		"/tmp":                  false,
		"../out":                false,
	}

	root, err := safepath.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for cwd, want := range cases {
		var ds diag.Diagnostics
		if got := importer.CheckCwd(root, "srv", cwd, &ds); got != want {
			t.Errorf("CheckCwd(%q) = %v, want %v", cwd, got, want)
		}
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
