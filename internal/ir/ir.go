// Package ir defines AgentBridge's internal representation of a plugin.
//
// The IR is the core architectural decision (docs/03-architecture.md section
// 1): the Agent Plugins specification is treated as one input dialect among
// several, not as the model. Claude Code plugins, bare mcp.json fragments, and
// whatever ships in a future spec revision all normalize into these types.
// Everything downstream — adapters, lockfile, policy, scanning — operates on
// the IR, so a change to any single dialect stays contained in one importer.
//
// Two properties matter more than convenience:
//
//   - The IR is a superset. Data a dialect carries that has no portable home
//     goes into Extensions (spec-defined, reverse-domain keyed) or Native
//     (dialect-specific), rather than being dropped. What cannot round-trip is
//     reported as a diagnostic, never discarded silently.
//   - The IR is content-addressable. Digest is stable across machines and
//     across runs for identical input, which is what makes the lockfile and
//     integrity checking possible.
package ir

import (
	"encoding/json"
)

// Version identifies the IR schema itself. Bump on a breaking change to these
// types; consumers pin it.
const Version = "agentbridge/ir/v0"

// Dialect identifies the source format a plugin was imported from.
type Dialect string

const (
	DialectAgentPlugins Dialect = "agent-plugins@1.0.0"
	DialectClaudeCode   Dialect = "claude-code"
	DialectMCPJSON      Dialect = "mcp.json"
)

// Transport is an MCP connection type as named by the Agent Plugins schema.
type Transport string

const (
	TransportStdio          Transport = "stdio"
	TransportStreamableHTTP Transport = "streamable-http"
	TransportSSE            Transport = "sse"
)

// Placeholders defined by the Agent Plugins specification. These are the only
// two a conformant client expands, and only in args, env values, and cwd.
const (
	PlaceholderPluginRoot = "${PLUGIN_ROOT}"
	PlaceholderPluginData = "${PLUGIN_DATA}"
)

// ExtensionNamespaceClaudeCode is the reverse-domain namespace under which we
// preserve Claude Code component data that Agent Plugins has no field for.
//
// Provisional: Anthropic has not declared a namespace for the Agent Plugins
// extensions object. If one is published, migrate to it — the specification
// requires clients to ignore namespaces they do not implement, so carrying an
// unrecognized key is safe in the meantime.
const ExtensionNamespaceClaudeCode = "com.anthropic.claude-code"

// Plugin is the normalized form of a plugin from any dialect.
type Plugin struct {
	IRVersion string `json:"irVersion"`

	// Portable identity, mirroring the Agent Plugins manifest.
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      *Author  `json:"author,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	License     string   `json:"license,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`

	// Components.
	Skills     []Skill     `json:"skills,omitempty"`
	MCPServers []MCPServer `json:"mcpServers,omitempty"`

	// Extensions holds reverse-domain-keyed client data verbatim. The
	// specification requires clients to ignore namespaces they do not
	// implement *without validating their contents*, so these stay opaque:
	// preserved on import, re-emitted on export, never interpreted.
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`

	// Native holds dialect-specific data that has no Agent Plugins equivalent
	// and no extension namespace of its own — for example a Claude Code
	// plugin's hooks or agents. Keeping it lets an import/export round trip
	// within the same dialect stay lossless, while a cross-dialect export
	// reports precisely what it cannot carry.
	Native map[string]json.RawMessage `json:"native,omitempty"`

	Capabilities Capabilities `json:"capabilities"`

	// Origin records where this plugin came from. Excluded from Digest so the
	// same bytes produce the same digest on any machine.
	Origin Origin `json:"origin"`
}

// Author mirrors the Agent Plugins author object.
type Author struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Origin describes where a plugin was loaded from.
type Origin struct {
	Dialect Dialect `json:"dialect"`
	// SchemaID is the `$schema` value the source declared, when it had one.
	SchemaID string `json:"schemaId,omitempty"`
	// Root is the absolute filesystem path the plugin was loaded from. Machine
	// specific, therefore excluded from the digest.
	Root string `json:"root,omitempty"`
	// ManifestPath is the plugin-relative path of the manifest that was read,
	// empty when the dialect allows a manifest-less plugin.
	ManifestPath string `json:"manifestPath,omitempty"`
}

// SkillKind distinguishes the layouts a skill can arrive in.
type SkillKind string

const (
	// SkillDirectory is the portable form: a directory containing SKILL.md.
	SkillDirectory SkillKind = "directory"
	// SkillFlatFile is a bare Markdown file used as a skill. Claude Code's
	// commands/ directory uses this; Agent Plugins has no equivalent, so a
	// flat skill cannot be exported portably without being restructured.
	SkillFlatFile SkillKind = "flat-file"
)

// Skill is one Agent Skill.
//
// The body is deliberately not stored. Capability inference runs at import
// time while the content is in hand, and the hash is what the lockfile and
// integrity checks need. Keeping bodies out of the IR keeps lockfiles small
// and avoids copying untrusted instruction text through every layer.
type Skill struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Kind        SkillKind `json:"kind"`
	// Dir is the plugin-relative directory of a directory skill, empty for a
	// flat file.
	Dir string `json:"dir,omitempty"`
	// Entrypoint is the plugin-relative path of the Markdown file.
	Entrypoint string `json:"entrypoint"`
	// Frontmatter is the parsed YAML frontmatter, preserved whole. Agent
	// Skills owns this format; we do not impose a schema on it.
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
	// ContentHash is sha256 of the entrypoint file's raw bytes.
	ContentHash string `json:"contentHash"`
}

// MCPServer is one MCP server configuration, normalized across dialects.
type MCPServer struct {
	Name      string    `json:"name"`
	Transport Transport `json:"transport"`

	// stdio.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`

	// streamable-http and sse.
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// ContentHash is sha256 of the server's canonical form, used to detect a
	// changed server across versions without diffing the whole plugin.
	ContentHash string `json:"contentHash"`
}

// Capability names a class of access a plugin can obtain.
type Capability string

const (
	// CapExec means the plugin causes a process to run on the user's machine.
	// Every stdio MCP server implies it.
	CapExec Capability = "exec"
	// CapNetwork means the plugin talks to a remote endpoint.
	CapNetwork Capability = "network"
	// CapFilesystem means the plugin reads or writes the local filesystem.
	CapFilesystem Capability = "filesystem"
	// CapSecrets means the plugin handles credentials, or its instructions
	// reference credential locations.
	CapSecrets Capability = "secrets"
)

// Capabilities is the inferred access surface of a plugin.
//
// This is inference, not a declaration: nothing in the Agent Plugins format
// requires a plugin to state what it does. Evidence records why each capability
// was inferred so a human reviewing a lockfile diff can judge it rather than
// trust a boolean.
type Capabilities struct {
	Exec       bool       `json:"exec"`
	Network    bool       `json:"network"`
	Filesystem bool       `json:"filesystem"`
	Secrets    bool       `json:"secrets"`
	Evidence   []Evidence `json:"evidence,omitempty"`
}

// Evidence justifies one inferred capability.
type Evidence struct {
	Capability Capability `json:"capability"`
	// Component is the skill or MCP server name the evidence came from.
	Component string `json:"component,omitempty"`
	Reason    string `json:"reason"`
}

// Has reports whether a capability was inferred.
func (c Capabilities) Has(cap Capability) bool {
	switch cap {
	case CapExec:
		return c.Exec
	case CapNetwork:
		return c.Network
	case CapFilesystem:
		return c.Filesystem
	case CapSecrets:
		return c.Secrets
	}
	return false
}

// Set marks a capability as present and records the evidence for it.
func (c *Capabilities) Set(cap Capability, component, reason string) {
	switch cap {
	case CapExec:
		c.Exec = true
	case CapNetwork:
		c.Network = true
	case CapFilesystem:
		c.Filesystem = true
	case CapSecrets:
		c.Secrets = true
	default:
		return
	}
	c.Evidence = append(c.Evidence, Evidence{Capability: cap, Component: component, Reason: reason})
}

// List returns the inferred capabilities in a stable order.
func (c Capabilities) List() []Capability {
	var out []Capability
	for _, cap := range []Capability{CapExec, CapNetwork, CapFilesystem, CapSecrets} {
		if c.Has(cap) {
			out = append(out, cap)
		}
	}
	return out
}

// Skill returns the skill with the given name, if present.
func (p *Plugin) Skill(name string) (*Skill, bool) {
	for i := range p.Skills {
		if p.Skills[i].Name == name {
			return &p.Skills[i], true
		}
	}
	return nil, false
}

// MCPServer returns the server with the given name, if present.
func (p *Plugin) MCPServer(name string) (*MCPServer, bool) {
	for i := range p.MCPServers {
		if p.MCPServers[i].Name == name {
			return &p.MCPServers[i], true
		}
	}
	return nil, false
}
