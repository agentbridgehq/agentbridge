package adapter

import (
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
