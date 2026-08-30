// Package receipt records exactly what was written into each client, so that
// uninstall removes what we added and nothing else.
//
// The alternative — deleting configuration keys that match a naming convention
// — is how tools end up destroying a user's own entry that happened to share a
// prefix. A receipt makes removal a lookup rather than a guess: if it is not in
// the receipt, we did not write it, so we do not touch it.
//
// This is deliberately separate from the lockfile. The lockfile records intent,
// which is shared and committed; a receipt records what physically happened on
// one machine, which is not.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
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
	// Source is the pinned reference the plugin was installed from — a branch
	// or tag replaced by the commit it resolved to, so the record reproduces
	// exactly these bytes rather than whatever that ref points at later.
	Source string `json:"source,omitempty"`
	// TreeDigest is the content address of the installed package. Together with
	// Source it is what makes an install reproducible and a substitution
	// detectable.
	TreeDigest string `json:"treeDigest,omitempty"`
	// SourceIdentity names the upstream with the revision removed, so an
	// upgrade of one plugin can be told apart from a second, different plugin
	// claiming the same name.
	SourceIdentity string `json:"sourceIdentity,omitempty"`
	// ConfigPath is the file that was edited, if any.
	ConfigPath string `json:"configPath,omitempty"`
	// ConfigKeys are the key paths written into ConfigPath. Each is a path
	// through the document, so removal is exact rather than pattern-matched.
	ConfigKeys [][]string `json:"configKeys,omitempty"`
	// CreatedContainers are objects the install created to hold its entries,
	// removed on uninstall if they are empty by then. Absent on receipts
	// written before this was recorded, which simply means nothing is
	// reclaimed — the old, safe behaviour.
	CreatedContainers [][]string `json:"createdContainers,omitempty"`
	// AuxConfigPath and AuxConfigKeys record a second configuration file the
	// install edited, for a client whose components do not all live in one
	// file. Removal deletes exactly these keys from exactly this path, the
	// same contract as ConfigKeys.
	AuxConfigPath        string     `json:"auxConfigPath,omitempty"`
	AuxConfigKeys        [][]string `json:"auxConfigKeys,omitempty"`
	AuxCreatedContainers [][]string `json:"auxCreatedContainers,omitempty"`
	// BlockSections are managed-block section headers written, for
	// line-oriented formats.
	BlockSections []string `json:"blockSections,omitempty"`
	// PackageDir is a directory installed wholesale, removed on uninstall.
	PackageDir string `json:"packageDir,omitempty"`
	// Managed names the manifest scope that declared this plugin, empty for an
	// ad-hoc install.
	//
	// This is what lets `sync` converge without destroying anything: it may
	// remove a plugin that a manifest used to declare and no longer does, and
	// it must never remove one a user installed by hand. Without the
	// distinction, sync would either leave orphans behind or delete work the
	// user did deliberately.
	Managed     string    `json:"managed,omitempty"`
	InstalledAt time.Time `json:"installedAt"`
}

// Key identifies an entry uniquely.
func (e Entry) Key() string { return e.Plugin + "\x00" + e.Client + "\x00" + e.Scope }

// Store is the on-disk receipt file.
type Store struct {
	path    string
	entries map[string]Entry
	// baseline is the digest of the file as last read or written.
	//
	// The store is a whole-file document, so a Save writes back everything the
	// instance knows. An instance that was loaded before some other write —
	// another process, or simply an older instance still held in memory —
	// would therefore erase receipts it never saw, and the plugins they
	// described would become unremovable with nothing to indicate why. Saving
	// checks the baseline still holds and refuses rather than clobbering.
	baseline string
}

func digestOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// currentDigest returns the digest of the file on disk, or the empty string if
// it is absent.
func (s *Store) currentDigest() string {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return ""
	}
	return digestOf(raw)
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
	s.baseline = digestOf(raw)

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

	if now := s.currentDigest(); now != s.baseline {
		return fmt.Errorf("%s changed on disk since it was read; "+
			"another agentbridge run may be in progress. Re-run the command", s.path)
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
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	s.baseline = digestOf(raw)
	return nil
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
