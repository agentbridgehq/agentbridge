// Package mcpjson imports a bare MCP configuration fragment.
//
// This dialect is not a plugin format at all. It exists because the most common
// thing a developer actually has in hand is a snippet someone pasted in a
// README:
//
//	{ "mcpServers": { "db": { "command": "npx", "args": ["@acme/db-mcp"] } } }
//
// Treating that as an importable dialect means `agentbridge install ./snippet`
// works on the artifact people really have, rather than requiring them to
// hand-build a plugin directory first. The result is a synthetic single-purpose
// plugin whose name comes from the containing directory or file.
//
// Server entries here follow no schema — this is whatever a client happened to
// write — so transport is inferred from shape, as in the Claude Code dialect,
// and the inference is reported.
package mcpjson

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/agentbridge/agentbridge/internal/capability"
	"github.com/agentbridge/agentbridge/internal/diag"
	"github.com/agentbridge/agentbridge/internal/importer"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
	"github.com/agentbridge/agentbridge/internal/schema"
)

// CandidatePaths are the filenames this importer looks for, in order.
var CandidatePaths = []string{"mcp.json", ".mcp.json"}

// Importer reads a bare mcp.json fragment.
type Importer struct{}

// New returns an mcp.json importer.
func New() *Importer { return &Importer{} }

// Dialect implements importer.Importer.
func (*Importer) Dialect() ir.Dialect { return ir.DialectMCPJSON }

// Detect implements importer.Importer.
//
// This is the lowest-priority dialect: it claims a directory only when no
// richer format does, which the registry enforces by ordering rather than by
// anything checked here.
func (*Importer) Detect(root *safepath.Root) bool {
	for _, p := range CandidatePaths {
		if importer.Exists(root, p) {
			return true
		}
	}
	return false
}

// Import implements importer.Importer.
func (i *Importer) Import(root *safepath.Root) (*importer.Result, error) {
	var ds diag.Diagnostics

	rel := ""
	for _, p := range CandidatePaths {
		if importer.Exists(root, p) {
			rel = p
			break
		}
	}
	if rel == "" {
		return nil, fmt.Errorf("no MCP configuration found (looked for %s)", strings.Join(CandidatePaths, ", "))
	}

	raw, _, err := importer.ReadJSON(root, rel)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}

	var doc struct {
		Schema     string                     `json:"$schema"`
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if len(doc.MCPServers) == 0 {
		return nil, fmt.Errorf("%s: no mcpServers entries", rel)
	}

	name := synthesizeName(root)
	if err := schema.ValidateName(name); err != nil {
		// A directory name is not required to be a legal plugin name, and this
		// dialect has nowhere else to get one. Report rather than fail; the
		// user can override at install time.
		ds.Add(diag.Warning, diag.CodeManifestInvalidName, rel,
			"synthesized name %q from the directory is not valid under Agent Plugins rules: %v", name, err)
	}

	p := &ir.Plugin{
		IRVersion:   ir.Version,
		Name:        name,
		Description: fmt.Sprintf("MCP servers imported from %s", rel),
		Origin: ir.Origin{
			Dialect:      ir.DialectMCPJSON,
			SchemaID:     doc.Schema,
			Root:         root.Path(),
			ManifestPath: rel,
		},
	}
	ds.Add(diag.Info, diag.CodeComponentPreserved, rel,
		"synthesized a plugin named %q from a bare MCP configuration; it carries no skills and no manifest metadata", name)

	for _, srvName := range importer.SortedKeys(doc.MCPServers) {
		srv, ok := convert(root, srvName, rel, doc.MCPServers[srvName], &ds)
		if !ok {
			continue
		}
		if _, err := srv.ComputeContentHash(); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, rel, srvName,
				"server was skipped: %v", err)
			continue
		}
		p.MCPServers = append(p.MCPServers, *srv)
	}

	ds.Extend(capability.Infer(p, nil))

	return &importer.Result{Plugin: p, Diagnostics: ds, SkillBodies: map[string]string{}}, nil
}

func synthesizeName(root *safepath.Root) string {
	base := path.Base(root.Declared())
	base = strings.ToLower(base)
	// Map the characters a directory name may contain but a plugin name may
	// not onto hyphens, then collapse the runs the name rule forbids.
	var b strings.Builder
	for _, r := range base {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", ".")
	}
	out = strings.Trim(out, "-.")
	if out == "" {
		return "mcp-servers"
	}
	return out
}

func convert(root *safepath.Root, name, file string, raw json.RawMessage, ds *diag.Diagnostics) (*ir.MCPServer, bool) {
	var e struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		Cwd     string            `json:"cwd"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, file, name,
			"server was skipped: %v", err)
		return nil, false
	}

	var transport ir.Transport
	switch strings.ToLower(e.Type) {
	case "stdio":
		transport = ir.TransportStdio
	case "streamable-http", "http":
		transport = ir.TransportStreamableHTTP
	case "sse":
		transport = ir.TransportSSE
	case "":
		switch {
		case e.Command != "":
			transport = ir.TransportStdio
			ds.AddComponent(diag.Info, diag.CodeMCPTransportInferred, file, name,
				"no transport declared; inferred stdio from the presence of a command")
		case e.URL != "":
			transport = ir.TransportStreamableHTTP
			ds.AddComponent(diag.Info, diag.CodeMCPTransportInferred, file, name,
				"no transport declared; inferred streamable-http from the presence of a url")
		default:
			ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, file, name,
				"server was skipped: entry has neither a command nor a url")
			return nil, false
		}
	default:
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, file, name,
			"server was skipped: unknown transport %q", e.Type)
		return nil, false
	}

	srv := &ir.MCPServer{
		Name:      name,
		Transport: transport,
		Command:   e.Command,
		Args:      e.Args,
		Env:       e.Env,
		Cwd:       e.Cwd,
		URL:       e.URL,
		Headers:   e.Headers,
	}

	switch transport {
	case ir.TransportStdio:
		// A pasted fragment follows no dialect's rules, so a reserved name is
		// a portability problem to report rather than grounds to drop a server
		// the user is clearly already using.
		importer.StripReservedEnv(name, srv.Env, ds)
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
		if !importer.CheckHeaders(name, srv.Headers, ds) {
			return nil, false
		}
	}
	return srv, true
}
