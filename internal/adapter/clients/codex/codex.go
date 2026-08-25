// Package codex installs plugins into Codex CLI.
//
// Codex is the only target whose configuration is TOML rather than JSON, which
// is why internal/configedit carries a second strategy. Rather than reformat a
// user's hand-written TOML — comments, table ordering and all — this adapter
// owns a single marker-delimited block at the end of the file and leaves every
// byte outside it untouched.
//
// Servers live under [mcp_servers.<name>]. Streamable HTTP servers take a url;
// stdio servers take command, args and env.
//
// Paths: https://developers.openai.com/codex/mcp — user $CODEX_HOME/config.toml
// (default ~/.codex/config.toml), project <root>/.codex/config.toml.
package codex

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentbridgehq/agentbridge/internal/adapter"
	"github.com/agentbridgehq/agentbridge/internal/configedit"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// ID is the client identifier.
const ID = "codex"

// Adapter writes Codex's config.toml.
type Adapter struct{ dataDir func(string) string }

// New returns a Codex adapter.
func New(pluginDataDir func(pluginName string) string) *Adapter {
	return &Adapter{dataDir: pluginDataDir}
}

// Client implements adapter.Adapter.
func (*Adapter) Client() adapter.Client {
	return adapter.Client{
		ID:         ID,
		Name:       "Codex",
		Conformant: true,
		Skills:     adapter.SupportUndocumented,
		MCP:        adapter.SupportTranslated,
		ConfigDoc:  "https://developers.openai.com/codex/mcp",
		Losses:     adapter.DeclaredLosses(adapter.LossSkillsUndocumented),
	}
}

// Home returns the Codex home directory, honoring CODEX_HOME.
func Home(env adapter.Env) string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(env.HomeDir, ".codex")
}

// Detect implements adapter.Adapter.
func (a *Adapter) Detect(env adapter.Env) []adapter.Installation {
	var out []adapter.Installation

	home := Home(env)
	if isDir(home) {
		out = append(out, adapter.Installation{
			Client:     a.Client(),
			Scope:      adapter.ScopeUser,
			ConfigPath: filepath.Join(home, "config.toml"),
			Evidence:   home + " exists",
		})
	}

	if env.ProjectDir != "" {
		projectDir := filepath.Join(env.ProjectDir, ".codex")
		if isDir(projectDir) {
			out = append(out, adapter.Installation{
				Client:     a.Client(),
				Scope:      adapter.ScopeProject,
				ConfigPath: filepath.Join(projectDir, "config.toml"),
				Evidence:   projectDir + " exists",
			})
		}
	}
	return out
}

// Plan implements adapter.Adapter.
func (a *Adapter) Plan(inst adapter.Installation, p *ir.Plugin, src *safepath.Root, opts adapter.PlanOptions) (*adapter.Plan, error) {
	plan := &adapter.Plan{Installation: inst, PluginName: p.Name}
	plan.Fidelity.Skills = adapter.Coverage{Total: len(p.Skills)}
	adapter.NoteSkillsUnsupported(&plan.Fidelity, a.Client(), p.Skills)

	doc, err := configedit.LoadBlock(inst.ConfigPath)
	if err != nil {
		return nil, err
	}

	pluginRoot := ""
	if src != nil {
		pluginRoot = src.Path()
	}
	pluginData := a.dataDir(p.Name)

	servers := adapter.SortServers(p.MCPServers)
	plan.Fidelity.MCPServers.Total = len(servers)

	var sections []string
	for _, s := range servers {
		materialized := adapter.Materialize(s, pluginRoot, pluginData)

		prepared, notes, allowed := adapter.PrepareSecrets(materialized, opts, &plan.Fidelity, inst.ConfigPath)
		if !allowed {
			continue
		}
		plan.SecretNotes = append(plan.SecretNotes, notes...)
		materialized = prepared

		header, lines, reason, ok := renderServer(p.Name, materialized)
		if !ok {
			plan.Fidelity.AddLoss(adapter.LossTransportUnsupported, s.Name, "Codex: %s", reason)
			continue
		}
		doc.SetSection(header, lines)
		sections = append(sections, header)
		plan.Fidelity.MCPServers.Carried++
	}

	for ns := range p.Extensions {
		plan.Fidelity.AddLoss(adapter.LossExtensionsDropped, "",
			"extension namespace %q is not carried into Codex, which has no place to put it", ns)
	}

	plan.Ops = []adapter.Op{{
		Kind:   adapter.OpWriteFile,
		Path:   inst.ConfigPath,
		Before: doc.Original(),
		After:  doc.Bytes(),
		Note:   "update the agentbridge managed block",
	}}
	plan.BlockSections = sections
	return plan, nil
}

// PlanRemove implements adapter.Adapter.
func (a *Adapter) PlanRemove(inst adapter.Installation, pluginName string) (*adapter.Plan, error) {
	return a.PlanRemoveSections(inst, pluginName, nil)
}

// PlanRemoveSections removes exactly the sections a receipt recorded.
func (a *Adapter) PlanRemoveSections(inst adapter.Installation, pluginName string, sections []string) (*adapter.Plan, error) {
	plan := &adapter.Plan{Installation: inst, PluginName: pluginName}

	doc, err := configedit.LoadBlock(inst.ConfigPath)
	if err != nil {
		return nil, err
	}
	if !doc.Existed() {
		return plan, nil
	}

	for _, section := range sections {
		doc.DeleteSection(section)
	}

	plan.Ops = []adapter.Op{{
		Kind:   adapter.OpWriteFile,
		Path:   inst.ConfigPath,
		Before: doc.Original(),
		After:  doc.Bytes(),
		Note:   "remove managed block entries",
	}}
	return plan, nil
}

// renderServer produces the TOML table for one server, returning its header
// line so the section can be replaced or removed later.
func renderServer(pluginName string, s ir.MCPServer) (header string, lines []string, reason string, ok bool) {
	key := adapter.ManagedKey(pluginName, s.Name)
	// The key is quoted because it contains a period, which TOML would
	// otherwise read as another level of table nesting.
	header = fmt.Sprintf("[mcp_servers.%s]", configedit.TOMLString(key))
	lines = []string{header}

	switch s.Transport {
	case ir.TransportStdio:
		lines = append(lines, fmt.Sprintf("command = %s", configedit.TOMLString(s.Command)))
		if len(s.Args) > 0 {
			lines = append(lines, fmt.Sprintf("args = %s", configedit.TOMLStringArray(s.Args)))
		}
		if len(s.Env) > 0 {
			lines = append(lines, fmt.Sprintf("env = %s", configedit.TOMLInlineTable(s.Env)))
		}
		return header, lines, "", true

	case ir.TransportStreamableHTTP:
		lines = append(lines, fmt.Sprintf("url = %s", configedit.TOMLString(s.URL)))
		return header, lines, "", true

	case ir.TransportSSE:
		// Codex documents a url-based Streamable HTTP server. Legacy HTTP+SSE
		// is not something it advertises, so writing the entry and hoping is
		// not appropriate; say so instead.
		return "", nil, "legacy HTTP+SSE transport is not documented for Codex", false

	default:
		return "", nil, "unknown transport " + string(s.Transport), false
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
