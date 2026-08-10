// Package receipt records exactly what was written into each client, so that
// uninstall removes what we added and nothing else.
//
// The alternative — deleting configuration keys that match a naming convention
// — is how tools end up destroying a user's own entry that happened to share a
// prefix. A receipt makes removal a lookup rather than a guess: if it is not in
// the receipt, we did not write it, so we do not touch it.
//
// This is deliberately separate from the lockfile that arrives in M4. The
// lockfile records intent, which is shared and committed; a receipt records
// what physically happened on one machine, which is not.
package receipt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FileName is the receipt store's filename inside the AgentBridge state
// directory.
const FileName = "receipts.json"

// Entry records one plugin's installation into one client scope.
type Entry struct {
	Plugin string `json:"plugin"`
	Client string `json:"client"`
	Scope  string `json:"scope"`
	// Digest is the plugin's IR digest at install time, so drift between what
	// is installed and what a lockfile expects is detectable.
	Digest string `json:"digest,omitempty"`
	// ConfigPath is the file that was edited, if any.
	ConfigPath string `json:"configPath,omitempty"`
	// ConfigKeys are the key paths written into ConfigPath. Each is a path
	// through the document, so removal is exact rather than pattern-matched.
	ConfigKeys [][]string `json:"configKeys,omitempty"`
	// BlockSections are managed-block section headers written, for
	// line-oriented formats.
	BlockSections []string `json:"blockSections,omitempty"`
	// PackageDir is a directory installed wholesale, removed on uninstall.
	PackageDir  string    `json:"packageDir,omitempty"`
	InstalledAt time.Time `json:"installedAt"`
}

// Key identifies an entry uniquely.
func (e Entry) Key() string { return e.Plugin + "\x00" + e.Client + "\x00" + e.Scope }

// Store is the on-disk receipt file.
type Store struct {
	path    string
	entries map[string]Entry
}

// Open loads the receipt store, creating an empty one if absent.
//
// A corrupt store is an error rather than something to reset: silently
// discarding it would orphan every installed plugin, leaving files in clients'
// configs that nothing knows how to remove.
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, FileName)
	s := &Store{path: path, entries: map[string]Entry{}}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}

	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w (remove the file by hand to reset, "+
			"but note that anything already installed will then have to be removed manually)", path, err)
	}
	for _, e := range entries {
		s.entries[e.Key()] = e
	}
	return s, nil
}

// Path returns the store's file path.
func (s *Store) Path() string { return s.path }

// Put records an installation, replacing any previous entry for the same
// plugin, client and scope.
func (s *Store) Put(e Entry) {
	if e.InstalledAt.IsZero() {
		e.InstalledAt = time.Now().UTC()
	}
	s.entries[e.Key()] = e
}

// Get returns the entry for a plugin in a client scope.
func (s *Store) Get(plugin, client, scope string) (Entry, bool) {
	e, ok := s.entries[Entry{Plugin: plugin, Client: client, Scope: scope}.Key()]
	return e, ok
}

// Delete removes an entry.
func (s *Store) Delete(plugin, client, scope string) {
	delete(s.entries, Entry{Plugin: plugin, Client: client, Scope: scope}.Key())
}

// ForPlugin returns every entry for a plugin, across clients and scopes.
func (s *Store) ForPlugin(plugin string) []Entry {
	var out []Entry
	for _, e := range s.entries {
		if e.Plugin == plugin {
			out = append(out, e)
		}
	}
	sortEntries(out)
	return out
}

// All returns every entry in a stable order.
func (s *Store) All() []Entry {
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sortEntries(out)
	return out
}

// Save writes the store atomically.
func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	entries := s.All()
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".receipts-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(raw); err != nil {
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
	return os.Rename(tmpName, s.path)
}

func sortEntries(out []Entry) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Plugin != out[j].Plugin {
			return out[i].Plugin < out[j].Plugin
		}
		if out[i].Client != out[j].Client {
			return out[i].Client < out[j].Client
		}
		return out[i].Scope < out[j].Scope
	})
}
