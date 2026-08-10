package lockfile

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LockName is the file recording what the manifest resolved to.
const LockName = "agentbridge.lock"

// LockVersion is the current lock schema version.
const LockVersion = 1

// Lock is the resolved state of a manifest.
type Lock struct {
	Version int      `yaml:"version"`
	Plugins []Locked `yaml:"plugins"`

	path string `yaml:"-"`
}

// Locked is one resolved plugin.
//
// Field order here is the order it appears in the file, and it is chosen for
// reading rather than for machines: identity, then where it came from, then
// what it can do, then what is in it. A reviewer looking at a version bump
// should meet the capability change before the file list.
type Locked struct {
	// Name is the plugin's own name, from its manifest.
	Name string `yaml:"name"`
	// Source is the reference as declared, revision and all.
	Source string `yaml:"source"`
	// Resolved is the same reference with any branch or tag replaced by the
	// commit it pointed at. This is what reproduces the install.
	Resolved string `yaml:"resolved,omitempty"`
	// Version is the plugin's declared version, for humans. It is not used to
	// resolve anything — the digests do that.
	Version string `yaml:"pluginVersion,omitempty"`

	// TreeDigest addresses the package bytes; IRDigest addresses the parsed
	// plugin. Both are recorded because they answer different questions: a
	// changed script alters the first without touching the second.
	TreeDigest string `yaml:"treeDigest"`
	IRDigest   string `yaml:"irDigest"`

	// Capabilities is the access this plugin obtains, inferred at resolve
	// time. This is the line a security reviewer should read first: a version
	// bump that adds `exec` or `network` is a different change from one that
	// does not, and without this the difference is invisible.
	Capabilities []string `yaml:"capabilities,omitempty"`

	// Skills and Servers make the contents reviewable. A new skill appearing
	// in a plugin is new instruction text handed to an agent, which is a
	// change worth seeing in a diff.
	Skills  []string       `yaml:"skills,omitempty"`
	Servers []LockedServer `yaml:"servers,omitempty"`

	// Clients restricts installation, mirroring the manifest entry.
	Clients []string `yaml:"clients,omitempty"`
	Scope   string   `yaml:"scope,omitempty"`
}

// LockedServer is a one-line summary of an MCP server, enough to see in a diff
// that its command or endpoint changed.
type LockedServer struct {
	Name      string `yaml:"name"`
	Transport string `yaml:"transport"`
	// Target is the command line for stdio servers, or the URL for remote
	// ones. Truncated for readability, since the digests carry the exactness.
	Target string `yaml:"target,omitempty"`
}

// Path returns where the lock was loaded from.
func (l *Lock) Path() string { return l.path }

// LoadLock reads a lock file. A missing file yields an empty lock.
func LoadLock(path string) (*Lock, error) {
	l := &Lock{Version: LockVersion, path: path}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(raw, l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	l.path = path

	if l.Version == 0 {
		l.Version = LockVersion
	}
	if l.Version > LockVersion {
		return nil, fmt.Errorf("%s: lock version %d is newer than this build understands (%d); upgrade agentbridge",
			path, l.Version, LockVersion)
	}
	return l, nil
}

// Exists reports whether the lock file is present.
func (l *Lock) Exists() bool {
	_, err := os.Stat(l.path)
	return err == nil
}

// FindBySource returns the locked entry for a declared source.
//
// Lookup is by declared source rather than by plugin name because the name is
// only known after fetching, and the whole point of the lock is to avoid
// fetching in order to find out what to fetch.
func (l *Lock) FindBySource(source string) (*Locked, bool) {
	for i := range l.Plugins {
		if l.Plugins[i].Source == source {
			return &l.Plugins[i], true
		}
	}
	return nil, false
}

// FindByName returns the locked entry for a plugin name.
func (l *Lock) FindByName(name string) (*Locked, bool) {
	for i := range l.Plugins {
		if l.Plugins[i].Name == name {
			return &l.Plugins[i], true
		}
	}
	return nil, false
}

// Names returns every locked plugin name.
func (l *Lock) Names() []string {
	out := make([]string, 0, len(l.Plugins))
	for _, p := range l.Plugins {
		out = append(out, p.Name)
	}
	return out
}

// Set adds or replaces an entry.
func (l *Lock) Set(entry Locked) {
	for i := range l.Plugins {
		if l.Plugins[i].Source == entry.Source {
			l.Plugins[i] = entry
			return
		}
	}
	l.Plugins = append(l.Plugins, entry)
}

// Remove deletes the entry for a declared source.
func (l *Lock) Remove(source string) bool {
	for i := range l.Plugins {
		if l.Plugins[i].Source == source {
			l.Plugins = append(l.Plugins[:i], l.Plugins[i+1:]...)
			return true
		}
	}
	return false
}

// Save writes the lock with a deterministic layout.
//
// Sorting matters more than it looks. A lock file that reorders itself between
// runs produces diff noise, and diff noise is how a meaningful change — a new
// capability, a changed command — gets scrolled past.
func (l *Lock) Save() error {
	if l.Version == 0 {
		l.Version = LockVersion
	}
	l.normalize()

	var b strings.Builder
	b.WriteString("# Generated by agentbridge. Commit this file.\n")
	b.WriteString("#\n")
	b.WriteString("# Review the `capabilities` line on every change: it is what a plugin can\n")
	b.WriteString("# do on this machine. `exec` means it runs a process with your access;\n")
	b.WriteString("# `secrets` means it handles credentials.\n")

	raw, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	b.Write(raw)

	return writeFileAtomic(l.path, []byte(b.String()))
}

func (l *Lock) normalize() {
	sort.Slice(l.Plugins, func(i, j int) bool {
		if l.Plugins[i].Name != l.Plugins[j].Name {
			return l.Plugins[i].Name < l.Plugins[j].Name
		}
		return l.Plugins[i].Source < l.Plugins[j].Source
	})
	for i := range l.Plugins {
		p := &l.Plugins[i]
		sort.Strings(p.Capabilities)
		sort.Strings(p.Skills)
		sort.Strings(p.Clients)
		sort.Slice(p.Servers, func(a, b int) bool { return p.Servers[a].Name < p.Servers[b].Name })
	}
}

// Diff describes how one lock differs from another, for reporting a change
// before it is written.
type Diff struct {
	Added   []Locked
	Removed []Locked
	Changed []Change
}

// Change is one plugin whose resolution moved.
type Change struct {
	Before Locked
	After  Locked
}

// CapabilitiesGained returns capabilities the plugin did not have before.
//
// This is the part of a change that deserves a different reaction from the
// rest. A plugin that gains `exec` between two versions has become a different
// proposition, whatever the version number says.
func (c Change) CapabilitiesGained() []string {
	had := map[string]bool{}
	for _, cap := range c.Before.Capabilities {
		had[cap] = true
	}
	var gained []string
	for _, cap := range c.After.Capabilities {
		if !had[cap] {
			gained = append(gained, cap)
		}
	}
	return gained
}

// Empty reports whether nothing differs.
func (d Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// DiffLocks compares two locks.
func DiffLocks(before, after *Lock) Diff {
	var d Diff

	beforeBySource := map[string]Locked{}
	for _, p := range before.Plugins {
		beforeBySource[p.Source] = p
	}
	afterBySource := map[string]Locked{}
	for _, p := range after.Plugins {
		afterBySource[p.Source] = p
	}

	for _, p := range after.Plugins {
		old, existed := beforeBySource[p.Source]
		switch {
		case !existed:
			d.Added = append(d.Added, p)
		case old.TreeDigest != p.TreeDigest || old.Resolved != p.Resolved:
			d.Changed = append(d.Changed, Change{Before: old, After: p})
		}
	}
	for _, p := range before.Plugins {
		if _, still := afterBySource[p.Source]; !still {
			d.Removed = append(d.Removed, p)
		}
	}
	return d
}
