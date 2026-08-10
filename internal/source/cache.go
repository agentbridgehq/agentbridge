package source

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// stampSuffix names the sidecar recording what a cache entry is and what it
// hashed to when it was written.
//
// It lives *beside* the package directory rather than inside it. Putting it
// inside would mean the digest either covers the stamp — which cannot work,
// since the stamp contains the digest — or excludes a reserved filename, which
// would carve a small hole in the integrity check for any plugin that happened
// to ship a file by that name.
const stampSuffix = ".json"

// Cache stores materialized packages, addressed by their pinned identity.
//
// Two things it buys, and the second is the one that matters:
//
//   - A pinned reference already present is never fetched again, so repeated
//     installs and air-gapped machines work without network access.
//   - Every entry carries the tree digest it had when it was written, and that
//     digest is re-verified on reuse. The cache lives in a writable directory
//     on a developer's machine, which makes it a tampering target: poison one
//     entry and every future install of that plugin is compromised, silently
//     and with a valid-looking pin. Re-verification turns that into an error.
type Cache struct{ root string }

// NewCache returns a cache rooted at dir.
func NewCache(dir string) *Cache { return &Cache{root: dir} }

// Root returns the cache directory.
func (c *Cache) Root() string { return c.root }

// Stamp is the metadata recorded alongside a cached package.
type Stamp struct {
	Kind Kind `json:"kind"`
	// URL and Commit identify a git source; Commit is always a full object id.
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
	Subdir string `json:"subdir,omitempty"`
	// TreeDigest is the content address of the package as fetched.
	TreeDigest string `json:"treeDigest"`
}

// key derives the cache directory name for a pinned source.
//
// Only immutable inputs contribute. A branch name must never appear here, or
// two different commits would collide on one entry and the cache would start
// serving stale bytes for a pin that claims to be exact.
func (c *Cache) key(s Stamp) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		string(s.Kind), s.URL, s.Commit, s.Subdir,
	}, "\x00")))
	return hex.EncodeToString(h[:])[:32]
}

// Dir returns the directory a pinned source would occupy.
func (c *Cache) Dir(s Stamp) string {
	return filepath.Join(c.root, "src", string(s.Kind), c.key(s))
}

func (c *Cache) stampPath(s Stamp) string { return c.Dir(s) + stampSuffix }

// Lookup returns a cached package if it is present and still intact.
//
// A corrupt or modified entry is reported rather than silently repaired: a
// mismatch here means either disk corruption or deliberate tampering, and
// quietly re-fetching over the evidence would hide both.
func (c *Cache) Lookup(s Stamp) (dir string, stamp *Stamp, err error) {
	dir = c.Dir(s)

	raw, err := os.ReadFile(c.stampPath(s))
	if os.IsNotExist(err) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}

	var found Stamp
	if err := json.Unmarshal(raw, &found); err != nil {
		return "", nil, fmt.Errorf("cache entry %s is unreadable: %w", dir, err)
	}
	if err := VerifyTreeDigest(dir, found.TreeDigest); err != nil {
		return "", nil, fmt.Errorf("cached copy of %s has been modified since it was fetched: %w", found.URL, err)
	}
	return dir, &found, nil
}

// Put stores a package, computing and recording its tree digest.
//
// The package is assembled in a temporary directory and moved into place, so a
// failure part-way through cannot leave a half-written entry that a later
// lookup would treat as complete.
func (c *Cache) Put(src string, s Stamp) (string, *Stamp, error) {
	dir := c.Dir(s)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", nil, err
	}

	staging, err := os.MkdirTemp(filepath.Dir(dir), ".staging-*")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(staging)

	if err := copyPackage(src, staging); err != nil {
		return "", nil, err
	}

	digest, err := TreeDigest(staging)
	if err != nil {
		return "", nil, err
	}
	s.TreeDigest = digest

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", nil, err
	}

	// Order matters. The package is moved into place first and the stamp
	// written second, because the stamp is what Lookup treats as proof the
	// entry is complete. A crash between the two leaves a directory with no
	// stamp, which reads as a miss and is simply re-fetched — whereas the
	// reverse order would leave a stamp vouching for bytes that were never
	// fully written.
	if err := os.RemoveAll(dir); err != nil {
		return "", nil, err
	}
	if err := os.Rename(staging, dir); err != nil {
		return "", nil, err
	}
	if err := os.WriteFile(c.stampPath(s), append(raw, '\n'), 0o644); err != nil {
		return "", nil, err
	}
	return dir, &s, nil
}

// Clear removes every cached package.
func (c *Cache) Clear() error {
	return os.RemoveAll(filepath.Join(c.root, "src"))
}

// copyPackage copies a directory tree, excluding version-control metadata and
// symlinks.
//
// Symlinks are dropped for the same reason adapter.Apply drops them: a package
// containing a link to somewhere else on the machine would otherwise be
// installed into a client's configuration directory pointing wherever the link
// said.
func copyPackage(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyOneFile(p, filepath.Join(dst, rel), info.Mode().Perm())
	})
}

func copyOneFile(src, dst string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Resolving a branch or tag to a commit is itself a network operation, so a
// cache of package bytes alone is not enough to make `--offline` useful: a user
// re-installing the same tag they installed yesterday would still be told to go
// and find its commit id by hand.
//
// Recording each resolution lets offline mode reuse the answer it saw last
// time. This is a genuine relaxation of the pinning rule and is treated as one:
// the resolution is only ever consulted when the network is unavailable, and
// the caller reports that the commit came from cache rather than from the
// remote. A tag that has since been moved will therefore resolve to what it
// meant when it was last seen — which is the honest meaning of offline, and far
// better than silently resolving to nothing.

func (c *Cache) revisionPath(url, rev string) string {
	h := sha256.Sum256([]byte(url + "\x00" + rev))
	return filepath.Join(c.root, "rev", hex.EncodeToString(h[:])[:32]+".json")
}

type revisionRecord struct {
	URL    string `json:"url"`
	Rev    string `json:"rev"`
	Commit string `json:"commit"`
}

// PutRevision records that a branch or tag resolved to a commit.
func (c *Cache) PutRevision(url, rev, commit string) error {
	if rev == "" || commit == "" {
		return nil
	}
	path := c.revisionPath(url, rev)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(revisionRecord{URL: url, Rev: rev, Commit: commit}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// LookupRevision returns the commit a branch or tag last resolved to, or the
// empty string if it has never been resolved on this machine.
func (c *Cache) LookupRevision(url, rev string) string {
	raw, err := os.ReadFile(c.revisionPath(url, rev))
	if err != nil {
		return ""
	}
	var rec revisionRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return ""
	}
	return rec.Commit
}
