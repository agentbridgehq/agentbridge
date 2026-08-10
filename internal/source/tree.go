package source

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TreeDigestPrefix labels the hash algorithm, matching the IR's convention.
const TreeDigestPrefix = "sha256:"

// TreeDigest computes a content address over every byte of a package.
//
// This is a different question from ir.Plugin.Digest, and both are needed. The
// IR digest asks "is this the same plugin?" — it is computed from the parsed,
// normalized model and deliberately ignores anything the model does not carry.
// The tree digest asks "are these the same bytes?", which is the question
// integrity verification actually turns on: a script under a skill's scripts/
// directory can be replaced without changing a single field the IR records, and
// that is precisely the tamper a supply chain has to catch.
//
// The digest covers, for every regular file in sorted path order: the
// slash-separated relative path, whether the file is executable, and the hash
// of its contents. Non-regular entries are excluded — symlinks are never
// followed or copied (see adapter.Apply), so including them would hash
// something we do not install.
//
// Executable bit but not full mode: it is the only permission bit that changes
// behavior, and it is the only one that survives a round trip through the
// archive formats and filesystems a plugin travels across.
func TreeDigest(root string) (string, error) {
	type entry struct {
		path string
		hash string
		exec bool
	}
	var entries []entry

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Version-control metadata is not part of the package: a plugin
			// fetched from git and the same plugin unpacked from an archive
			// describe the same artifact and must hash the same.
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := hashFile(p)
		if err != nil {
			return err
		}
		entries = append(entries, entry{
			path: filepath.ToSlash(rel),
			hash: h,
			exec: info.Mode().Perm()&0o111 != 0,
		})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", root, err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	// A length-prefixed, newline-delimited record per file. Length prefixing
	// keeps a path containing a newline from being able to forge a record
	// boundary and so produce a colliding digest.
	sum := sha256.New()
	var b strings.Builder
	for _, e := range entries {
		b.Reset()
		mode := "0644"
		if e.exec {
			mode = "0755"
		}
		fmt.Fprintf(&b, "%d %s %s %s\n", len(e.path), e.path, mode, e.hash)
		sum.Write([]byte(b.String()))
	}
	return TreeDigestPrefix + hex.EncodeToString(sum.Sum(nil)), nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyTreeDigest checks a package against an expected digest.
//
// The error names both digests, because the only useful response to a mismatch
// is to look at what changed, and a bare "verification failed" sends people
// straight to disabling the check.
func VerifyTreeDigest(root, expected string) error {
	if expected == "" {
		return nil
	}
	actual, err := TreeDigest(root)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("integrity check failed for %s:\n  expected %s\n  actual   %s\n"+
			"the package contents differ from what was recorded; do not install it until you know why", root, expected, actual)
	}
	return nil
}
