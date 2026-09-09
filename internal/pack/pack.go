// Package pack writes the vendor manifests a conformant package needs in order
// to install into clients that do not read the specification's own plugin.json.
//
// Every other command in this tool acts on a plugin someone is installing.
// This one acts on a plugin someone is writing, and the difference matters:
// pack's output is committed to the author's repository, so it has to be
// idempotent, reviewable in a diff, and correct without agentbridge present on
// the machine that later installs it. A user who has never heard of this
// project should be able to `git clone` a packed repository and have their
// client load it.
//
// The reason the command exists is the finding in conformance/README.md: three
// vendors have each introduced a private manifest at a private path, and an
// unmodified Agent Plugins package installs in only one of the clients we
// measured. Adding a handful of small files fixes that, and the shapes of
// those files are exactly what this project has spent its time measuring.
//
// What pack does not do is invent. Where a client's behaviour has not been
// measured, the gap is reported rather than guessed at, because a manifest
// that is wrong in a way nobody notices is worse than one that is absent.
package pack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/adapter/clients/claudecode"
	"github.com/agentbridgehq/agentbridge/internal/adapter/clients/cursor"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// Client identifiers pack can write for. These are the three clients that
// require a manifest of their own; the others either read the specification's
// package directly or take their configuration somewhere outside it.
const (
	ClaudeCode = "claude-code"
	Cursor     = "cursor"
	Codex      = "codex"
)

// Clients lists every client pack can write for, in a stable order.
var Clients = []string{ClaudeCode, Codex, Cursor}

// File is one file pack writes into the package.
type File struct {
	Client string `json:"client"`
	// Path is package-relative and slash-separated, so it reads the same in
	// output on every platform and can be pasted into a .gitignore or a
	// review comment.
	Path    string `json:"path"`
	Content []byte `json:"-"`
	// Why records what this file buys, for the human reading the output.
	Why string `json:"why"`
}

// State is what applying a File would do.
type State string

const (
	// Created means no such file exists yet.
	Created State = "created"
	// Updated means a file exists with different content.
	Updated State = "updated"
	// Unchanged means the file on disk is already byte-identical.
	Unchanged State = "unchanged"
)

// Change pairs a File with what writing it would do.
type Change struct {
	File
	State State `json:"state"`
}

// Gap is something a client needs that pack could not supply, and why. A gap
// is not an error: the package is still improved by everything else pack
// wrote. It is recorded so the author knows what remains unproven rather than
// assuming silence means success.
type Gap struct {
	Client string `json:"client"`
	Detail string `json:"detail"`
}

// Result is the whole outcome of a pack run.
type Result struct {
	Plugin  string   `json:"plugin"`
	Changes []Change `json:"changes"`
	Gaps    []Gap    `json:"gaps,omitempty"`
}

// Written reports whether applying this result would change anything on disk.
// `--check` turns this into an exit code.
func (r Result) Written() bool {
	for _, c := range r.Changes {
		if c.State != Unchanged {
			return true
		}
	}
	return false
}

// Build renders every manifest the named clients need for this plugin.
//
// Passing no clients means all of them. An unknown client is an error rather
// than a silent omission: a typo in --client should not look like a client
// that needs nothing.
func Build(p *ir.Plugin, clients []string) ([]File, []Gap, error) {
	if p == nil {
		return nil, nil, errors.New("no plugin")
	}
	if p.Name == "" {
		return nil, nil, errors.New("plugin has no name; a vendor manifest cannot be written without one")
	}

	selected, err := selectClients(clients)
	if err != nil {
		return nil, nil, err
	}

	var files []File
	var gaps []Gap

	for _, id := range selected {
		switch id {
		case ClaudeCode:
			f, g, err := claudeCodeFiles(p)
			if err != nil {
				return nil, nil, err
			}
			files = append(files, f...)
			gaps = append(gaps, g...)

		case Cursor:
			f, g, err := cursorFiles(p)
			if err != nil {
				return nil, nil, err
			}
			files = append(files, f...)
			gaps = append(gaps, g...)

		case Codex:
			f, g, err := codexFiles(p)
			if err != nil {
				return nil, nil, err
			}
			files = append(files, f...)
			gaps = append(gaps, g...)
		}
	}
	return files, gaps, nil
}

// selectClients resolves the --client selection to a stable, deduplicated list.
func selectClients(clients []string) ([]string, error) {
	if len(clients) == 0 {
		return Clients, nil
	}
	known := map[string]bool{}
	for _, c := range Clients {
		known[c] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range clients {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !known[c] {
			return nil, fmt.Errorf("pack does not write a manifest for %q; it writes for %s", c, strings.Join(Clients, ", "))
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	// Emit in the canonical order regardless of the order asked for, so the
	// output of two equivalent invocations is identical.
	var ordered []string
	for _, c := range Clients {
		if seen[c] {
			ordered = append(ordered, c)
		}
	}
	return ordered, nil
}

// claudeCodeFiles writes .claude-plugin/plugin.json and, when the plugin has
// servers, a translated .mcp.json beside it.
//
// The manifest's mcpServers field accepts a path, so it is tempting to point
// it at the package's own mcp.json and write one file instead of two. That
// does not work, and it fails quietly: Claude Code expands
// ${CLAUDE_PLUGIN_ROOT}, the specification says ${PLUGIN_ROOT}, and an
// unrecognized placeholder is passed through as literal text rather than
// rejected. The server starts with a command containing a dollar sign and a
// brace, and the only symptom is a plugin that does not work.
func claudeCodeFiles(p *ir.Plugin) ([]File, []Gap, error) {
	manifest, err := claudecode.BuildManifest(p)
	if err != nil {
		return nil, nil, fmt.Errorf("claude-code manifest: %w", err)
	}
	files := []File{{
		Client:  ClaudeCode,
		Path:    ".claude-plugin/plugin.json",
		Content: manifest,
		Why:     "Claude Code looks for its own manifest here and ignores the package without it",
	}}

	var gaps []Gap
	if len(p.MCPServers) > 0 {
		mcp, carried, err := claudecode.BuildPackageMCP(p.MCPServers)
		if err != nil {
			return nil, nil, fmt.Errorf("claude-code mcp: %w", err)
		}
		files = append(files, File{
			Client:  ClaudeCode,
			Path:    ".mcp.json",
			Content: mcp,
			Why:     "the same servers in Claude Code's dialect, because it expands ${CLAUDE_PLUGIN_ROOT} and not ${PLUGIN_ROOT}",
		})
		if carried < len(p.MCPServers) {
			gaps = append(gaps, Gap{
				Client: ClaudeCode,
				Detail: fmt.Sprintf("%d of %d servers could not be translated; run `agentbridge losses` for which and why", len(p.MCPServers)-carried, len(p.MCPServers)),
			})
		}
	}
	return files, gaps, nil
}

// cursorFiles writes .cursor-plugin/plugin.json.
//
// Cursor is the one client that already accepts an unmodified conformant
// package, so this manifest is not what makes the plugin work — it is what
// makes the components explicit, which is what the plugins Cursor ships itself
// do.
func cursorFiles(p *ir.Plugin) ([]File, []Gap, error) {
	manifest, err := cursor.BuildManifest(p, true)
	if err != nil {
		return nil, nil, fmt.Errorf("cursor manifest: %w", err)
	}
	files := []File{{
		Client:  Cursor,
		Path:    ".cursor-plugin/plugin.json",
		Content: manifest,
		Why:     "names the package's skills and servers explicitly rather than by convention",
	}}

	var gaps []Gap
	if usesPlaceholders(p.MCPServers) {
		// Stated rather than assumed. Our own measurement of ${PLUGIN_ROOT}
		// under Cursor went through agentbridge, which substitutes absolute
		// paths before Cursor ever sees the value, so it says nothing about
		// what Cursor does with a placeholder in a package's own mcp.json.
		gaps = append(gaps, Gap{
			Client: Cursor,
			Detail: "this manifest points Cursor at ./mcp.json, but whether Cursor expands ${PLUGIN_ROOT} there is unmeasured; test before relying on it",
		})
	}
	return files, gaps, nil
}

// codexFiles writes .codex-plugin/plugin.json.
//
// This one is for users on an older Codex, and that is worth being exact
// about. Up to and including 0.151.0, `codex plugin add` rejected a package
// that was otherwise entirely conformant with `missing plugin.json`, and this
// file was the whole fix. As of 0.153.4 it is not: an unmodified conformant
// package installs, and its skills reach the model. Both were measured; see
// conformance/results/codex.yaml.
//
// It is still written by default because an author cannot control which Codex
// their users are on, and the file costs four lines and breaks nothing on a
// version that ignores it. An author who only supports current Codex can drop
// it with --client.
//
// Nothing is translated here. Codex reads the package's own mcp.json from
// 0.147.0 onward and expands the specification's placeholders in it, so unlike
// Claude Code there is no second dialect to emit.
func codexFiles(p *ir.Plugin) ([]File, []Gap, error) {
	m := map[string]any{"name": p.Name}
	if len(p.Skills) > 0 {
		m["skills"] = "./skills/"
	}
	setIfNotEmpty(m, "version", p.Version)
	setIfNotEmpty(m, "description", p.Description)
	setIfNotEmpty(m, "homepage", p.Homepage)
	setIfNotEmpty(m, "repository", p.Repository)
	setIfNotEmpty(m, "license", p.License)
	if len(p.Keywords) > 0 {
		m["keywords"] = p.Keywords
	}
	if p.Author != nil && p.Author.Name != "" {
		m["author"] = map[string]any{"name": p.Author.Name}
	}

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("codex manifest: %w", err)
	}

	files := []File{{
		Client:  Codex,
		Path:    ".codex-plugin/plugin.json",
		Content: append(raw, '\n'),
		Why:     "required by Codex up to 0.151; ignored by 0.153+, which takes the package as it is",
	}}
	return files, nil, nil
}

// usesPlaceholders reports whether any server depends on placeholder expansion.
// A package whose servers are all remote URLs does not care who expands what.
func usesPlaceholders(servers []ir.MCPServer) bool {
	has := func(v string) bool {
		return strings.Contains(v, ir.PlaceholderPluginRoot) ||
			strings.Contains(v, ir.PlaceholderPluginData)
	}
	for _, s := range servers {
		if has(s.Command) || has(s.Cwd) {
			return true
		}
		if strings.HasPrefix(s.Command, "./") {
			return true
		}
		for _, a := range s.Args {
			if has(a) {
				return true
			}
		}
		for _, v := range s.Env {
			if has(v) {
				return true
			}
		}
	}
	return false
}

// Plan compares what Build produced against what is already on disk.
//
// Separating this from Apply is what lets --dry-run and --check share every
// line of logic with a real run, so the thing that reports is the thing that
// writes.
func Plan(root string, files []File) ([]Change, error) {
	r, err := safepath.NewRoot(root)
	if err != nil {
		return nil, err
	}

	changes := make([]Change, 0, len(files))
	for _, f := range files {
		abs, err := r.Resolve(filepath.FromSlash(f.Path))
		if err != nil {
			return nil, err
		}
		existing, err := os.ReadFile(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			changes = append(changes, Change{File: f, State: Created})
		case err != nil:
			return nil, err
		case bytes.Equal(existing, f.Content):
			changes = append(changes, Change{File: f, State: Unchanged})
		default:
			changes = append(changes, Change{File: f, State: Updated})
		}
	}

	sort.SliceStable(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})
	return changes, nil
}

// Apply writes the changes that are not already satisfied.
//
// An unchanged file is not rewritten. That keeps mtimes stable, so a packed
// repository does not look modified to a build cache or a file watcher every
// time somebody runs pack.
func Apply(root string, changes []Change) error {
	r, err := safepath.NewRoot(root)
	if err != nil {
		return err
	}
	for _, c := range changes {
		if c.State == Unchanged {
			continue
		}
		abs, err := r.Resolve(filepath.FromSlash(c.Path))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, c.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func setIfNotEmpty(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}
