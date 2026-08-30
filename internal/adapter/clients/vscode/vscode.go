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
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/adapter"
	"github.com/agentbridgehq/agentbridge/internal/configedit"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// ID is the client identifier.
const ID = "vscode"

// Adapter writes VS Code's MCP configuration and installs skills.
type Adapter struct {
	dataDir func(string) string
	// home is kept because the skills location must be expressed relative to
	// it, and Plan does not receive the Env that Detect did.
	home string
}

// New returns a VS Code adapter.
func New(pluginDataDir func(pluginName string) string, home string) *Adapter {
	return &Adapter{dataDir: pluginDataDir, home: home}
}

// Client implements adapter.Adapter.
func (*Adapter) Client() adapter.Client {
	return adapter.Client{
		ID:         ID,
		Name:       "VS Code / Copilot",
		Conformant: true,
		Skills:     adapter.SupportTranslated,
		MCP:        adapter.SupportNative,
		ConfigDoc:  "https://code.visualstudio.com/docs/agents/reference/mcp-configuration",
		Losses:     adapter.DeclaredLosses(adapter.LossNativeComponentDropped),
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

// SkillsLocationsKey is the setting VS Code reads extra skill roots from. Its
// default value is the built-in table of locations, so adding to it is the
// documented way to extend that list rather than a way around it.
var SkillsLocationsKey = []string{"chat.agentSkillsLocations"}

// homeRelative renders an absolute path as the "~/"-prefixed form the setting
// requires.
//
// VS Code validates the keys of chat.agentSkillsLocations against a pattern
// that rejects absolute paths outright — `(?!/)` and a Windows-drive guard —
// while explicitly permitting a leading "~/". A key it rejects is dropped, and
// the skills simply never appear, so this is not a stylistic preference.
// Backslashes and glob characters are refused by the same pattern, which is why
// the result is always slash-separated.
func homeRelative(home, path string) (string, bool) {
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if strings.ContainsAny(rel, `\*?[]{}`) {
		return "", false
	}
	return "~/" + rel, true
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
			Client: a.Client(),
			Scope:  adapter.ScopeUser,
			// Skills are installed under the User directory rather than into
			// our own state, so uninstalling VS Code takes them with it, and
			// so the registered path stays under the home directory the
			// setting's pattern requires.
			ConfigPath: userConfig,
			PackageDir: filepath.Join(filepath.Dir(userConfig), "agentbridge"),
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
func (a *Adapter) Plan(inst adapter.Installation, p *ir.Plugin, src *safepath.Root, opts adapter.PlanOptions) (*adapter.Plan, error) {
	plan, err := adapter.PlanJSONMCP(a.spec(), inst, p, src, opts)
	if err != nil {
		return nil, err
	}
	if len(p.Skills) == 0 || inst.PackageDir == "" || inst.Scope != adapter.ScopeUser {
		return plan, nil
	}
	if src == nil {
		plan.Fidelity.AddLoss(adapter.LossNativeComponentDropped, "",
			"VS Code reads skills from a directory, so installing them requires the plugin's source; none was supplied")
		return plan, nil
	}

	// VS Code scans one level down: for each configured root it looks at the
	// immediate child directories and reads <child>/SKILL.md. Nothing below
	// that is examined, which is why the package cannot simply be dropped into
	// a shared skills directory the way every other client accepts it — the
	// skills sit one level too deep.
	//
	// Registering the package's own skills/ directory as a root turns that
	// around: its immediate children are exactly the skills, which is the
	// layout the scan wants, and the package stays a single unit that can be
	// removed as one.
	target := filepath.Join(inst.PackageDir, p.Name)
	settings := filepath.Join(filepath.Dir(inst.ConfigPath), "settings.json")

	key, ok := homeRelative(a.home, filepath.Join(target, "skills"))
	if !ok {
		plan.Fidelity.AddLoss(adapter.LossNativeComponentDropped, "",
			"VS Code only accepts a skills location under the home directory, and %s is not one", target)
		return plan, nil
	}

	doc, err := configedit.LoadJSON(settings)
	if err != nil {
		return nil, err
	}
	created := missingPrefix(doc, SkillsLocationsKey)
	keyPath := append(append([]string(nil), SkillsLocationsKey...), key)
	if err := doc.Set(keyPath, true); err != nil {
		return nil, err
	}
	after, err := doc.Bytes()
	if err != nil {
		return nil, err
	}

	plan.PackageDir = target
	plan.Ops = append([]adapter.Op{
		adapter.CopyTreeOp(target, src.Path(), "install plugin package"),
	}, plan.Ops...)
	plan.Ops = append(plan.Ops, adapter.Op{
		Kind:   adapter.OpWriteFile,
		Path:   settings,
		Before: doc.Original(),
		After:  after,
		Note:   "register the skills location",
	})
	plan.AuxConfigPath = settings
	plan.AuxConfigKeys = [][]string{keyPath}
	if len(created) > 0 {
		plan.AuxCreatedContainers = [][]string{created}
	}
	plan.Fidelity.Skills = adapter.Coverage{Carried: len(p.Skills), Total: len(p.Skills)}

	return plan, nil
}

// missingPrefix returns the shortest prefix of keys the document does not have,
// so removal can reclaim a container this install created and leave one the
// user already had.
func missingPrefix(doc *configedit.JSONDoc, keys []string) []string {
	for i := 1; i <= len(keys); i++ {
		prefix := keys[:i]
		if ok, err := doc.Has(prefix); err != nil {
			return nil
		} else if !ok {
			return append([]string(nil), prefix...)
		}
	}
	return nil
}

// PlanRemove implements adapter.Adapter.
func (a *Adapter) PlanRemove(inst adapter.Installation, pluginName string) (*adapter.Plan, error) {
	return a.PlanRemoveKeys(inst, pluginName, nil, nil)
}

// PlanRemoveKeys removes exactly the keys a receipt recorded, and the installed
// package with them. The registered skills location is taken back separately,
// from the aux config the receipt records — it lives in settings.json, not in
// the MCP file this plan otherwise edits.
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
