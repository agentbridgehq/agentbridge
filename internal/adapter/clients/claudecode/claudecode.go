// Package claudecode installs plugins into Claude Code.
//
// This adapter carries more than any other, which is the point: Claude Code is
// absent from the Agent Plugins standard and has the densest plugin ecosystem
// in the market, so the bridge is worth least if it cannot reach it.
//
// Claude Code documents a plugin layout that takes skills *and* MCP servers as
// a unit — any directory under a skills directory containing
// .claude-plugin/plugin.json is loaded as a plugin — so a whole package can be
// installed rather than MCP configuration alone. Skills therefore reach full
// coverage here while every other target reports them as not installed.
//
// The install works by copying the plugin's source tree and then writing the
// two files Claude Code reads, which means components the IR has no field for —
// agents, hooks, workflows, bundled executables — travel with it intact rather
// than being reconstructed from what we happened to model.
//
// Placeholders are rewritten back to Claude Code's spellings, completing the
// round trip the importer began: ${PLUGIN_ROOT} becomes ${CLAUDE_PLUGIN_ROOT},
// and a plugin-relative "./bin/server" becomes "${CLAUDE_PLUGIN_ROOT}/bin/server",
// which is exactly the form the importer had to rewrite on the way in.
//
// Paths: https://code.claude.com/docs/en/plugins-reference
package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
)

// ID is the client identifier.
const ID = "claude-code"

// Claude Code's placeholder spellings.
const (
	placeholderRoot = "${CLAUDE_PLUGIN_ROOT}"
	placeholderData = "${CLAUDE_PLUGIN_DATA}"
)

// Adapter installs a full plugin package into Claude Code.
type Adapter struct{}

// New returns a Claude Code adapter.
func New() *Adapter { return &Adapter{} }

// Client implements adapter.Adapter.
func (*Adapter) Client() adapter.Client {
	return adapter.Client{
		ID:   ID,
		Name: "Claude Code",
		// Not an Agent Plugins launch client; we translate into its own
		// documented format.
		Conformant: false,
		Skills:     adapter.SupportTranslated,
		MCP:        adapter.SupportTranslated,
		ConfigDoc:  "https://code.claude.com/docs/en/plugins-reference",
	}
}

// Detect implements adapter.Adapter.
func (a *Adapter) Detect(env adapter.Env) []adapter.Installation {
	var out []adapter.Installation

	userDir := filepath.Join(env.HomeDir, ".claude")
	if isDir(userDir) {
		out = append(out, adapter.Installation{
			Client:     a.Client(),
			Scope:      adapter.ScopeUser,
			PackageDir: filepath.Join(userDir, "skills"),
			Evidence:   userDir + " exists",
		})
	}

	if env.ProjectDir != "" {
		projectDir := filepath.Join(env.ProjectDir, ".claude")
		if isDir(projectDir) {
			out = append(out, adapter.Installation{
				Client:     a.Client(),
				Scope:      adapter.ScopeProject,
				PackageDir: filepath.Join(projectDir, "skills"),
				Evidence:   projectDir + " exists",
			})
		}
	}
	return out
}

// Plan implements adapter.Adapter.
func (a *Adapter) Plan(inst adapter.Installation, p *ir.Plugin, src *safepath.Root, opts adapter.PlanOptions) (*adapter.Plan, error) {
	plan := &adapter.Plan{Installation: inst, PluginName: p.Name}

	target := filepath.Join(inst.PackageDir, p.Name)
	plan.PackageDir = target

	if src == nil {
		plan.Fidelity.Skills = adapter.Coverage{Total: len(p.Skills)}
		plan.Fidelity.MCPServers = adapter.Coverage{Total: len(p.MCPServers)}
		plan.Fidelity.AddLoss(adapter.LossNativeComponentDropped, "",
			"Claude Code takes a whole plugin directory, so installing requires the plugin's source; none was supplied")
		return plan, nil
	}

	// Copy the source wholesale. Reconstructing only the components the IR
	// models would silently drop everything it does not — which for a Claude
	// Code plugin is most of what makes it useful.
	plan.Ops = append(plan.Ops, adapter.Op{
		Kind:      adapter.OpCopyTree,
		Path:      target,
		SourceDir: src.Path(),
		Note:      "install plugin package",
	})

	manifest, err := buildManifest(p)
	if err != nil {
		return nil, err
	}
	plan.Ops = append(plan.Ops, adapter.Op{
		Kind: adapter.OpWriteFile,
		Path: filepath.Join(target, ".claude-plugin", "plugin.json"),
		// Before is nil: the copy above establishes the directory's contents,
		// so there is nothing meaningful to diff against.
		After: manifest,
		Note:  "write Claude Code manifest",
	})

	plan.Fidelity.Skills = adapter.Coverage{Carried: len(p.Skills), Total: len(p.Skills)}

	servers := adapter.SortServers(p.MCPServers)
	plan.Fidelity.MCPServers.Total = len(servers)
	if len(servers) > 0 {
		mcp, carried, notes, err := buildMCP(servers, opts, &plan.Fidelity, filepath.Join(target, ".mcp.json"))
		plan.SecretNotes = append(plan.SecretNotes, notes...)
		if err != nil {
			return nil, err
		}
		plan.Fidelity.MCPServers.Carried = carried
		plan.Ops = append(plan.Ops, adapter.Op{
			Kind:  adapter.OpWriteFile,
			Path:  filepath.Join(target, ".mcp.json"),
			After: mcp,
			Note:  "write MCP server configuration",
		})
	}

	for _, s := range p.Skills {
		if s.Kind == ir.SkillFlatFile {
			// Claude Code reads flat command files natively, so this is a note
			// about portability rather than a loss here.
			plan.Fidelity.AddLoss(adapter.LossFlatSkillRestructured, s.Name,
				"loaded as a flat command file, which Claude Code supports but the portable format does not")
		}
	}

	return plan, nil
}

// PlanRemove implements adapter.Adapter.
func (a *Adapter) PlanRemove(inst adapter.Installation, pluginName string) (*adapter.Plan, error) {
	target := filepath.Join(inst.PackageDir, pluginName)
	return &adapter.Plan{
		Installation: inst,
		PluginName:   pluginName,
		PackageDir:   target,
		Ops: []adapter.Op{{
			Kind:         adapter.OpRemoveTree,
			Path:         target,
			TargetExists: adapter.PathExists(target),
			Note:         "remove plugin package",
		}},
	}, nil
}

// buildManifest writes Claude Code's manifest, restoring anything the importer
// preserved from an original Claude Code plugin so a round trip through the
// portable format does not quietly lose manifest fields.
func buildManifest(p *ir.Plugin) ([]byte, error) {
	manifest := map[string]any{}

	if raw, ok := p.Extensions[ir.ExtensionNamespaceClaudeCode]; ok {
		var preserved struct {
			Manifest json.RawMessage `json:"manifest"`
		}
		if err := json.Unmarshal(raw, &preserved); err == nil && len(preserved.Manifest) > 0 {
			if err := json.Unmarshal(preserved.Manifest, &manifest); err != nil {
				manifest = map[string]any{}
			}
		}
	}

	// Portable identity wins over the preserved copy: it is what the user
	// asked to install, and it may have been updated since.
	manifest["name"] = p.Name
	setIfNotEmpty(manifest, "version", p.Version)
	setIfNotEmpty(manifest, "description", p.Description)
	setIfNotEmpty(manifest, "homepage", p.Homepage)
	setIfNotEmpty(manifest, "repository", p.Repository)
	setIfNotEmpty(manifest, "license", p.License)
	if len(p.Keywords) > 0 {
		manifest["keywords"] = p.Keywords
	}
	if p.Author != nil {
		author := map[string]any{}
		setIfNotEmpty(author, "name", p.Author.Name)
		setIfNotEmpty(author, "email", p.Author.Email)
		setIfNotEmpty(author, "url", p.Author.URL)
		if len(author) > 0 {
			manifest["author"] = author
		}
	}

	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// buildMCP writes .mcp.json, translating the portable placeholders back into
// the spellings Claude Code expands.
func buildMCP(servers []ir.MCPServer, opts adapter.PlanOptions, f *adapter.Fidelity, configPath string) ([]byte, int, []adapter.SecretNote, error) {
	out := map[string]any{}
	carried := 0
	var notes []adapter.SecretNote

	for _, s := range servers {
		prepared, n, allowed := adapter.PrepareSecrets(s, opts, f, configPath)
		if !allowed {
			continue
		}
		notes = append(notes, n...)
		s = prepared

		entry := map[string]any{}

		switch s.Transport {
		case ir.TransportStdio:
			entry["command"] = toClaudePath(s.Command)
			if len(s.Args) > 0 {
				entry["args"] = mapStrings(s.Args, toClaudePlaceholders)
			}
			env := map[string]string{}
			for k, v := range s.Env {
				env[k] = toClaudePlaceholders(v)
			}
			// Spec 9.1 requires every plugin subprocess to receive PLUGIN_ROOT
			// and PLUGIN_DATA. Claude Code supplies its own CLAUDE_-prefixed
			// variables and knows nothing about these, so a portable plugin
			// reading the spec-mandated names would find them unset. Mapping
			// them onto Claude Code's placeholders is exact and costs nothing:
			// Claude Code expands placeholders in env values, so the process
			// receives the same absolute paths a conformant client would give
			// it. Set last, which is also the precedence §9.1 requires.
			env["PLUGIN_ROOT"] = placeholderRoot
			env["PLUGIN_DATA"] = placeholderData
			entry["env"] = env
			if s.Cwd != "" {
				entry["cwd"] = toClaudePlaceholders(s.Cwd)
			}

		case ir.TransportStreamableHTTP:
			entry["type"] = "http"
			entry["url"] = s.URL
			if len(s.Headers) > 0 {
				entry["headers"] = s.Headers
			}

		case ir.TransportSSE:
			entry["type"] = "sse"
			entry["url"] = s.URL
			if len(s.Headers) > 0 {
				entry["headers"] = s.Headers
			}

		default:
			f.AddLoss(adapter.LossTransportUnsupported, s.Name,
				"Claude Code: unknown transport %q", s.Transport)
			continue
		}

		out[s.Name] = entry
		carried++
	}

	raw, err := json.MarshalIndent(map[string]any{"mcpServers": out}, "", "  ")
	if err != nil {
		return nil, 0, nil, err
	}
	return append(raw, '\n'), carried, notes, nil
}

// toClaudePath converts a plugin-relative command into the placeholder form
// Claude Code expands. This is the exact inverse of the rewrite the importer
// performs, and doing it here is what makes the round trip close.
func toClaudePath(command string) string {
	if strings.HasPrefix(command, "./") {
		return placeholderRoot + "/" + strings.TrimPrefix(command, "./")
	}
	return toClaudePlaceholders(command)
}

func toClaudePlaceholders(v string) string {
	v = strings.ReplaceAll(v, ir.PlaceholderPluginRoot, placeholderRoot)
	v = strings.ReplaceAll(v, ir.PlaceholderPluginData, placeholderData)
	return v
}

func mapStrings(in []string, f func(string) string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = f(s)
	}
	return out
}

func setIfNotEmpty(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
