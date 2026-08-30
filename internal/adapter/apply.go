package adapter

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Apply executes a plan.
//
// Writes are atomic per file: content goes to a temporary file in the same
// directory and is renamed into place, so a crash or a full disk leaves the
// user's existing configuration intact rather than truncated. A partially
// written MCP config is worse than no change at all, because the client will
// silently fail to start and the user has no idea why.
//
// Operations that would change nothing are skipped, so re-running an install
// does not touch mtimes or trigger a client's file watcher.
func Apply(p *Plan) error {
	for _, op := range p.Ops {
		if op.Unchanged() {
			continue
		}
		if err := applyOp(op); err != nil {
			return fmt.Errorf("%s: %w", op.Path, err)
		}
	}
	return nil
}

func applyOp(op Op) error {
	switch op.Kind {
	case OpWriteFile:
		return writeFileAtomic(op.Path, op.After)
	case OpRemoveFile:
		if err := os.Remove(op.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case OpCopyTree:
		if err := os.RemoveAll(op.Path); err != nil {
			return err
		}
		return copyTree(op.SourceDir, op.Path)
	case OpRemoveTree:
		if err := os.RemoveAll(op.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q", op.Kind)
	}
}

func writeFileAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Preserve the existing file's mode. Some clients ship configuration at
	// 0600 and silently widening it would be a real, if quiet, regression.
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".agentbridge-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	// Flush to disk before the rename, so a crash cannot leave a renamed but
	// empty file where the user's config used to be.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// Symlinks are not followed. A plugin that ships one could otherwise
		// place a link pointing anywhere on the machine into a client's
		// configuration directory, which is an escape by another route.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
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

// CopyTreeOp builds a copy operation, having first checked whether the target
// already holds exactly what the copy would produce.
//
// Without that check a re-install always reports a change and always rewrites
// the tree, which churns mtimes and wakes every client's file watcher for
// nothing. `install` twice in a row said "ok" rather than "already up to date"
// for every package-based client, and had done since the first adapter: the
// idempotency test only ever ran against a client whose install was a config
// edit, so no copy was ever in the plan it examined.
// alsoWritten names paths, relative to target, that the plan writes after the
// copy — a client's own manifest, say. They are expected to be in the target
// and absent from the source, so they are excluded from the comparison rather
// than counted as drift.
func CopyTreeOp(target, sourceDir, note string, alsoWritten ...string) Op {
	return Op{
		Kind:      OpCopyTree,
		Path:      target,
		SourceDir: sourceDir,
		Note:      note,
		Identical: treesMatch(sourceDir, target, alsoWritten),
	}
}

// treesMatch reports whether two directories hold the same relative paths with
// the same contents. It answers false on any error: a copy that did not need to
// happen is wasteful, while one that was skipped when it was needed is a bug.
func treesMatch(source, target string, alsoWritten []string) bool {
	fs, err := treeFiles(source)
	if err != nil {
		return false
	}
	ft, err := treeFiles(target)
	if err != nil {
		return false
	}
	for _, rel := range alsoWritten {
		delete(ft, filepath.ToSlash(rel))
	}
	// Counted both ways on purpose. Checking only that every source file is
	// present would leave a file deleted upstream sitting in the target
	// forever, because the copy that would have removed it was skipped.
	if len(fs) != len(ft) {
		return false
	}
	for rel, sum := range fs {
		if got, ok := ft[rel]; !ok || got != sum {
			return false
		}
	}
	return true
}

func treeFiles(root string) (map[string][32]byte, error) {
	out := map[string][32]byte{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skipped for the same reason the packer skips them: version
			// control metadata is not part of what a client loads.
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = sha256.Sum256(raw)
		return nil
	})
	return out, err
}

// ExistingFile returns a file's current contents, or nil when it is absent.
//
// It is what a write operation should use for Before. An op with a nil Before
// can never compare equal to its After, so a manifest written beside a copied
// package made every re-install report a change even when the copy itself was
// identical — the plan was honest about the file it had never read, and wrong
// about the outcome.
func ExistingFile(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return raw
}
