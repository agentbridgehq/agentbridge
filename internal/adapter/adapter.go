// Package adapter writes plugins into the agent clients installed on a
// machine.
//
// An adapter answers three questions for one client: where its configuration
// lives, what to write there for a given plugin, and — the part that
// distinguishes this tool — what it could not carry across and why.
//
// Two rules shape every adapter:
//
//  1. **Nothing is dropped silently.** A client that cannot take a component
//     produces a Loss with a stable reason code, which surfaces in the
//     fidelity report on every install. Silent degradation is the ecosystem's
//     default failure mode; refusing to participate in it is the product.
//
//  2. **Planning is separate from writing.** Plan computes operations and
//     their exact resulting bytes without touching disk. That gives --dry-run
//     for free, makes every change reviewable as a diff before it happens, and
//     means an install that fails halfway can be reasoned about.
//
// Adapters never invent a location. Where a client's install path for a
// component is not documented by its vendor, the adapter declares that
// component unsupported and says so, rather than guessing at a path and
// writing into a user's machine on a hunch. Those gaps are what the
// conformance harness (M10) is for.
package adapter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
)

// Support describes how well a client handles a component type.
type Support string

const (
	// SupportNative means the client loads this component as-is.
	SupportNative Support = "native"
	// SupportTranslated means we convert it into a client-specific form.
	SupportTranslated Support = "translated"
	// SupportNone means the client has no equivalent at all.
	SupportNone Support = "none"
	// SupportUndocumented means the client may well support this, but its
	// vendor has not published where the files go. We will not guess. The
	// conformance harness resolves these empirically.
	SupportUndocumented Support = "undocumented"
)

// Client identifies a target agent client.
type Client struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Conformant records whether the client implements Agent Plugins natively.
	Conformant bool `json:"conformant"`
	// Skills and MCP describe component support.
	Skills Support `json:"skills"`
	MCP    Support `json:"mcp"`
	// ConfigDoc points at the vendor documentation the paths came from, so a
	// reviewer can check the claim rather than trust it.
	ConfigDoc string `json:"configDoc,omitempty"`
}

// Scope is where a plugin is installed.
type Scope string

const (
	// ScopeUser installs for the current user, across all projects.
	ScopeUser Scope = "user"
	// ScopeProject installs into the current project, so it can be committed
	// and shared with a team.
	ScopeProject Scope = "project"
)

// Installation is one place a client reads configuration from on this machine.
type Installation struct {
	Client Client `json:"client"`
	Scope  Scope  `json:"scope"`
	// ConfigPath is the file holding MCP configuration, if any.
	ConfigPath string `json:"configPath,omitempty"`
	// PackageDir is where a full plugin package is installed, if the client
	// supports one.
	PackageDir string `json:"packageDir,omitempty"`
	// Evidence explains why we believe this client is installed. Shown by
	// `doctor` so a wrong detection can be diagnosed rather than argued with.
	Evidence string `json:"evidence,omitempty"`
}

// Env supplies the filesystem context adapters resolve paths against.
// Injecting it keeps detection testable: a test can point HomeDir at a
// fixture tree instead of the developer's real machine.
type Env struct {
	HomeDir    string
	ConfigDir  string
	DataDir    string
	ProjectDir string
	GOOS       string
}

// OpKind is a filesystem action.
type OpKind string

const (
	// OpWriteFile replaces a file's contents.
	OpWriteFile OpKind = "write-file"
	// OpRemoveFile deletes a file.
	OpRemoveFile OpKind = "remove-file"
	// OpCopyTree copies a directory recursively.
	OpCopyTree OpKind = "copy-tree"
	// OpRemoveTree deletes a directory recursively.
	OpRemoveTree OpKind = "remove-tree"
)

// Op is a single planned change, carrying both the before and after bytes so
// it can be rendered as a diff without re-reading anything.
type Op struct {
	Kind OpKind `json:"kind"`
	Path string `json:"path"`
	// Before is the file's existing content, nil if it does not exist.
	Before []byte `json:"-"`
	// After is the content to write.
	After []byte `json:"-"`
	// SourceDir is the directory to copy, for OpCopyTree.
	SourceDir string `json:"sourceDir,omitempty"`
	// Note explains the operation in one line, for the plan summary.
	Note string `json:"note,omitempty"`
}

// Unchanged reports whether an operation would leave the file as it is.
// Re-running an install must be a no-op, and the plan should say so rather
// than rewriting identical bytes and churning the file's mtime.
func (o Op) Unchanged() bool {
	switch o.Kind {
	case OpWriteFile:
		return o.Before != nil && string(o.Before) == string(o.After)
	case OpRemoveFile, OpRemoveTree:
		return o.Before == nil && o.SourceDir == ""
	}
	return false
}

// Loss is one thing that did not survive translation into a client.
type Loss struct {
	// Code is a stable reason code. Fidelity reports and, later, machine
	// consumers key off these, so renaming one is a breaking change.
	Code string `json:"code"`
	// Component is the skill or server affected, empty for plugin-wide losses.
	Component string `json:"component,omitempty"`
	Reason    string `json:"reason"`
}

// Stable loss codes.
const (
	LossSkillsUnsupported      = "client.skills_unsupported"
	LossSkillsUndocumented     = "client.skills_location_undocumented"
	LossMCPUnsupported         = "client.mcp_unsupported"
	LossTransportUnsupported   = "client.transport_unsupported"
	LossExtensionsDropped      = "client.extensions_dropped"
	LossNativeComponentDropped = "client.native_component_dropped"
	LossFlatSkillRestructured  = "client.flat_skill_restructured"
	LossSecretInPlaintext      = "client.secret_written_plaintext"
)

// Coverage counts how many of a component type were carried.
type Coverage struct {
	Carried int `json:"carried"`
	Total   int `json:"total"`
}

func (c Coverage) String() string { return fmt.Sprintf("%d/%d", c.Carried, c.Total) }

// Complete reports whether everything was carried.
func (c Coverage) Complete() bool { return c.Carried == c.Total }

// Fidelity is the honest account of what a client actually received.
type Fidelity struct {
	Skills     Coverage `json:"skills"`
	MCPServers Coverage `json:"mcpServers"`
	Losses     []Loss   `json:"losses,omitempty"`
}

// Degraded reports whether anything was lost.
func (f Fidelity) Degraded() bool {
	return len(f.Losses) > 0 || !f.Skills.Complete() || !f.MCPServers.Complete()
}

// AddLoss records something that did not survive.
func (f *Fidelity) AddLoss(code, component, format string, args ...any) {
	f.Losses = append(f.Losses, Loss{
		Code:      code,
		Component: component,
		Reason:    fmt.Sprintf(format, args...),
	})
}

// Plan is what an adapter proposes to do, computed without touching disk.
type Plan struct {
	Installation Installation `json:"installation"`
	PluginName   string       `json:"pluginName"`
	Ops          []Op         `json:"ops"`
	Fidelity     Fidelity     `json:"fidelity"`

	// The fields below record precisely what this plan claims ownership of, so
	// the receipt written after a successful apply can drive an exact removal
	// later. Without them, uninstall would have to guess from a naming
	// convention and would eventually delete something a user wrote by hand.
	ConfigKeys    [][]string `json:"configKeys,omitempty"`
	BlockSections []string   `json:"blockSections,omitempty"`
	PackageDir    string     `json:"packageDir,omitempty"`
}

// Changed reports whether the plan would alter anything.
func (p *Plan) Changed() bool {
	for _, op := range p.Ops {
		if !op.Unchanged() {
			return true
		}
	}
	return false
}

// Adapter installs plugins into one client.
type Adapter interface {
	// Client describes the target.
	Client() Client
	// Detect returns the places this client reads configuration from on this
	// machine. An empty result means the client is not installed.
	Detect(env Env) []Installation
	// Plan computes what installing p into inst would do. src is the plugin's
	// source directory, needed when a client takes a whole package.
	Plan(inst Installation, p *ir.Plugin, src *safepath.Root) (*Plan, error)
	// PlanRemove computes what removing a plugin by name would do.
	PlanRemove(inst Installation, pluginName string) (*Plan, error)
}

// ManagedKey builds the configuration key for one of a plugin's MCP servers.
//
// Namespacing by plugin serves two purposes: two plugins may reasonably ship a
// server called "db", and an uninstall needs to identify exactly what it owns.
// Removal still consults the receipt rather than deleting everything matching
// this prefix, so a user's own key that happens to look like ours is never
// touched.
func ManagedKey(pluginName, serverName string) string {
	return pluginName + "." + serverName
}

// SortServers returns a plugin's servers in name order, so a plan is
// deterministic and a config file does not churn between runs.
func SortServers(servers []ir.MCPServer) []ir.MCPServer {
	out := append([]ir.MCPServer(nil), servers...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// NoteSkillsUnsupported records the standard loss for a client that cannot
// take skills, choosing the wording by why it cannot.
func NoteSkillsUnsupported(f *Fidelity, c Client, skills []ir.Skill) {
	if len(skills) == 0 {
		return
	}
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Name
	}
	list := strings.Join(names, ", ")

	switch c.Skills {
	case SupportNone:
		f.AddLoss(LossSkillsUnsupported, "",
			"%s has no skills mechanism; %d skill(s) not installed: %s", c.Name, len(skills), list)
	case SupportUndocumented:
		f.AddLoss(LossSkillsUndocumented, "",
			"%s may support skills, but its vendor has not documented where they are installed; "+
				"%d skill(s) not installed: %s. We will not write to an unverified path", c.Name, len(skills), list)
	}
}
