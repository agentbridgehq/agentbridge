// Package registry selects the right importer for a directory.
//
// It lives apart from package importer so that the shared helpers stay free of
// any dependency on the concrete dialects, which would otherwise be an import
// cycle.
//
// Order is significant. Detection is deliberately ordered from most specific to
// least: Agent Plugins first because a root plugin.json is unambiguous, then
// Claude Code, then the bare mcp.json fallback, which would otherwise claim any
// directory that happens to contain an MCP config.
package registry

import (
	"fmt"

	"github.com/agentbridgehq/agentbridge/internal/importer"
	"github.com/agentbridgehq/agentbridge/internal/importer/agentplugins"
	"github.com/agentbridgehq/agentbridge/internal/importer/claudecode"
	"github.com/agentbridgehq/agentbridge/internal/importer/mcpjson"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// All returns every importer in detection order.
func All() []importer.Importer {
	return []importer.Importer{
		agentplugins.New(),
		claudecode.New(),
		mcpjson.New(),
	}
}

// ByDialect returns the importer for an explicitly named dialect.
func ByDialect(d ir.Dialect) (importer.Importer, bool) {
	for _, imp := range All() {
		if imp.Dialect() == d {
			return imp, true
		}
	}
	return nil, false
}

// Detect returns the first importer that claims the directory.
func Detect(root *safepath.Root) (importer.Importer, bool) {
	for _, imp := range All() {
		if imp.Detect(root) {
			return imp, true
		}
	}
	return nil, false
}

// Open loads a plugin from a directory, detecting its dialect.
func Open(dir string) (*importer.Result, error) {
	root, err := safepath.NewRoot(dir)
	if err != nil {
		return nil, err
	}
	imp, ok := Detect(root)
	if !ok {
		return nil, fmt.Errorf("%s: %w", dir, importer.ErrNotRecognized)
	}
	return imp.Import(root)
}

// OpenAs loads a plugin from a directory using an explicitly named dialect,
// bypassing detection.
func OpenAs(dir string, d ir.Dialect) (*importer.Result, error) {
	root, err := safepath.NewRoot(dir)
	if err != nil {
		return nil, err
	}
	imp, ok := ByDialect(d)
	if !ok {
		return nil, fmt.Errorf("unknown dialect %q", d)
	}
	return imp.Import(root)
}
