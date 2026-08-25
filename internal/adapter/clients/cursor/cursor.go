// Package cursor installs plugins into Cursor.
//
// Cursor is an Agent Plugins launch client, but the on-disk location it loads
// portable plugin packages from is not documented by its vendor. MCP
// configuration is documented, so that is what this adapter writes; skills are
// reported as not installed rather than written to a guessed path. Resolving
// that gap is a job for the conformance harness (M10-2), not for a hunch.
//
// Paths: https://cursor.com/docs/mcp — global ~/.cursor/mcp.json, project
// <root>/.cursor/mcp.json, with the project file taking precedence.
package cursor

import (
	"os"
	"path/filepath"

	"github.com/agentbridgehq/agentbridge/internal/adapter"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// ID is the client identifier.
const ID = "cursor"

// Adapter writes Cursor's MCP configuration.
type Adapter struct{ dataDir func(string) string }

// New returns a Cursor adapter. pluginDataDir resolves ${PLUGIN_DATA}.
func New(pluginDataDir func(pluginName string) string) *Adapter {
	return &Adapter{dataDir: pluginDataDir}
}

// Client implements adapter.Adapter.
func (*Adapter) Client() adapter.Client {
	return adapter.Client{
		ID:         ID,
		Name:       "Cursor",
		Conformant: true,
		// Cursor loads Agent Plugins, but where from is undocumented.
		Skills:    adapter.SupportUndocumented,
		MCP:       adapter.SupportNative,
		ConfigDoc: "https://cursor.com/docs/mcp",
		Losses:    adapter.DeclaredLosses(adapter.LossSkillsUndocumented),
	}
}

// Detect implements adapter.Adapter.
func (a *Adapter) Detect(env adapter.Env) []adapter.Installation {
	var out []adapter.Installation

	userDir := filepath.Join(env.HomeDir, ".cursor")
	if isDir(userDir) {
		out = append(out, adapter.Installation{
			Client:     a.Client(),
			Scope:      adapter.ScopeUser,
			ConfigPath: filepath.Join(userDir, "mcp.json"),
			Evidence:   userDir + " exists",
		})
	}

	if env.ProjectDir != "" {
		projectDir := filepath.Join(env.ProjectDir, ".cursor")
		if isDir(projectDir) {
			out = append(out, adapter.Installation{
				Client:     a.Client(),
				Scope:      adapter.ScopeProject,
				ConfigPath: filepath.Join(projectDir, "mcp.json"),
				Evidence:   projectDir + " exists",
			})
		}
	}
	return out
}

// Plan implements adapter.Adapter.
func (a *Adapter) Plan(inst adapter.Installation, p *ir.Plugin, src *safepath.Root, opts adapter.PlanOptions) (*adapter.Plan, error) {
	return adapter.PlanJSONMCP(a.spec(), inst, p, src, opts)
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

// encode renders a server in Cursor's shape: a command-based entry for stdio,
// a url-based one for remote transports.
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

	case ir.TransportStreamableHTTP, ir.TransportSSE:
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
