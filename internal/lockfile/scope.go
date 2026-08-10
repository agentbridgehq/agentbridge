package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Scope is where a manifest and lock live.
type Scope string

const (
	// ScopeProject is the manifest in the current project, meant to be
	// committed and shared with a team.
	ScopeProject Scope = "project"
	// ScopeUser is the manifest for this machine's user, applying everywhere.
	ScopeUser Scope = "user"
)

// Workspace locates the manifest and lock for a scope.
type Workspace struct {
	Scope Scope
	Dir   string
}

// ProjectWorkspace returns the workspace rooted at a project directory.
func ProjectWorkspace(dir string) Workspace { return Workspace{Scope: ScopeProject, Dir: dir} }

// UserWorkspace returns the workspace for the current user's state directory.
func UserWorkspace(stateDir string) Workspace { return Workspace{Scope: ScopeUser, Dir: stateDir} }

// ManifestPath returns the manifest location.
func (w Workspace) ManifestPath() string { return filepath.Join(w.Dir, ManifestName) }

// LockPath returns the lock location.
func (w Workspace) LockPath() string { return filepath.Join(w.Dir, LockName) }

// Load reads both files for this workspace.
func (w Workspace) Load() (*Manifest, *Lock, error) {
	m, err := LoadManifest(w.ManifestPath())
	if err != nil {
		return nil, nil, err
	}
	l, err := LoadLock(w.LockPath())
	if err != nil {
		return nil, nil, err
	}
	return m, l, nil
}

// FindProjectRoot walks up from dir looking for a manifest.
//
// Walking up matches how every other project-scoped tool behaves, and it is
// what makes `agentbridge sync` work from a subdirectory. The search stops at a
// repository boundary rather than continuing to the filesystem root: picking up
// a manifest from an unrelated parent directory would install plugins the user
// never declared for this project.
func FindProjectRoot(dir string) (string, bool) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, ManifestName)); err == nil {
			return dir, true
		}
		// A repository root is a boundary. Beyond it we are in someone else's
		// project.
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Resolution is the merged view of every scope that applies.
type Resolution struct {
	// Entries are the declared plugins after precedence is applied, in the
	// order they should be installed.
	Entries []ScopedEntry
	// Workspaces are every scope consulted, whether or not it declared
	// anything.
	//
	// This is not redundant with Entries. Removing the last line from a
	// manifest produces zero entries, and a sync that only looked at entries
	// would then never open that lock — leaving it listing a plugin nobody
	// declares any more. An empty manifest is a statement about what should be
	// installed, not an absence of one.
	Workspaces []Workspace
}

// ScopedEntry is a manifest entry together with where it came from.
//
// The embedded Entry has its own `Scope` field, meaning "install into user or
// project *client configuration*", which is a different axis from the manifest
// scope this entry was declared in. Naming both `Scope` compiled but shadowed
// one with the other, so this one is `DeclaredIn` — the two are genuinely
// different questions and reading the code should not require remembering
// which shadow wins.
type ScopedEntry struct {
	Entry
	// DeclaredIn is the manifest scope this entry came from.
	DeclaredIn Scope
	// Workspace is where the declaration lives, so its lock can be updated.
	Workspace Workspace
}

// Merge combines user and project scopes.
//
// Precedence is project over user, matching the rule in docs/03: the narrower
// scope wins, because a project's declaration is a deliberate statement about
// *this* codebase and a user's is a default. A source declared in both is
// installed once, under the project's terms.
//
// The org scope that will eventually sit above both is deliberately absent:
// centrally-pushed policy is Phase 3 work, and stubbing it here would invite
// code that assumes a shape we have not designed yet.
func Merge(user, project *Manifest, userWS, projectWS Workspace) Resolution {
	res := Resolution{Workspaces: []Workspace{projectWS, userWS}}
	seen := map[string]bool{}

	for _, e := range project.Plugins {
		seen[e.Source] = true
		res.Entries = append(res.Entries, ScopedEntry{Entry: e, DeclaredIn: ScopeProject, Workspace: projectWS})
	}
	for _, e := range user.Plugins {
		if seen[e.Source] {
			continue
		}
		res.Entries = append(res.Entries, ScopedEntry{Entry: e, DeclaredIn: ScopeUser, Workspace: userWS})
	}
	return res
}

// Describe renders a scope for messages.
func (w Workspace) Describe() string {
	return fmt.Sprintf("%s (%s)", w.Scope, w.ManifestPath())
}
