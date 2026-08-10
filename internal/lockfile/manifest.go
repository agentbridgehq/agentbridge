// Package lockfile defines the two files that make an installation
// reproducible: a manifest of intent and a lock of what that intent resolved
// to.
//
// The split is the one npm, Cargo and Bundler all landed on, and it is worth
// being explicit about why it applies here. The manifest says what a person
// wants — "the database plugin, version 1.2" — and is written by hand. The lock
// says what that turned out to mean on the day it was resolved — a commit, a
// content address, and the capabilities the resulting package actually has —
// and is written by the tool.
//
// The second file is the one that matters for this project. A plugin is not
// only code but instructions handed to an agent with tool access, so "what
// changed when we bumped this version" is a security question, not a build
// detail. The lock is therefore designed to be *read* in a pull request: it
// records the skills and servers a plugin contains and the capabilities it
// obtains, so granting an agent the ability to execute processes or reach the
// network shows up as a reviewable line in a diff instead of as an invisible
// consequence of a version bump.
package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManifestName is the file declaring intent.
const ManifestName = "agentbridge.yaml"

// ManifestVersion is the current manifest schema version.
const ManifestVersion = 1

// Manifest is the declared set of plugins for one scope.
type Manifest struct {
	// Version is the manifest schema version, not the tool's version.
	Version int `yaml:"version"`
	// Plugins are declared in file order, which is preserved on rewrite so a
	// hand-edited file keeps the shape its author gave it.
	Plugins []Entry `yaml:"plugins"`

	// path is where this manifest was read from, for error messages.
	path string `yaml:"-"`
}

// Entry is one declared plugin.
type Entry struct {
	// Source is a plugin reference: a local path or a repository, optionally
	// with a revision and subdirectory. See internal/source.
	Source string `yaml:"source"`
	// Clients restricts installation to these client ids. Empty means every
	// client detected on the machine, which is the point of the tool and so
	// the default.
	Clients []string `yaml:"clients,omitempty"`
	// Scope restricts installation to user or project configuration. Empty
	// means both, wherever the client supports them.
	Scope string `yaml:"scope,omitempty"`
}

// Path returns where the manifest was loaded from.
func (m *Manifest) Path() string { return m.path }

// LoadManifest reads a manifest. A missing file yields an empty manifest, not
// an error: "no plugins declared" is a perfectly ordinary state, and it is what
// a fresh project looks like.
func LoadManifest(path string) (*Manifest, error) {
	m := &Manifest{Version: ManifestVersion, path: path}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(raw, m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.path = path

	if m.Version == 0 {
		m.Version = ManifestVersion
	}
	if m.Version > ManifestVersion {
		return nil, fmt.Errorf("%s: manifest version %d is newer than this build understands (%d); upgrade agentbridge",
			path, m.Version, ManifestVersion)
	}
	for i, e := range m.Plugins {
		if strings.TrimSpace(e.Source) == "" {
			return nil, fmt.Errorf("%s: plugin %d has no source", path, i+1)
		}
	}
	return m, nil
}

// Exists reports whether the manifest file is present.
func (m *Manifest) Exists() bool {
	_, err := os.Stat(m.path)
	return err == nil
}

// Find returns the entry declaring a source, if any.
func (m *Manifest) Find(source string) (int, bool) {
	for i, e := range m.Plugins {
		if e.Source == source {
			return i, true
		}
	}
	return -1, false
}

// Add appends an entry, replacing any existing declaration of the same source.
// Re-adding is how a user changes the clients or scope of something already
// declared, so it must not produce a duplicate.
func (m *Manifest) Add(e Entry) {
	if i, ok := m.Find(e.Source); ok {
		m.Plugins[i] = e
		return
	}
	m.Plugins = append(m.Plugins, e)
}

// Remove deletes the entry declaring a source. It reports whether anything was
// removed.
func (m *Manifest) Remove(source string) bool {
	i, ok := m.Find(source)
	if !ok {
		return false
	}
	m.Plugins = append(m.Plugins[:i], m.Plugins[i+1:]...)
	return true
}

// Save writes the manifest.
func (m *Manifest) Save() error {
	if m.Version == 0 {
		m.Version = ManifestVersion
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# Plugins this project or user declares. Edit by hand or use\n")
	b.WriteString("# `agentbridge install --save`. The resolved result is recorded in\n")
	b.WriteString("# agentbridge.lock, which should be committed alongside it.\n")

	enc := yaml.NewEncoder(&strings.Builder{})
	_ = enc.Close()

	raw, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	b.Write(raw)

	return writeFileAtomic(m.path, []byte(b.String()))
}

func writeFileAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".agentbridge-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
