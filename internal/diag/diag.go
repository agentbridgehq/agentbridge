// Package diag carries structured diagnostics produced while importing and
// exporting plugins.
//
// The Agent Plugins specification requires that a single invalid component not
// reject an entire plugin, and that unknown manifest fields and unimplemented
// extension namespaces be tolerated. That means "partially succeeded, and here
// is precisely what was lost" is the normal outcome of a load, not an
// exceptional one. Diagnostics are how that outcome is represented.
//
// Every diagnostic carries a stable Code. Codes are part of the public
// contract: fidelity reports, adapter loss lists, and eventually machine
// consumers key off them, so treat renaming one as a breaking change.
package diag

import (
	"fmt"
	"sort"
	"strings"
)

// Severity classifies a diagnostic.
type Severity string

const (
	// Error means the affected item was rejected. Whether that item is the
	// whole plugin or a single component depends on where it was raised.
	Error Severity = "error"
	// Warning means the item loaded, but something was lost, ignored, or
	// rewritten. Every silent-degradation case in this codebase must produce
	// at least a Warning.
	Warning Severity = "warning"
	// Info records a translation that is expected and lossless but worth
	// surfacing, such as a placeholder rewrite.
	Info Severity = "info"
)

// Stable diagnostic codes.
//
// Naming: <area>.<subject>_<condition>. Do not renumber or reuse.
const (
	// Manifest and schema.
	CodeManifestMissing       = "manifest.missing"
	CodeManifestUnreadable    = "manifest.unreadable"
	CodeManifestInvalidJSON   = "manifest.invalid_json"
	CodeManifestSchemaFailed  = "manifest.schema_failed"
	CodeManifestUnknownField  = "manifest.unknown_field"
	CodeManifestInvalidName   = "manifest.invalid_name"
	CodeManifestSchemaMissing = "manifest.schema_field_missing"
	CodeVersionMismatch       = "manifest.version_mismatch"

	// Skills.
	CodeSkillMissingFile     = "skill.missing_skill_md"
	CodeSkillUnreadable      = "skill.unreadable"
	CodeSkillInvalidFrontmat = "skill.invalid_frontmatter"
	CodeSkillNoName          = "skill.no_name"
	CodeSkillDuplicate       = "skill.duplicate_name"
	CodeSkillFlatCommand     = "skill.flat_command_file"

	// MCP servers.
	CodeMCPInvalidJSON       = "mcp.invalid_json"
	CodeMCPSchemaFailed      = "mcp.schema_failed"
	CodeMCPServerInvalid     = "mcp.server_invalid"
	CodeMCPServerDuplicate   = "mcp.server_duplicate"
	CodeMCPTransportInferred = "mcp.transport_inferred"
	CodeMCPInsecureURL       = "mcp.insecure_url"
	CodeMCPReservedEnv       = "mcp.reserved_env_name"
	CodeMCPPlaceholderRewrit = "mcp.placeholder_rewritten"
	CodeMCPCommandRewritten  = "mcp.command_rewritten"
	CodeMCPCwdUncontained    = "mcp.cwd_not_contained"

	// Paths and containment.
	CodePathEscape     = "path.escapes_plugin_root"
	CodePathAbsolute   = "path.absolute_not_allowed"
	CodePathSymlink    = "path.symlink_escape"
	CodePathUnreadable = "path.unreadable"

	// Cross-dialect translation.
	CodeExtensionPreserved  = "translate.extension_preserved"
	CodeComponentUnsupport  = "translate.component_unsupported"
	CodeComponentPreserved  = "translate.component_preserved_native"
	CodeCapabilityInferred  = "capability.inferred"
	CodeSecretLiteralInEnv  = "capability.secret_literal_in_env"
	CodeSkillReadsCredsHint = "capability.skill_references_credentials"
)

// Diagnostic is a single finding about a plugin or one of its components.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	// Path locates the finding: a plugin-relative file path, optionally with a
	// component reference such as "mcp.json#/mcpServers/db". Empty when the
	// finding is about the plugin as a whole.
	Path string `json:"path,omitempty"`
	// Component names the skill or MCP server the finding concerns, when
	// applicable. Used to group fidelity output by component.
	Component string `json:"component,omitempty"`
}

func (d Diagnostic) String() string {
	var b strings.Builder
	b.WriteString(string(d.Severity))
	b.WriteString(" [")
	b.WriteString(d.Code)
	b.WriteString("] ")
	b.WriteString(d.Message)
	if d.Path != "" {
		fmt.Fprintf(&b, " (%s)", d.Path)
	}
	return b.String()
}

// Diagnostics is an ordered collection of findings.
type Diagnostics []Diagnostic

// Add appends a diagnostic.
func (ds *Diagnostics) Add(sev Severity, code, path, format string, args ...any) {
	*ds = append(*ds, Diagnostic{
		Severity: sev,
		Code:     code,
		Message:  fmt.Sprintf(format, args...),
		Path:     path,
	})
}

// AddComponent appends a diagnostic scoped to a named component.
func (ds *Diagnostics) AddComponent(sev Severity, code, path, component, format string, args ...any) {
	*ds = append(*ds, Diagnostic{
		Severity:  sev,
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Path:      path,
		Component: component,
	})
}

// Extend appends all diagnostics from another set.
func (ds *Diagnostics) Extend(other Diagnostics) {
	*ds = append(*ds, other...)
}

// HasErrors reports whether any diagnostic is an Error.
func (ds Diagnostics) HasErrors() bool {
	for _, d := range ds {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Filter returns the diagnostics matching the given severity.
func (ds Diagnostics) Filter(sev Severity) Diagnostics {
	var out Diagnostics
	for _, d := range ds {
		if d.Severity == sev {
			out = append(out, d)
		}
	}
	return out
}

// Codes returns the sorted, deduplicated set of codes present.
func (ds Diagnostics) Codes() []string {
	seen := map[string]bool{}
	for _, d := range ds {
		seen[d.Code] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
