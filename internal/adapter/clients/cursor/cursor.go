// Package cursor installs plugins into Cursor.
//
// Cursor takes both components, but only one of the two locations comes from
// its vendor. MCP configuration is documented: ~/.cursor/mcp.json. The plugin
// directory is not, and was established instead by reading a plugin Cursor had
// already installed on a real machine — ~/.cursor/plugins/, with local/ for
// local installs — and then confirmed by putting a package there and asking
// Cursor what it had loaded. It answered with the skill and the path it came
// from. Until that happened this adapter reported skills as not installed
// rather than write to a guessed path, which is the same standard applied to
// the clients still marked undocumented.
//
// A package is marked by .cursor-plugin/plugin.json, the direct analogue of
// Claude Code's .claude-plugin/plugin.json. That manifest names its own
// components rather than relying on convention: "skills": "./skills/", and
// "mcpServers": "./.mcp.json" if it carries any.
//
// This adapter deliberately declares only skills there. MCP servers keep going
// to ~/.cursor/mcp.json, which is documented, already verified, and where a
// user expects to find them — declaring them in both places would register
// every server twice.
//
// Paths: https://cursor.com/docs/mcp — global ~/.cursor/mcp.json, project
// <root>/.cursor/mcp.json, with the project file taking precedence.
package cursor

import (
	"encoding/json"
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
		Skills:     adapter.SupportTranslated,
		MCP:        adapter.SupportNative,
		ConfigDoc:  "https://cursor.com/docs/mcp",
		Losses:     adapter.DeclaredLosses(adapter.LossNativeComponentDropped),
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
			PackageDir: filepath.Join(userDir, "plugins", "local"),
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
	plan, err := adapter.PlanJSONMCP(a.spec(), inst, p, src, opts)
	if err != nil {
		return nil, err
	}
	if len(p.Skills) == 0 || inst.PackageDir == "" {
		return plan, nil
	}

	// PlanJSONMCP assumes a JSON-configured client cannot take skills, which
	// was true of Cursor until the plugin directory was found.
	if src == nil {
		plan.Fidelity.AddLoss(adapter.LossNativeComponentDropped, "",
			"Cursor takes skills as a plugin directory, so installing them requires the plugin's source; none was supplied")
		return plan, nil
	}

	target := filepath.Join(inst.PackageDir, p.Name)
	plan.PackageDir = target
	// The copy goes first so the manifest is never written into a directory
	// that does not exist yet.
	plan.Ops = append([]adapter.Op{adapter.CopyTreeOp(target, src.Path(), "install plugin package", ".cursor-plugin/plugin.json")}, plan.Ops...)

	manifest, err := buildManifest(p)
	if err != nil {
		return nil, err
	}
	plan.Ops = append(plan.Ops, adapter.Op{
		Kind: adapter.OpWriteFile,
		Path: filepath.Join(target, ".cursor-plugin", "plugin.json"),
		// Read from disk rather than left nil: an op with no Before can never
		// compare equal, which made every re-install report a change even when
		// the package copy above was identical.
		Before: adapter.ExistingFile(filepath.Join(target, ".cursor-plugin", "plugin.json")),
		After:  manifest,
		Note:   "write Cursor plugin manifest",
	})
	plan.Fidelity.Skills = adapter.Coverage{Carried: len(p.Skills), Total: len(p.Skills)}

	return plan, nil
}

// buildManifest writes .cursor-plugin/plugin.json.
//
// "skills" is declared explicitly rather than left to convention, which is what
// the plugins Cursor ships itself do. "mcpServers" is deliberately omitted:
// those go to ~/.cursor/mcp.json, and naming them here as well would register
// every server twice.
func buildManifest(p *ir.Plugin) ([]byte, error) {
	m := map[string]any{"name": p.Name, "skills": "./skills/"}
	setIfNotEmpty(m, "version", p.Version)
	setIfNotEmpty(m, "description", p.Description)
	setIfNotEmpty(m, "homepage", p.Homepage)
	setIfNotEmpty(m, "repository", p.Repository)
	if len(p.Keywords) > 0 {
		m["keywords"] = p.Keywords
	}
	if p.Author != nil && p.Author.Name != "" {
		m["author"] = map[string]any{"name": p.Author.Name}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func setIfNotEmpty(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// PlanRemove implements adapter.Adapter.
func (a *Adapter) PlanRemove(inst adapter.Installation, pluginName string) (*adapter.Plan, error) {
	return a.PlanRemoveKeys(inst, pluginName, nil, nil)
}

// PlanRemoveKeys removes exactly the keys a receipt recorded, and the plugin
// package alongside them. Both happen here because removal dispatches on the
// recorded config keys: a plugin that installed a server never reaches
// PlanRemove, and its skills would otherwise stay installed.
func (a *Adapter) PlanRemoveKeys(inst adapter.Installation, pluginName string, keys, created [][]string) (*adapter.Plan, error) {
	plan, err := adapter.PlanRemoveJSONMCP(a.spec(), inst, pluginName, keys, created)
	if err != nil {
		return nil, err
	}
	if inst.PackageDir == "" {
		return plan, nil
	}
	target := filepath.Join(inst.PackageDir, pluginName)
	plan.PackageDir = target
	plan.Ops = append(plan.Ops, adapter.Op{
		Kind:         adapter.OpRemoveTree,
		Path:         target,
		TargetExists: adapter.PathExists(target),
		Note:         "remove plugin package",
	})
	return plan, nil
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
