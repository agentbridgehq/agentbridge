// Package gemini installs plugins into Gemini CLI.
//
// Gemini CLI is not an Agent Plugins client at all, which is precisely why it
// is here: a bridge that only spans the clients already covered by the standard
// is not bridging anything. It reads MCP servers from a top-level "mcpServers"
// object in its settings file, and distinguishes the two HTTP transports by
// field name rather than by a type discriminator — "httpUrl" for streamable
// HTTP, "url" for SSE.
//
// It has no skills mechanism, so skills are reported as not installed. That is
// a hard "none", not an undocumented gap.
//
// Paths: https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md
// — user ~/.gemini/settings.json, project <root>/.gemini/settings.json.
package gemini

import (
	"os"
	"path/filepath"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
)

// ID is the client identifier.
const ID = "gemini-cli"

// Adapter writes Gemini CLI's settings file.
type Adapter struct{ dataDir func(string) string }

// New returns a Gemini CLI adapter.
func New(pluginDataDir func(pluginName string) string) *Adapter {
	return &Adapter{dataDir: pluginDataDir}
}

// Client implements adapter.Adapter.
func (*Adapter) Client() adapter.Client {
	return adapter.Client{
		ID:         ID,
		Name:       "Gemini CLI",
		Conformant: false,
		Skills:     adapter.SupportNone,
		MCP:        adapter.SupportTranslated,
		ConfigDoc:  "https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md",
	}
}

// Detect implements adapter.Adapter.
func (a *Adapter) Detect(env adapter.Env) []adapter.Installation {
	var out []adapter.Installation

	userDir := filepath.Join(env.HomeDir, ".gemini")
	if isDir(userDir) {
		out = append(out, adapter.Installation{
			Client:     a.Client(),
			Scope:      adapter.ScopeUser,
			ConfigPath: filepath.Join(userDir, "settings.json"),
			Evidence:   userDir + " exists",
		})
	}

	if env.ProjectDir != "" {
		projectDir := filepath.Join(env.ProjectDir, ".gemini")
		if isDir(projectDir) {
			out = append(out, adapter.Installation{
				Client:     a.Client(),
				Scope:      adapter.ScopeProject,
				ConfigPath: filepath.Join(projectDir, "settings.json"),
				Evidence:   projectDir + " exists",
			})
		}
	}
	return out
}

// Plan implements adapter.Adapter.
func (a *Adapter) Plan(inst adapter.Installation, p *ir.Plugin, src *safepath.Root) (*adapter.Plan, error) {
	return adapter.PlanJSONMCP(a.spec(), inst, p, src)
}

// PlanRemove implements adapter.Adapter.
func (a *Adapter) PlanRemove(inst adapter.Installation, pluginName string) (*adapter.Plan, error) {
	return adapter.PlanRemoveJSONMCP(a.spec(), inst, pluginName, nil)
}

// PlanRemoveKeys removes exactly the keys a receipt recorded.
func (a *Adapter) PlanRemoveKeys(inst adapter.Installation, pluginName string, keys [][]string) (*adapter.Plan, error) {
	return adapter.PlanRemoveJSONMCP(a.spec(), inst, pluginName, keys)
}

func (a *Adapter) spec() adapter.JSONMCPSpec {
	return adapter.JSONMCPSpec{
		Client:        a.Client(),
		ServersKey:    []string{"mcpServers"},
		Encode:        encode,
		PluginDataDir: a.dataDir,
	}
}

func encode(s ir.MCPServer) (map[string]any, string, bool) {
	switch s.Transport {
	case ir.TransportStdio:
		v := map[string]any{"command": s.Command}
		if len(s.Args) > 0 {
			v["args"] = s.Args
		}
		if len(s.Env) > 0 {
			v["env"] = s.Env
		}
		if s.Cwd != "" {
			v["cwd"] = s.Cwd
		}
		return v, "", true

	case ir.TransportStreamableHTTP:
		v := map[string]any{"httpUrl": s.URL}
		if len(s.Headers) > 0 {
			v["headers"] = s.Headers
		}
		return v, "", true

	case ir.TransportSSE:
		v := map[string]any{"url": s.URL}
		if len(s.Headers) > 0 {
			v["headers"] = s.Headers
		}
		return v, "", true

	default:
		return nil, "unknown transport " + string(s.Transport), false
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
