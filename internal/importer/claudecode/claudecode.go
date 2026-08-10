// Package claudecode imports plugins in Claude Code's plugin format.
//
// This importer is the reason the IR exists. Claude Code has the densest plugin
// ecosystem in the market and is not an Agent Plugins launch client, so
// bridging it is the single highest-value translation in the product
// (docs/01-vision-and-strategy.md section 5). It is also where the two formats
// disagree in ways that a naive converter would paper over silently.
//
// Where the formats agree:
//
//   - skills/<name>/SKILL.md is identical in both. This is the one component
//     that crosses without transformation.
//
// Where they differ, and what this importer does about it:
//
//   - The manifest lives at .claude-plugin/plugin.json and is optional; a
//     plugin with no manifest takes its name from the directory. Agent Plugins
//     requires plugin.json at the root.
//   - Claude Code MCP entries need no "type" field. Transport is inferred from
//     the shape and the inference is reported, because guessing wrong changes
//     how the server is connected.
//   - Placeholders are spelled ${CLAUDE_PLUGIN_ROOT} and ${CLAUDE_PLUGIN_DATA}.
//     They are rewritten to the portable spelling.
//   - Claude Code expands placeholders in "command"; Agent Plugins expands them
//     only in args, env values, and cwd. A command of
//     "${CLAUDE_PLUGIN_ROOT}/bin/server" therefore cannot be represented
//     literally, and is rewritten to the plugin-relative form "./bin/server".
//     This is the concrete round-trip hazard the M1-4 spike was meant to find.
//   - Claude Code has components Agent Plugins has no concept of: agents,
//     hooks, workflows, output styles, themes, monitors, LSP servers, bundled
//     executables, and plugin-level settings. These are preserved — the
//     manifest verbatim under an extension namespace, the on-disk components in
//     Native — and every one of them produces a diagnostic, because a user
//     exporting such a plugin to a client that only understands Agent Plugins
//     is losing real functionality and must be told which.
package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/agentbridge/agentbridge/internal/capability"
	"github.com/agentbridge/agentbridge/internal/diag"
	"github.com/agentbridge/agentbridge/internal/importer"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
	"github.com/agentbridge/agentbridge/internal/schema"
)

// Default component locations.
const (
	ManifestPath = ".claude-plugin/plugin.json"
	MCPPath      = ".mcp.json"
	SkillsDir    = "skills"
	CommandsDir  = "commands"
	RootSkill    = "SKILL.md"
)

// Claude Code's placeholder spellings.
const (
	placeholderRoot = "${CLAUDE_PLUGIN_ROOT}"
	placeholderData = "${CLAUDE_PLUGIN_DATA}"
)

// unsupportedComponents are Claude Code component locations with no Agent
// Plugins equivalent. Presence of any of them is preserved and reported.
var unsupportedComponents = []struct {
	path  string
	label string
}{
	{"agents", "subagent definitions"},
	{"hooks", "hook configuration"},
	{"workflows", "workflow scripts"},
	{"output-styles", "output styles"},
	{"themes", "color themes"},
	{"monitors", "background monitors"},
	{"bin", "bundled executables added to PATH"},
	{".lsp.json", "LSP server configuration"},
	{"settings.json", "plugin-level default settings"},
}

// Importer reads the Claude Code plugin format.
type Importer struct{}

// New returns a Claude Code importer.
func New() *Importer { return &Importer{} }

// Dialect implements importer.Importer.
func (*Importer) Dialect() ir.Dialect { return ir.DialectClaudeCode }

// Detect implements importer.Importer.
//
// A manifest is the unambiguous signal. Failing that, this format is
// identifiable by a component layout that Agent Plugins does not define — a
// bare skills/ directory alone is not enough, since that is exactly what the
// two formats share.
func (*Importer) Detect(root *safepath.Root) bool {
	if importer.Exists(root, ManifestPath) {
		return true
	}
	if importer.Exists(root, "plugin.json") {
		// An Agent Plugins manifest at the root: not our dialect.
		return false
	}
	for _, c := range unsupportedComponents {
		if importer.Exists(root, c.path) {
			return true
		}
	}
	return importer.Exists(root, MCPPath) || importer.IsDir(root, CommandsDir)
}

// Import implements importer.Importer.
func (i *Importer) Import(root *safepath.Root) (*importer.Result, error) {
	var ds diag.Diagnostics

	m, rawManifest, err := readManifest(root, &ds)
	if err != nil {
		return nil, err
	}

	name := m.Name
	if name == "" {
		name = path.Base(root.Declared())
		ds.Add(diag.Warning, diag.CodeManifestMissing, "",
			"no manifest name; using directory name %q, which changes if the directory is renamed", name)
	}
	// A Claude Code name need not satisfy the Agent Plugins name rule. That is
	// not an import failure — it is an export problem, and the user should
	// learn about it now rather than when a target client rejects the plugin.
	if err := schema.ValidateName(name); err != nil {
		ds.Add(diag.Warning, diag.CodeManifestInvalidName, ManifestPath,
			"name %q is not valid under Agent Plugins rules and must be changed before this plugin can be exported portably: %v", name, err)
	}

	p := &ir.Plugin{
		IRVersion:   ir.Version,
		Name:        name,
		Version:     m.Version,
		Description: m.Description,
		Homepage:    m.Homepage,
		Repository:  m.Repository,
		License:     m.License,
		Keywords:    m.Keywords,
		Origin: ir.Origin{
			Dialect:      ir.DialectClaudeCode,
			Root:         root.Path(),
			ManifestPath: manifestPathOrEmpty(root),
		},
	}
	if m.Author != nil {
		p.Author = &ir.Author{Name: m.Author.Name, Email: m.Author.Email, URL: m.Author.URL}
	}

	// Preserve the manifest verbatim under a reverse-domain namespace. The
	// specification requires clients to ignore namespaces they do not
	// implement, so this survives a trip through the portable format intact
	// and can be restored on the way back.
	if len(rawManifest) > 0 {
		p.Extensions = map[string]json.RawMessage{
			ir.ExtensionNamespaceClaudeCode: mustExtension(rawManifest),
		}
	}

	skills, bodies, sds := loadSkills(root, m, &ds)
	ds.Extend(sds)
	p.Skills = importer.DedupeSkills(skills, &ds)

	servers, mds := loadMCP(root, m)
	ds.Extend(mds)
	p.MCPServers = servers

	if native := recordUnsupported(root, m, &ds); native != nil {
		p.Native = map[string]json.RawMessage{"claude-code": native}
	}

	ds.Extend(capability.Infer(p, bodies))

	return &importer.Result{Plugin: p, Diagnostics: ds, SkillBodies: bodies}, nil
}

func manifestPathOrEmpty(root *safepath.Root) string {
	if importer.Exists(root, ManifestPath) {
		return ManifestPath
	}
	return ""
}

func readManifest(root *safepath.Root, ds *diag.Diagnostics) (*manifest, []byte, error) {
	if !importer.Exists(root, ManifestPath) {
		// The manifest is optional in this dialect; components are discovered
		// in their default locations.
		return &manifest{}, nil, nil
	}
	raw, _, err := importer.ReadJSON(root, ManifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}
	return &m, raw, nil
}

// loadSkills gathers skills from every layout Claude Code supports.
func loadSkills(root *safepath.Root, m *manifest, ds *diag.Diagnostics) ([]ir.Skill, map[string]string, diag.Diagnostics) {
	var out diag.Diagnostics
	bodies := map[string]string{}
	var skills []ir.Skill

	// The default skills/ scan always runs; the manifest's skills field adds
	// to it rather than replacing it.
	found, b, sds := importer.DiscoverDirSkills(root, SkillsDir)
	out.Extend(sds)
	skills = append(skills, found...)
	mergeBodies(bodies, b)

	for _, rel := range m.Skills.values() {
		rel = strings.TrimPrefix(rel, "./")
		if rel == "" || rel == "." {
			// "." and "./" denote the plugin root itself, used by
			// single-skill plugins.
			if s, body, sds := importer.LoadDirSkill(root, "."); s != nil {
				out.Extend(sds)
				s.Dir = ""
				skills = append(skills, *s)
				bodies[s.Name] = body
			} else {
				out.Extend(sds)
			}
			continue
		}
		if importer.Exists(root, path.Join(rel, "SKILL.md")) {
			s, body, sds := importer.LoadDirSkill(root, rel)
			out.Extend(sds)
			if s != nil {
				skills = append(skills, *s)
				bodies[s.Name] = body
			}
			continue
		}
		// Otherwise the entry names a directory of skill directories.
		found, b, sds := importer.DiscoverDirSkills(root, rel)
		out.Extend(sds)
		skills = append(skills, found...)
		mergeBodies(bodies, b)
	}

	// A plugin with a root SKILL.md, no skills/ directory and no skills field
	// is a single-skill plugin.
	if len(skills) == 0 && len(m.Skills.values()) == 0 && importer.Exists(root, RootSkill) {
		if s, body, sds := importer.LoadDirSkill(root, "."); s != nil {
			out.Extend(sds)
			s.Dir = ""
			skills = append(skills, *s)
			bodies[s.Name] = body
		} else {
			out.Extend(sds)
		}
	}

	// Flat command files. Agent Plugins has no flat-file skill layout, so
	// these need restructuring into a directory before they can be exported
	// portably.
	commandPaths := m.Commands.values()
	if len(commandPaths) == 0 {
		commandPaths = []string{CommandsDir}
	}
	for _, rel := range commandPaths {
		rel = strings.TrimPrefix(rel, "./")
		flat, b, sds := loadFlatSkills(root, rel)
		out.Extend(sds)
		skills = append(skills, flat...)
		mergeBodies(bodies, b)
	}

	return skills, bodies, out
}

func loadFlatSkills(root *safepath.Root, rel string) ([]ir.Skill, map[string]string, diag.Diagnostics) {
	var ds diag.Diagnostics
	bodies := map[string]string{}

	if strings.HasSuffix(rel, ".md") {
		if !importer.Exists(root, rel) {
			return nil, bodies, ds
		}
		s, body, sds := importer.LoadFlatSkill(root, rel)
		ds.Extend(sds)
		if s == nil {
			return nil, bodies, ds
		}
		ds.AddComponent(diag.Warning, diag.CodeSkillFlatCommand, rel, s.Name,
			"flat Markdown skill; Agent Plugins requires a directory containing SKILL.md, so this must be restructured to export portably")
		bodies[s.Name] = body
		return []ir.Skill{*s}, bodies, ds
	}

	abs, err := root.Resolve(rel)
	if err != nil {
		return nil, bodies, ds
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, bodies, ds
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var skills []ir.Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		fileRel := path.Join(rel, e.Name())
		s, body, sds := importer.LoadFlatSkill(root, fileRel)
		ds.Extend(sds)
		if s == nil {
			continue
		}
		ds.AddComponent(diag.Warning, diag.CodeSkillFlatCommand, fileRel, s.Name,
			"flat Markdown skill; Agent Plugins requires a directory containing SKILL.md, so this must be restructured to export portably")
		skills = append(skills, *s)
		bodies[s.Name] = body
	}
	return skills, bodies, ds
}

func mergeBodies(dst, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

// loadMCP reads MCP configuration from .mcp.json and from the manifest's
// mcpServers field, which may be inline or a path.
func loadMCP(root *safepath.Root, m *manifest) ([]ir.MCPServer, diag.Diagnostics) {
	var ds diag.Diagnostics
	collected := map[string]json.RawMessage{}

	if importer.Exists(root, MCPPath) {
		readServerFile(root, MCPPath, collected, &ds)
	}

	switch {
	case len(m.MCPServers.object) > 0:
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(m.MCPServers.object, &doc); err != nil {
			ds.Add(diag.Error, diag.CodeMCPInvalidJSON, ManifestPath,
				"inline mcpServers was not loaded: %v", err)
			break
		}
		// Inline configuration may be given either as the servers map itself
		// or wrapped in an mcpServers key.
		if inner, ok := doc["mcpServers"]; ok {
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(inner, &nested); err == nil {
				doc = nested
			}
		}
		for k, v := range doc {
			collected[k] = v
		}
	default:
		for _, rel := range m.MCPServers.values() {
			readServerFile(root, strings.TrimPrefix(rel, "./"), collected, &ds)
		}
	}

	var servers []ir.MCPServer
	for _, name := range importer.SortedKeys(collected) {
		srv, ok := convertServer(root, name, collected[name], &ds)
		if !ok {
			continue
		}
		if _, err := srv.ComputeContentHash(); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, MCPPath, name,
				"server was skipped: %v", err)
			continue
		}
		servers = append(servers, *srv)
	}
	return servers, ds
}

func readServerFile(root *safepath.Root, rel string, into map[string]json.RawMessage, ds *diag.Diagnostics) {
	raw, _, err := importer.ReadJSON(root, rel)
	if err != nil {
		ds.Add(diag.Error, diag.CodeMCPInvalidJSON, rel,
			"MCP configuration was not loaded: %v", err)
		return
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		ds.Add(diag.Error, diag.CodeMCPInvalidJSON, rel,
			"MCP configuration was not loaded: %v", err)
		return
	}
	for k, v := range doc.MCPServers {
		if _, dup := into[k]; dup {
			ds.AddComponent(diag.Warning, diag.CodeMCPServerDuplicate, rel, k,
				"server is defined more than once; the definition in %s was used", rel)
		}
		into[k] = v
	}
}

// convertServer translates one Claude Code server entry into the IR, applying
// the transport inference and placeholder rewrites this dialect requires.
func convertServer(root *safepath.Root, name string, raw json.RawMessage, ds *diag.Diagnostics) (*ir.MCPServer, bool) {
	var e serverEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, MCPPath, name,
			"server was skipped: %v", err)
		return nil, false
	}

	transport, ok := inferTransport(name, e, ds)
	if !ok {
		return nil, false
	}

	srv := &ir.MCPServer{
		Name:      name,
		Transport: transport,
		Command:   e.Command,
		Args:      append([]string(nil), e.Args...),
		Env:       maps(e.Env),
		Cwd:       e.Cwd,
		URL:       e.URL,
		Headers:   maps(e.Headers),
	}

	rewritePlaceholders(srv, ds)

	switch transport {
	case ir.TransportStdio:
		importer.CheckReservedEnv(name, srv.Env, ds)
		if !importer.CheckStdioCommand(root, name, srv.Command, ds) {
			return nil, false
		}
		if !importer.CheckCwd(root, name, srv.Cwd, ds) {
			return nil, false
		}
	case ir.TransportStreamableHTTP, ir.TransportSSE:
		if !importer.CheckServerURL(name, srv.URL, ds) {
			return nil, false
		}
	}

	return srv, true
}

// inferTransport determines the transport for an entry that may not declare
// one. Claude Code allows the type to be implied by shape; Agent Plugins
// requires it explicitly, so the inference is recorded.
func inferTransport(name string, e serverEntry, ds *diag.Diagnostics) (ir.Transport, bool) {
	switch strings.ToLower(e.Type) {
	case "stdio":
		return ir.TransportStdio, true
	case "streamable-http", "http":
		return ir.TransportStreamableHTTP, true
	case "sse":
		return ir.TransportSSE, true
	case "ws", "websocket":
		// Agent Plugins defines no WebSocket transport, so there is nothing
		// portable to translate this into.
		ds.AddComponent(diag.Error, diag.CodeComponentUnsupport, MCPPath, name,
			"server was skipped: WebSocket transport has no Agent Plugins equivalent")
		return "", false
	case "":
		// Inferred from shape.
	default:
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, MCPPath, name,
			"server was skipped: unknown transport %q", e.Type)
		return "", false
	}

	switch {
	case e.Command != "":
		ds.AddComponent(diag.Info, diag.CodeMCPTransportInferred, MCPPath, name,
			"no transport declared; inferred stdio from the presence of a command")
		return ir.TransportStdio, true
	case e.URL != "":
		ds.AddComponent(diag.Info, diag.CodeMCPTransportInferred, MCPPath, name,
			"no transport declared; inferred streamable-http from the presence of a url")
		return ir.TransportStreamableHTTP, true
	default:
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, MCPPath, name,
			"server was skipped: entry has neither a command nor a url")
		return "", false
	}
}

// rewritePlaceholders translates Claude Code's placeholder spellings to the
// portable ones, and handles the one case where the two formats genuinely
// cannot express the same thing.
func rewritePlaceholders(srv *ir.MCPServer, ds *diag.Diagnostics) {
	// Agent Plugins expands placeholders only in args, env values, and cwd.
	// A command containing one must become a plugin-relative path instead.
	if strings.Contains(srv.Command, placeholderRoot) {
		rewritten := strings.TrimPrefix(strings.ReplaceAll(srv.Command, placeholderRoot, "."), "//")
		if !strings.HasPrefix(rewritten, "./") {
			rewritten = "./" + strings.TrimPrefix(rewritten, "/")
		}
		ds.AddComponent(diag.Info, diag.CodeMCPCommandRewritten, MCPPath, srv.Name,
			"command %q rewritten to %q: Agent Plugins expands placeholders only in args, env values and cwd, not in command",
			srv.Command, rewritten)
		srv.Command = rewritten
	}
	if strings.Contains(srv.Command, placeholderData) {
		// PLUGIN_DATA is outside the package, so there is no plugin-relative
		// form to fall back to. The server is left as-is and flagged: this is
		// a genuine expressiveness gap between the formats, not a bug.
		ds.AddComponent(diag.Warning, diag.CodeComponentUnsupport, MCPPath, srv.Name,
			"command references %s, which Agent Plugins cannot express in a command; this server will not run correctly in a client that only understands the portable format", placeholderData)
	}

	replaced := false
	rewrite := func(s string) string {
		out := strings.ReplaceAll(s, placeholderRoot, ir.PlaceholderPluginRoot)
		out = strings.ReplaceAll(out, placeholderData, ir.PlaceholderPluginData)
		if out != s {
			replaced = true
		}
		return out
	}

	for i, a := range srv.Args {
		srv.Args[i] = rewrite(a)
	}
	for k, v := range srv.Env {
		srv.Env[k] = rewrite(v)
	}
	for k, v := range srv.Headers {
		srv.Headers[k] = rewrite(v)
	}
	srv.Cwd = rewrite(srv.Cwd)

	if replaced {
		ds.AddComponent(diag.Info, diag.CodeMCPPlaceholderRewrit, MCPPath, srv.Name,
			"rewrote %s and %s to the portable %s and %s spellings",
			placeholderRoot, placeholderData, ir.PlaceholderPluginRoot, ir.PlaceholderPluginData)
	}
}

// recordUnsupported preserves the Claude Code components that Agent Plugins has
// no concept of, and reports each one. Preservation is what makes a same-dialect
// round trip lossless; the diagnostics are what stop a cross-dialect export
// from quietly producing a less capable plugin.
func recordUnsupported(root *safepath.Root, m *manifest, ds *diag.Diagnostics) json.RawMessage {
	present := map[string]string{}

	for _, c := range unsupportedComponents {
		if !importer.Exists(root, c.path) {
			continue
		}
		present[c.path] = c.label
		ds.Add(diag.Warning, diag.CodeComponentUnsupport, c.path,
			"%s has no Agent Plugins equivalent; preserved but not portable", c.label)
	}

	for field, label := range map[string]string{
		"hooks": "hook configuration", "agents": "subagent definitions",
		"workflows": "workflow scripts", "outputStyles": "output styles",
		"lspServers": "LSP server configuration", "userConfig": "user configuration prompts",
		"channels": "channel declarations", "dependencies": "plugin dependencies",
	} {
		if !m.hasField(field) {
			continue
		}
		if _, already := present["manifest:"+field]; already {
			continue
		}
		present["manifest:"+field] = label
		ds.Add(diag.Warning, diag.CodeComponentUnsupport, ManifestPath,
			"manifest field %q (%s) has no Agent Plugins equivalent; preserved but not portable", field, label)
	}

	if len(present) == 0 {
		return nil
	}
	ds.Add(diag.Info, diag.CodeComponentPreserved, "",
		"%d Claude Code component(s) preserved for round-tripping back to this format", len(present))

	blob, err := json.Marshal(map[string]any{"unsupportedComponents": present})
	if err != nil {
		return nil
	}
	return blob
}

func mustExtension(raw json.RawMessage) json.RawMessage {
	blob, err := json.Marshal(map[string]json.RawMessage{"manifest": raw})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return blob
}

func maps(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

type manifest struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"displayName"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Author      *manifestAuthor `json:"author"`
	Homepage    string          `json:"homepage"`
	Repository  string          `json:"repository"`
	License     string          `json:"license"`
	Keywords    []string        `json:"keywords"`

	Skills     flexStrings `json:"skills"`
	Commands   flexStrings `json:"commands"`
	MCPServers flexValue   `json:"mcpServers"`

	// Fields with no Agent Plugins equivalent, captured only so their presence
	// can be reported. Contents are preserved verbatim via the extension
	// namespace, not through these.
	Hooks        json.RawMessage `json:"hooks"`
	Agents       json.RawMessage `json:"agents"`
	Workflows    json.RawMessage `json:"workflows"`
	OutputStyles json.RawMessage `json:"outputStyles"`
	LSPServers   json.RawMessage `json:"lspServers"`
	UserConfig   json.RawMessage `json:"userConfig"`
	Channels     json.RawMessage `json:"channels"`
	Dependencies json.RawMessage `json:"dependencies"`
}

func (m *manifest) hasField(name string) bool {
	switch name {
	case "hooks":
		return len(m.Hooks) > 0
	case "agents":
		return len(m.Agents) > 0
	case "workflows":
		return len(m.Workflows) > 0
	case "outputStyles":
		return len(m.OutputStyles) > 0
	case "lspServers":
		return len(m.LSPServers) > 0
	case "userConfig":
		return len(m.UserConfig) > 0
	case "channels":
		return len(m.Channels) > 0
	case "dependencies":
		return len(m.Dependencies) > 0
	}
	return false
}

type manifestAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	URL   string `json:"url"`
}

// flexStrings accepts either a single string or an array of strings, which is
// how Claude Code spells most of its component path fields.
type flexStrings struct {
	items []string
}

func (f *flexStrings) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		f.items = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	f.items = many
	return nil
}

func (f flexStrings) values() []string { return f.items }

// flexValue accepts a string, an array of strings, or an inline object. The
// mcpServers field can be any of the three.
type flexValue struct {
	items  []string
	object json.RawMessage
}

func (f *flexValue) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if strings.HasPrefix(trimmed, "{") {
		f.object = append(json.RawMessage(nil), b...)
		return nil
	}
	var fs flexStrings
	if err := fs.UnmarshalJSON(b); err != nil {
		return err
	}
	f.items = fs.items
	return nil
}

func (f flexValue) values() []string { return f.items }

type serverEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Cwd     string            `json:"cwd"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}
