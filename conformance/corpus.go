// Package corpus embeds the Agent Plugins conformance cases.
//
// The corpus is a public artifact, not an internal fixture: its whole value is
// that a client vendor can check their own implementation against the same
// cases we check ours against. That is what makes a compatibility matrix
// something the ecosystem cites rather than something one vendor asserts.
//
// It lives in the binary because of who it is for. `agentbridge conformance`
// previously read `conformance/cases` relative to the working directory, which
// meant it worked in this repository and nowhere else — so the one command
// written specifically for people outside this project was unusable by exactly
// them. Embedding costs 220 KB and removes the precondition.
//
// This package is also importable, so a vendor writing Go can range over the
// cases directly rather than shelling out.
package corpus

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Files holds the corpus exactly as it appears in the repository.
//
// `all:` is required: several cases deliberately contain files beginning with a
// dot or an underscore, which the default embed rules would silently drop —
// and a conformance corpus missing the awkward cases is worse than none.
//
//go:embed all:cases
var Files embed.FS

// Root is the directory prefix inside Files.
const Root = "cases"

// MCPFiles holds the MCP configuration corpus.
//
// It is a separate set because it asks a different question. The cases above
// point a client at a plugin package; these deliver a server through the
// client's own configuration file, which is how servers actually reach every
// client measured so far — none of them reads a package's mcp.json. Keeping
// them apart means a result from one cannot be read as a result from the other.
//
// The probe directory is excluded: it is Go source for a helper binary, not a
// case, and embedding it would put it in the corpus digest.
//
//go:embed all:mcp/cases mcp/README.md
var MCPFiles embed.FS

// MCPRoot is the directory prefix inside MCPFiles.
const MCPRoot = "mcp/cases"

// Digest is the content address of the embedded corpus.
//
// Used to name the exported copy, so a build carrying a different corpus never
// reads a stale extraction left by an older one.
func Digest() (string, error) {
	h := sha256.New()

	var paths []string
	err := fs.WalkDir(Files, Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	// Sorted, so the digest is a property of the content rather than of walk
	// order.
	sort.Strings(paths)

	for _, p := range paths {
		raw, err := Files.ReadFile(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(raw))
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// Export writes the corpus to a directory under root and returns the path to
// the cases.
//
// The destination is named after the corpus digest and reused if already
// complete, so repeated runs do not rewrite 55 files, and a binary carrying a
// newer corpus lands somewhere else rather than reading yesterday's.
func Export(root string) (string, error) {
	digest, err := Digest()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "corpus", digest)
	cases := filepath.Join(dir, Root)

	// A marker written last, so an interrupted export is re-done rather than
	// read as complete — the same ordering the package cache uses.
	marker := filepath.Join(dir, ".complete")
	if _, err := os.Stat(marker); err == nil {
		return cases, nil
	}

	staging, err := os.MkdirTemp(root, ".corpus-*")
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", err
		}
		if staging, err = os.MkdirTemp(root, ".corpus-*"); err != nil {
			return "", err
		}
	}
	defer os.RemoveAll(staging)

	err = fs.WalkDir(Files, Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(staging, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := Files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		return "", err
	}

	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(staging, dir); err != nil {
		return "", err
	}
	if err := os.WriteFile(marker, []byte(digest+"\n"), 0o644); err != nil {
		return "", err
	}
	return cases, nil
}
