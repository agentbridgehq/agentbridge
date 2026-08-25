package registry_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/diag"
	"github.com/agentbridgehq/agentbridge/internal/importer"
	"github.com/agentbridgehq/agentbridge/internal/importer/registry"
	"github.com/agentbridgehq/agentbridge/internal/ir"
)

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

const agentPluginsManifest = `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "detect-me"
}`

// Detection is ordered most-specific first. The bare mcp.json dialect would
// otherwise claim any directory containing an MCP config, including every
// plugin in both richer formats.
func TestDetectionOrder(t *testing.T) {
	t.Run("agent-plugins wins over a bare mcp.json", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "plugin.json", agentPluginsManifest)
		write(t, dir, "mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{}}`)

		res, err := registry.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if res.Plugin.Origin.Dialect != ir.DialectAgentPlugins {
			t.Errorf("dialect = %q, want %q", res.Plugin.Origin.Dialect, ir.DialectAgentPlugins)
		}
	})

	t.Run("claude-code wins over a bare .mcp.json", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, ".claude-plugin/plugin.json", `{"name":"cc-plugin"}`)
		write(t, dir, ".mcp.json", `{"mcpServers":{"x":{"command":"node"}}}`)

		res, err := registry.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if res.Plugin.Origin.Dialect != ir.DialectClaudeCode {
			t.Errorf("dialect = %q, want %q", res.Plugin.Origin.Dialect, ir.DialectClaudeCode)
		}
	})

	t.Run("a bare fragment falls through to mcp.json", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "mcp.json", `{"mcpServers":{"db":{"command":"npx","args":["@acme/db"]}}}`)

		res, err := registry.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if res.Plugin.Origin.Dialect != ir.DialectMCPJSON {
			t.Errorf("dialect = %q, want %q", res.Plugin.Origin.Dialect, ir.DialectMCPJSON)
		}
		if _, ok := res.Plugin.MCPServer("db"); !ok {
			t.Error("server not imported")
		}
		// The synthesized plugin is not a real one, and saying so is more
		// useful than pretending otherwise.
		if !hasCode(res.Diagnostics, diag.CodeComponentPreserved) {
			t.Errorf("synthesis not reported: %v", res.Diagnostics.Codes())
		}
	})
}

// A Claude Code plugin whose only marker is a skills/ directory is
// indistinguishable from an Agent Plugins one, because that layout is
// identical in both. Claiming it would be a guess, so the directory is
// reported as unrecognized instead.
func TestUnrecognizedDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "skills/a/SKILL.md", "---\nname: a\n---\nbody")

	_, err := registry.Open(dir)
	if !errors.Is(err, importer.ErrNotRecognized) {
		t.Errorf("Open = %v, want ErrNotRecognized", err)
	}
}

func TestOpenAs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "plugin.json", agentPluginsManifest)

	// Forcing a dialect must bypass detection entirely.
	if _, err := registry.OpenAs(dir, ir.DialectMCPJSON); err == nil {
		t.Error("forcing an inapplicable dialect should fail rather than fall back")
	}
	if _, err := registry.OpenAs(dir, "no-such-dialect"); err == nil {
		t.Error("an unknown dialect should be rejected")
	}
	if _, err := registry.OpenAs(dir, ir.DialectAgentPlugins); err != nil {
		t.Errorf("OpenAs = %v", err)
	}
}

func TestOpenRejectsMissingDirectory(t *testing.T) {
	if _, err := registry.Open(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a missing directory")
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
