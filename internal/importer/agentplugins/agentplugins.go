// Package agentplugins imports plugins in the Agent Plugins 1.0.0 format.
//
// This is the reference importer: it follows the specification's conformance
// rules literally, including the parts that are easy to get subtly wrong.
//
//   - Unknown top-level manifest fields are reported and ignored, not fatal.
//     The canonical schema closes the object, but the conformance checklist
//     requires tolerance; see internal/schema for how both are honored.
//   - Extension namespaces are preserved without being validated. The
//     specification is explicit that a client "MUST ignore manifest entries for
//     namespaces it does not implement without validating the contents of their
//     values" — so foreign namespaces are carried as opaque bytes.
//   - One malformed MCP server entry is skipped, not fatal. Servers are
//     therefore validated individually rather than as part of the whole
//     document.
//   - A missing component location is acceptable. A plugin with neither skills
//     nor MCP servers is valid, if useless.
package agentplugins

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/agentbridge/agentbridge/internal/capability"
	"github.com/agentbridge/agentbridge/internal/diag"
	"github.com/agentbridge/agentbridge/internal/importer"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
	"github.com/agentbridge/agentbridge/internal/schema"
)

// Fixed component locations. The specification does not allow these to be
// configured, which is what makes discovery portable.
const (
	ManifestPath = "plugin.json"
	MCPPath      = "mcp.json"
	SkillsDir    = "skills"
)

// knownManifestFields is the closed set from the specification. Anything else
// is reported as unknown and ignored.
var knownManifestFields = map[string]bool{
	"$schema": true, "name": true, "version": true, "description": true,
	"author": true, "homepage": true, "repository": true, "license": true,
	"keywords": true, "extensions": true,
}

// Importer reads the Agent Plugins format.
type Importer struct{}

// New returns an Agent Plugins importer.
func New() *Importer { return &Importer{} }

// Dialect implements importer.Importer.
func (*Importer) Dialect() ir.Dialect { return ir.DialectAgentPlugins }

// Detect implements importer.Importer.
func (*Importer) Detect(root *safepath.Root) bool {
	return importer.Exists(root, ManifestPath)
}

// Import implements importer.Importer.
func (i *Importer) Import(root *safepath.Root) (*importer.Result, error) {
	var ds diag.Diagnostics

	raw, decoded, err := importer.ReadJSON(root, ManifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: %w", ManifestPath, os.ErrNotExist)
		}
		return nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}

	obj, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: top level must be a JSON object", ManifestPath)
	}

	// Spec 5.2: a client selects its validation rules from `$schema`, and if it
	// does not support the declared version it "MUST reject the plugin and
	// SHOULD report the unsupported version". Checking first turns what would
	// otherwise be an opaque const-mismatch into a message that names the
	// version we do support.
	if declared, ok := obj["$schema"].(string); ok && declared != schema.PluginSchemaID {
		return nil, fmt.Errorf("%s: unsupported Agent Plugins version: $schema is %q, this build supports %s (%s)",
			ManifestPath, declared, schema.SpecVersion, schema.PluginSchemaID)
	}

	// Schema violations are otherwise fatal: spec 5.2 and 11.3 require
	// rejecting a plugin whose required fields are missing or malformed, or
	// which violates a type constraint, with exactly two exceptions handled
	// below (unknown top-level fields, and a non-object `extensions`).
	if err := schema.ValidatePluginManifest(decoded); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}

	// Spec 8.1: "If `extensions` is not an object, the client MUST report and
	// ignore the field and continue loading components." It is one of only two
	// non-fatal schema violations, so it is detected here rather than left to
	// the schema, which would make it fatal.
	extensionsUsable := true
	if v, present := obj["extensions"]; present {
		if _, isObject := v.(map[string]any); !isObject {
			extensionsUsable = false
			ds.Add(diag.Warning, diag.CodeManifestBadExtensions, ManifestPath,
				"extensions is not an object and was ignored")
		}
	}

	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}
	if !extensionsUsable {
		m.Extensions = nil
	}

	// The name pattern is enforced here rather than by the schema; see
	// schema.ValidateName for why.
	if err := schema.ValidateName(m.Name); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}

	for _, k := range importer.SortedKeys(obj) {
		if !knownManifestFields[k] {
			ds.Add(diag.Warning, diag.CodeManifestUnknownField, ManifestPath,
				"unknown top-level field %q was ignored", k)
		}
	}

	p := &ir.Plugin{
		IRVersion:   ir.Version,
		Name:        m.Name,
		Version:     m.Version,
		Description: m.Description,
		Homepage:    m.Homepage,
		Repository:  m.Repository,
		License:     m.License,
		Keywords:    m.Keywords,
		Extensions:  m.Extensions,
		Origin: ir.Origin{
			Dialect:      ir.DialectAgentPlugins,
			SchemaID:     m.Schema,
			Root:         root.Path(),
			ManifestPath: ManifestPath,
		},
	}
	if m.Author != nil {
		p.Author = &ir.Author{Name: m.Author.Name, Email: m.Author.Email, URL: m.Author.URL}
	}
	for _, ns := range importer.SortedKeys(m.Extensions) {
		ds.Add(diag.Info, diag.CodeExtensionPreserved, ManifestPath,
			"extension namespace %q preserved without interpretation", ns)
	}

	skills, bodies, sds := importer.DiscoverDirSkills(root, SkillsDir)
	ds.Extend(sds)
	p.Skills = importer.DedupeSkills(skills, &ds)

	servers, mds := loadMCP(root, m.Schema)
	ds.Extend(mds)
	p.MCPServers = servers

	ds.Extend(capability.Infer(p, bodies))

	return &importer.Result{Plugin: p, Diagnostics: ds, SkillBodies: bodies}, nil
}

type manifest struct {
	Schema      string          `json:"$schema"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Author      *manifestAuthor `json:"author"`
	Homepage    string          `json:"homepage"`
	Repository  string          `json:"repository"`
	License     string          `json:"license"`
	Keywords    []string        `json:"keywords"`
	Extensions  extensionMap    `json:"extensions"`
}

// extensionMap decodes the `extensions` field tolerantly.
//
// Spec 8.1 makes a non-object `extensions` non-fatal, so decoding must not fail
// on one. Strict decoding here would turn a violation the specification
// explicitly says to report-and-ignore into a rejected plugin. The importer
// detects and reports the bad shape separately; this type's only job is to not
// blow up first.
type extensionMap map[string]json.RawMessage

func (e *extensionMap) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		*e = nil
		return nil
	}
	*e = m
	return nil
}

type manifestAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	URL   string `json:"url"`
}

// loadMCP reads mcp.json.
//
// The whole file is the isolation boundary for envelope problems — a bad
// `$schema` or a missing `mcpServers` means MCP configuration is rejected while
// the plugin's skills still load. Within a valid envelope, each server is its
// own boundary.
func loadMCP(root *safepath.Root, manifestSchema string) ([]ir.MCPServer, diag.Diagnostics) {
	var ds diag.Diagnostics

	if !importer.Exists(root, MCPPath) {
		// Spec 6.2: a missing fixed location is not an error.
		return nil, ds
	}
	if !importer.IsRegularFile(root, MCPPath) {
		// Spec 6.2: present but the wrong filesystem kind makes this component
		// type invalid while others continue loading.
		ds.Add(diag.Error, diag.CodeMCPNotRegularFile, MCPPath,
			"MCP configuration was not loaded: %s exists but is not a regular file", MCPPath)
		return nil, ds
	}

	raw, decoded, err := importer.ReadJSON(root, MCPPath)
	if err != nil {
		ds.Add(diag.Error, diag.CodeMCPInvalidJSON, MCPPath,
			"MCP configuration was not loaded: %v", err)
		return nil, ds
	}
	_ = raw

	if obj, ok := decoded.(map[string]any); ok {
		if declared, ok := obj["$schema"].(string); ok && declared != schema.MCPSchemaID {
			// Spec 7.2.2 rule 2: an unsupported or mismatched version disables
			// MCP for the plugin and continues with other component types.
			ds.Add(diag.Error, diag.CodeUnsupportedSpecVer, MCPPath,
				"MCP configuration was not loaded: $schema is %q, this build supports %s", declared, schema.MCPSchemaID)
			return nil, ds
		}
	}

	if err := schema.ValidateMCPEnvelope(decoded); err != nil {
		ds.Add(diag.Error, diag.CodeMCPSchemaFailed, MCPPath,
			"MCP configuration was not loaded: %v", err)
		return nil, ds
	}

	var doc mcpDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		ds.Add(diag.Error, diag.CodeMCPInvalidJSON, MCPPath,
			"MCP configuration was not loaded: %v", err)
		return nil, ds
	}

	// Both manifests must declare the same Agent Plugins version. The
	// envelope's `$schema` const already pins mcp.json to 1.0.0, so this only
	// fires when plugin.json declares something else — but the check is stated
	// explicitly in the conformance rules and the diagnostic is far clearer
	// than a schema failure would be.
	if manifestSchema != "" && doc.Schema != "" &&
		manifestSchema == schema.PluginSchemaID && doc.Schema != schema.MCPSchemaID {
		ds.Add(diag.Error, diag.CodeVersionMismatch, MCPPath,
			"mcp.json declares %q but plugin.json targets Agent Plugins %s; both manifests must declare the same version",
			doc.Schema, schema.SpecVersion)
		return nil, ds
	}

	var servers []ir.MCPServer
	for _, name := range importer.SortedKeys(doc.MCPServers) {
		entry := doc.MCPServers[name]

		var generic any
		if err := json.Unmarshal(entry, &generic); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, MCPPath, name,
				"server was skipped: %v", err)
			continue
		}
		if err := schema.ValidateMCPServer(generic); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, MCPPath, name,
				"server was skipped: %v", err)
			continue
		}

		var s serverEntry
		if err := json.Unmarshal(entry, &s); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, MCPPath, name,
				"server was skipped: %v", err)
			continue
		}

		srv := ir.MCPServer{
			Name:      name,
			Transport: ir.Transport(s.Type),
			Command:   s.Command,
			Args:      s.Args,
			Env:       s.Env,
			Cwd:       s.Cwd,
			URL:       s.URL,
			Headers:   s.Headers,
		}

		if !validateServer(root, &srv, &ds) {
			continue
		}
		if _, err := srv.ComputeContentHash(); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, MCPPath, name,
				"server was skipped: %v", err)
			continue
		}
		servers = append(servers, srv)
	}

	return servers, ds
}

func validateServer(root *safepath.Root, srv *ir.MCPServer, ds *diag.Diagnostics) bool {
	switch srv.Transport {
	case ir.TransportStdio:
		// Spec 9.2: a reserved env name makes the server entry invalid, not
		// merely questionable.
		if !importer.CheckReservedEnv(srv.Name, srv.Env, ds) {
			return false
		}
		if !importer.CheckStdioCommand(root, srv.Name, srv.Command, ds) {
			return false
		}
		return importer.CheckCwd(root, srv.Name, srv.Cwd, ds)
	case ir.TransportStreamableHTTP, ir.TransportSSE:
		if !importer.CheckServerURL(srv.Name, srv.URL, ds) {
			return false
		}
		return importer.CheckHeaders(srv.Name, srv.Headers, ds)
	default:
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, MCPPath, srv.Name,
			"server was skipped: unknown transport %q", srv.Transport)
		return false
	}
}

type mcpDoc struct {
	Schema     string                     `json:"$schema"`
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

type serverEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Cwd     string            `json:"cwd"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}
