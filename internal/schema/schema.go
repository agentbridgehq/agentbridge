// Package schema embeds and compiles the canonical Agent Plugins 1.0.0 JSON
// schemas.
//
// The schemas are embedded rather than fetched. The specification's conformance
// checklist is explicit: a client must "select locally supported manifest rules
// from $schema; do not retrieve a schema during loading." Embedding is also
// what lets the CLI work offline and in air-gapped environments, which the
// enterprise deployment story depends on.
//
// Two deliberate deviations from the canonical documents are made when
// compiling the *loader* variants, both documented at the point of change
// below: the manifest's closed-object constraint is relaxed so unknown
// top-level fields can be reported rather than fatal, and the name pattern is
// removed so it can be validated by code instead. The canonical documents
// themselves are kept unmodified and exposed for strict validation.
package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

//go:embed plugin.schema.json
var pluginSchemaJSON []byte

//go:embed mcp.schema.json
var mcpSchemaJSON []byte

// Canonical schema identifiers for Agent Plugins 1.0.0. A manifest must
// declare these exact values in its `$schema` field, and plugin.json and
// mcp.json must agree on the version.
const (
	PluginSchemaID = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
	MCPSchemaID    = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"
	SpecVersion    = "1.0.0"
)

// PluginManifestJSON returns the canonical, unmodified plugin manifest schema.
func PluginManifestJSON() []byte { return bytes.Clone(pluginSchemaJSON) }

// MCPConfigJSON returns the canonical, unmodified MCP configuration schema.
func MCPConfigJSON() []byte { return bytes.Clone(mcpSchemaJSON) }

// Identifiers for schemas we derive rather than ship. They are never fetched
// and never appear in a manifest; they exist because the compiler keys
// resources by URL and a derived document must not collide with, or be read as
// a fragment of, the canonical one it came from.
const derivedSchemaBase = "https://schemas.agentbridge.invalid/1.0.0/"

var loaderSchemaID = internalSchemaID("plugin.loader")

func internalSchemaID(name string) string { return derivedSchemaBase + name + ".schema.json" }

type compiled struct {
	pluginLoader *jsonschema.Schema
	pluginStrict *jsonschema.Schema
	mcpEnvelope  *jsonschema.Schema
	mcpServer    *jsonschema.Schema
	byTransport  map[string]*jsonschema.Schema
	err          error
}

var (
	once   sync.Once
	loaded compiled
)

func schemas() compiled {
	once.Do(func() { loaded = compileAll() })
	return loaded
}

func compileAll() compiled {
	var c compiled

	strictDoc, err := pluginVariant(pluginSchemaJSON, false)
	if err != nil {
		c.err = err
		return c
	}
	if c.pluginStrict, c.err = compileOne(PluginSchemaID, strictDoc); c.err != nil {
		return c
	}

	loaderDoc, err := pluginVariant(pluginSchemaJSON, true)
	if err != nil {
		c.err = err
		return c
	}
	if c.pluginLoader, c.err = compileOne(loaderSchemaID, loaderDoc); c.err != nil {
		return c
	}

	envelopeDoc, err := mcpEnvelopeVariant(mcpSchemaJSON)
	if err != nil {
		c.err = err
		return c
	}
	if c.mcpEnvelope, c.err = compileOne(internalSchemaID("mcp.envelope"), envelopeDoc); c.err != nil {
		return c
	}

	// Per-server schemas. The specification requires that one malformed server
	// entry not reject the whole plugin, so servers are validated
	// individually; validating only the whole document would make isolation
	// impossible.
	c.byTransport = map[string]*jsonschema.Schema{}
	for _, def := range []struct {
		transport string
		ref       string
	}{
		{"", "server"},
		{"stdio", "stdioServer"},
		{"streamable-http", "streamableHttpServer"},
		{"sse", "sseServer"},
	} {
		sub, err := subschemaDoc(mcpSchemaJSON, def.ref)
		if err != nil {
			c.err = err
			return c
		}
		s, err := compileOne(internalSchemaID("mcp."+def.ref), sub)
		if err != nil {
			c.err = err
			return c
		}
		if def.transport == "" {
			c.mcpServer = s
			continue
		}
		c.byTransport[def.transport] = s
	}

	return c
}

func decode(b []byte) (any, error) {
	return jsonschema.UnmarshalJSON(bytes.NewReader(b))
}

func compileOne(id string, doc any) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		return nil, fmt.Errorf("adding %s: %w", id, err)
	}
	s, err := c.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("compiling %s: %w", id, err)
	}
	return s, nil
}

// pluginVariant produces a compilable variant of the manifest schema.
//
// Two changes are made to the canonical document, for different reasons.
//
// The `name` pattern is removed from **both** variants, because it is not
// compilable here at all:
//
//	^(?!.*(?:--|\.\.))[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$
//
// The leading negative lookahead is ECMA-262 regex, which JSON Schema
// specifies, but Go's regexp package implements RE2 and has no lookahead by
// design. Leaving it in fails at schema *compile* time, not validation time,
// which would take the whole loader down. Pulling in a second regex engine for
// one field is not worth it, so the rule is enforced by ValidateName, which
// also reports which part of it was broken instead of "does not match
// pattern". This is a genuine deviation from the canonical document and the
// reason it is safe is that the constraint is still enforced, just elsewhere.
//
// additionalProperties is opened up only for the loader variant. The canonical
// schema closes the object, but the conformance rules require a client to
// "report but continue when encountering unknown top-level fields." Schema
// validation alone cannot satisfy both, so the loader tolerates unknown keys
// and the importer reports them, while author-facing strict validation keeps
// the closed-object constraint.
func pluginVariant(raw []byte, openAdditionalProperties bool) (any, error) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("preparing plugin schema: %w", err)
	}

	if openAdditionalProperties {
		doc["additionalProperties"] = true
		// Give the variant its own identity. Reusing the canonical $id with a
		// fragment would be read as an anchor reference into the canonical
		// document rather than as a separate schema.
		doc["$id"] = loaderSchemaID
	}

	props, ok := doc["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("preparing plugin schema: no properties object")
	}
	name, ok := props["name"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("preparing plugin schema: no name property")
	}
	if _, had := name["pattern"]; !had {
		// The upstream schema changed shape. Fail loudly: silently losing the
		// name constraint is exactly the kind of drift that should not pass
		// unnoticed.
		return nil, fmt.Errorf("preparing plugin schema: name has no pattern; " +
			"the embedded schema changed and ValidateName may no longer match it")
	}
	delete(name, "pattern")

	return doc, nil
}

// mcpEnvelopeVariant produces a schema that checks only the outer shape of an
// mcp.json: that `$schema` names this specification version and that
// `mcpServers` is an object of objects.
//
// The canonical document validates the whole file, servers included, which
// would make a single malformed entry reject every server in the file. The
// conformance rules require the opposite — "validate server entries
// independently", "continue loading when individual MCP servers fail" — so the
// envelope and the entries are two separate boundaries here, and the per-server
// schemas in $defs handle the inner one.
func mcpEnvelopeVariant(raw []byte) (any, error) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("preparing mcp envelope schema: %w", err)
	}

	props, ok := doc["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("preparing mcp envelope schema: no properties object")
	}
	servers, ok := props["mcpServers"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("preparing mcp envelope schema: no mcpServers property")
	}
	// Each entry must be an object; what kind of object is the per-server
	// schemas' business.
	servers["additionalProperties"] = map[string]any{"type": "object"}

	doc["$id"] = internalSchemaID("mcp.envelope")
	delete(doc, "$defs")

	return doc, nil
}

// subschemaDoc extracts one entry from the MCP schema's $defs into a
// standalone, compilable document that keeps the sibling $defs available for
// internal $ref resolution.
func subschemaDoc(raw []byte, def string) (any, error) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("extracting %s: %w", def, err)
	}
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("extracting %s: mcp schema has no $defs", def)
	}
	if _, ok := defs[def]; !ok {
		return nil, fmt.Errorf("extracting %s: not found in $defs", def)
	}
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     internalSchemaID("mcp." + def),
		"$ref":    "#/$defs/" + def,
		"$defs":   defs,
	}, nil
}

// ValidatePluginManifest validates a decoded plugin.json against the loader
// variant of the manifest schema.
func ValidatePluginManifest(instance any) error {
	s := schemas()
	if s.err != nil {
		return s.err
	}
	return format(s.pluginLoader.Validate(instance))
}

// ValidatePluginManifestStrict validates against the canonical closed schema,
// including the additionalProperties constraint. Used by author-facing
// validation, not by the loader.
func ValidatePluginManifestStrict(instance any) error {
	s := schemas()
	if s.err != nil {
		return s.err
	}
	return format(s.pluginStrict.Validate(instance))
}

// ValidateMCPEnvelope validates the top level of an mcp.json: its `$schema`
// and the presence of `mcpServers`. Individual servers are validated
// separately so one bad entry does not reject the rest.
func ValidateMCPEnvelope(instance any) error {
	s := schemas()
	if s.err != nil {
		return s.err
	}
	return format(s.mcpEnvelope.Validate(instance))
}

// ValidateMCPServer validates a single server entry.
//
// When the entry declares a known transport, it is validated against that
// transport's schema so the error names the actual problem. Otherwise it falls
// back to the oneOf, whose failure message is necessarily vaguer.
func ValidateMCPServer(instance any) error {
	s := schemas()
	if s.err != nil {
		return s.err
	}
	if obj, ok := instance.(map[string]any); ok {
		if t, ok := obj["type"].(string); ok {
			if sub, ok := s.byTransport[t]; ok {
				return format(sub.Validate(instance))
			}
		}
	}
	return format(s.mcpServer.Validate(instance))
}

// enPrinter renders the library's localized error strings. Output is
// user-facing CLI text, and the project has no localization story yet.
var enPrinter = message.NewPrinter(language.English)

// format flattens a jsonschema validation error into a single readable line.
// The library's default rendering is a multi-line tree, which reads badly in a
// CLI diagnostic and worse in a JSON field.
func format(err error) error {
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err
	}
	causes := leafCauses(ve, nil)
	if len(causes) == 0 {
		return errors.New(strings.TrimSpace(ve.Error()))
	}
	// Deep oneOf failures can produce the same leaf message for several
	// branches; repeating it adds noise without adding information.
	return errors.New(strings.Join(dedupe(causes), "; "))
}

func leafCauses(ve *jsonschema.ValidationError, acc []string) []string {
	if len(ve.Causes) == 0 {
		path := "/" + strings.Join(ve.InstanceLocation, "/")
		return append(acc, fmt.Sprintf("%s: %s", path, ve.ErrorKind.LocalizedString(enPrinter)))
	}
	for _, c := range ve.Causes {
		acc = leafCauses(c, acc)
	}
	return acc
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
