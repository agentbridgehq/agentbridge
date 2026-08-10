// Package vscode installs plugins into VS Code and GitHub Copilot.
//
// VS Code's MCP configuration differs from every other client's in two ways
// that matter: the container key is "servers", not "mcpServers", and the
// transport for a remote server is spelled "http" rather than the
// specification's "streamable-http". Both are easy to get wrong and both fail
// silently — the file parses, the server never appears.
//
// The user configuration file is JSONC: VS Code ships it with comments, and
// users add their own. That is the reason internal/configedit exists.
//
// Paths: https://code.visualstudio.com/docs/agents/reference/mcp-configuration
//   - workspace: <root>/.vscode/mcp.json
//   - user:      macOS   ~/Library/Application Support/Code/User/mcp.json
//     Linux   ~/.config/Code/User/mcp.json
//     Windows %APPDATA%\Code\User\mcp.json
package vscode

import (
	"os"
	"path/filepath"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
)

// ID is the client identifier.
const ID = "vscode"

// Adapter writes VS Code's MCP configuration.
type Adapter struct{ dataDir func(string) string }

// New returns a VS Code adapter.
func New(pluginDataDir func(pluginName string) string) *Adapter {
	return &Adapter{dataDir: pluginDataDir}
}

// Client implements adapter.Adapter.
func (*Adapter) Client() adapter.Client {
	return adapter.Client{
		ID:         ID,
		Name:       "VS Code / Copilot",
		Conformant: true,
		Skills:     adapter.SupportUndocumented,
		MCP:        adapter.SupportNative,
		ConfigDoc:  "https://code.visualstudio.com/docs/agents/reference/mcp-configuration",
	}
}

// UserConfigPath returns the per-OS user configuration path.
func UserConfigPath(env adapter.Env) string {
	switch env.GOOS {
	case "darwin":
		return filepath.Join(env.HomeDir, "Library", "Application Support", "Code", "User", "mcp.json")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(env.HomeDir, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Code", "User", "mcp.json")
	default:
		configDir := env.ConfigDir
		if configDir == "" {
			configDir = filepath.Join(env.HomeDir, ".config")
		}
		return filepath.Join(configDir, "Code", "User", "mcp.json")
	}
}

// Detect implements adapter.Adapter.
func (a *Adapter) Detect(env adapter.Env) []adapter.Installation {
	var out []adapter.Installation

	userConfig := UserConfigPath(env)
	// The User directory is the reliable signal. mcp.json itself only appears
	// once someone has configured a server, so requiring it would miss every
	// fresh installation.
	if isDir(filepath.Dir(userConfig)) {
		out = append(out, adapter.Installation{
			Client:     a.Client(),
			Scope:      adapter.ScopeUser,
			ConfigPath: userConfig,
			Evidence:   filepath.Dir(userConfig) + " exists",
		})
	}

	if env.ProjectDir != "" {
		projectDir := filepath.Join(env.ProjectDir, ".vscode")
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
		Client: a.Client(),
		// Not "mcpServers". VS Code is the odd one out.
		ServersKey:    []string{"servers"},
		Encode:        encode,
		PluginDataDir: a.dataDir,
	}
}

func encode(s ir.MCPServer) (map[string]any, string, bool) {
	switch s.Transport {
	case ir.TransportStdio:
		v := map[string]any{"type": "stdio", "command": s.Command}
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
		// VS Code spells this "http".
		v := map[string]any{"type": "http", "url": s.URL}
		if len(s.Headers) > 0 {
			v["headers"] = s.Headers
		}
		return v, "", true

	case ir.TransportSSE:
		v := map[string]any{"type": "sse", "url": s.URL}
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
