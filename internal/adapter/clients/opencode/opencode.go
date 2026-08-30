// Package opencode installs plugins into opencode.
//
// opencode is the second client after Claude Code that can take skills, and
// unlike every other target here that is not a guess: its skill loader scans
// configured skill directories recursively for **/SKILL.md, so a whole plugin
// package can be dropped in one directory and every skill inside it is found.
// That was verified against the binary rather than inferred from prose — see
// the note on `environment` below for why reading the documentation was not
// enough.
//
// MCP configuration is translated. opencode's dialect differs from the
// portable format in three ways that all have to be right at once:
//
//   - command and args are a single array: ["npx", "-y", "@scope/server"].
//   - the transport discriminator is "local" or "remote", not stdio/http.
//   - environment variables live under "environment".
//
// That last one is the reason this adapter exists in the shape it does.
// opencode's own bundled documentation gives the key as "env", and a config
// using "env" is accepted without complaint — the key is then silently
// dropped, so the server starts with none of its environment. Following the
// vendor's documentation would therefore lose PLUGIN_ROOT and PLUGIN_DATA,
// which §9.1 requires every plugin subprocess to receive, and the failure
// would surface much later as a plugin that merely does not work. The
// published schema is right and the prose is wrong; both were checked against
// `opencode debug config`, which reports the config opencode actually resolved.
//
// Placeholders are materialized to absolute paths before they are written,
// because opencode interpolates {env:VAR} and {file:path} but explicitly does
// not substitute shell-style ${VAR}.
//
// Paths: https://opencode.ai/docs/config/ — global ~/.config/opencode/, project
// <root>/opencode.json, honouring XDG_CONFIG_HOME.
package opencode

import (
	"os"
	"path/filepath"

	"github.com/agentbridgehq/agentbridge/internal/adapter"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// ID is the client identifier.
const ID = "opencode"

// Adapter installs a plugin package and MCP configuration into opencode.
type Adapter struct{ dataDir func(string) string }

// New returns an opencode adapter. pluginDataDir resolves ${PLUGIN_DATA}.
func New(pluginDataDir func(pluginName string) string) *Adapter {
	return &Adapter{dataDir: pluginDataDir}
}

// Client implements adapter.Adapter.
func (*Adapter) Client() adapter.Client {
	return adapter.Client{
		ID:   ID,
		Name: "opencode",
		// Not an Agent Plugins launch client; we translate into its own
		// documented format.
		Conformant: false,
		Skills:     adapter.SupportTranslated,
		MCP:        adapter.SupportTranslated,
		ConfigDoc:  "https://opencode.ai/docs/config/",
		Losses: adapter.DeclaredLosses(
			adapter.LossNativeComponentDropped,
		),
	}
}

// Detect implements adapter.Adapter.
func (a *Adapter) Detect(env adapter.Env) []adapter.Installation {
	var out []adapter.Installation

	userDir := filepath.Join(configHome(env), "opencode")
	if isDir(userDir) {
		out = append(out, adapter.Installation{
			Client:     a.Client(),
			Scope:      adapter.ScopeUser,
			ConfigPath: existingConfig(userDir, "opencode.json", "opencode.jsonc"),
			PackageDir: filepath.Join(userDir, "skills"),
			Evidence:   userDir + " exists",
		})
	}

	if env.ProjectDir != "" {
		if path := projectConfig(env.ProjectDir); path != "" {
			out = append(out, adapter.Installation{
				Client:     a.Client(),
				Scope:      adapter.ScopeProject,
				ConfigPath: path,
				PackageDir: filepath.Join(env.ProjectDir, ".opencode", "skills"),
				Evidence:   filepath.Dir(path) + " has opencode configuration",
			})
		}
	}
	return out
}

// configHome returns the XDG configuration root. opencode is explicit that its
// global directory is ~/.config/opencode on every platform, including macOS —
// so env.ConfigDir, which is ~/Library/Application Support there, is the wrong
// answer and would silently miss a real installation.
func configHome(env adapter.Env) string {
	if env.ConfigHome != "" {
		return env.ConfigHome
	}
	return filepath.Join(env.HomeDir, ".config")
}

// existingConfig picks the config file already on disk, so an install adds to
// the user's opencode.jsonc rather than creating a second opencode.json beside
// it that would then be merged over. It falls back to the first name, which is
// the one to create when there is nothing yet.
func existingConfig(dir string, names ...string) string {
	for _, name := range names {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join(dir, names[0])
}

// projectConfig finds a project-scoped opencode config, in the order opencode
// itself looks for one. It returns "" when the project does not use opencode,
// which is what keeps an install from creating configuration nobody asked for.
func projectConfig(root string) string {
	for _, rel := range []string{
		"opencode.json",
		"opencode.jsonc",
		filepath.Join(".opencode", "opencode.json"),
		filepath.Join(".opencode", "opencode.jsonc"),
	} {
		path := filepath.Join(root, rel)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	// A .opencode directory without a config file is still an opencode
	// project; it is where agents, commands and skills live.
	if isDir(filepath.Join(root, ".opencode")) {
		return filepath.Join(root, "opencode.json")
	}
	return ""
}

// Plan implements adapter.Adapter.
func (a *Adapter) Plan(inst adapter.Installation, p *ir.Plugin, src *safepath.Root, opts adapter.PlanOptions) (*adapter.Plan, error) {
	plan, err := adapter.PlanJSONMCP(a.spec(), inst, p, src, opts)
	if err != nil {
		return nil, err
	}

	if len(p.Skills) == 0 {
		return plan, nil
	}

	// PlanJSONMCP assumes a JSON-configured client cannot take skills, which
	// is true of every other client with this config shape. opencode can, so
	// the package copy is layered on top.
	if src == nil {
		plan.Fidelity.AddLoss(adapter.LossNativeComponentDropped, "",
			"opencode takes skills as a directory, so installing them requires the plugin's source; none was supplied")
		return plan, nil
	}

	target := filepath.Join(inst.PackageDir, p.Name)
	plan.PackageDir = target
	// The copy goes first: it creates the directory the skills are read from,
	// and if it fails there is no config entry pointing at a tree that is not
	// there.
	plan.Ops = append([]adapter.Op{adapter.CopyTreeOp(target, src.Path(), "install plugin package")}, plan.Ops...)
	plan.Fidelity.Skills = adapter.Coverage{Carried: len(p.Skills), Total: len(p.Skills)}

	return plan, nil
}

// PlanRemove implements adapter.Adapter.
func (a *Adapter) PlanRemove(inst adapter.Installation, pluginName string) (*adapter.Plan, error) {
	return a.PlanRemoveKeys(inst, pluginName, nil, nil)
}

// PlanRemoveKeys removes exactly the keys a receipt recorded, and the package
// directory alongside them. Both have to happen here: removal dispatches on
// the recorded config keys, so a plugin that installed servers never reaches
// PlanRemove and its skills would otherwise be left behind.
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
		ServersKey:    []string{"mcp"},
		Encode:        encode,
		PluginDataDir: a.dataDir,
	}
}

// encode renders a server in opencode's shape.
func encode(s ir.MCPServer) (map[string]any, string, bool) {
	switch s.Transport {
	case ir.TransportStdio:
		// command carries the executable and its arguments in one array.
		command := append([]string{s.Command}, s.Args...)
		v := map[string]any{
			"type":    "local",
			"command": command,
			"enabled": true,
		}
		if len(s.Env) > 0 {
			// "environment", not "env": see the package comment.
			v["environment"] = s.Env
		}
		if s.Cwd != "" {
			v["cwd"] = s.Cwd
		}
		return v, "", true

	case ir.TransportStreamableHTTP, ir.TransportSSE:
		v := map[string]any{
			"type":    "remote",
			"url":     s.URL,
			"enabled": true,
		}
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
