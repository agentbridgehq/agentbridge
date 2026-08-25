// Package safepath enforces the package boundary the Agent Plugins
// specification requires: "all resolved paths remain within the
// filesystem-resolved plugin root."
//
// Every path that originates from a manifest — a skill directory, an MCP
// server's cwd, a plugin-relative command — must pass through this package
// before it is opened. Threat T7 in docs/05-security-and-trust.md is the escape
// case, and "filesystem-resolved" is the operative phrase: a path can be
// syntactically clean and still escape through a symlink, so containment is
// checked after symlink evaluation, not before.
package safepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrAbsolute is returned for an absolute path where a plugin-relative one
	// is required.
	ErrAbsolute = errors.New("absolute path not allowed")
	// ErrEscapes is returned when a path resolves outside the plugin root.
	ErrEscapes = errors.New("path escapes plugin root")
	// ErrEmpty is returned for an empty path.
	ErrEmpty = errors.New("empty path")
)

// Root is a validated plugin root. Construct it with NewRoot so the root itself
// is symlink-resolved once, up front: every later containment check compares
// against the resolved form, which is what makes symlinked roots (a common
// layout on macOS, where /tmp is a symlink to /private/tmp) work correctly.
type Root struct {
	// declared is the path as given by the caller.
	declared string
	// resolved is the fully symlink-evaluated, absolute root.
	resolved string
}

// NewRoot validates and resolves a plugin root directory.
func NewRoot(dir string) (*Root, error) {
	if dir == "" {
		return nil, ErrEmpty
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", dir, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", dir)
	}
	return &Root{declared: abs, resolved: resolved}, nil
}

// Path returns the resolved root directory.
func (r *Root) Path() string { return r.resolved }

// Declared returns the root as the caller supplied it (absolute, but not
// symlink-evaluated). Use it for user-facing messages, where the resolved form
// would be confusing.
func (r *Root) Declared() string { return r.declared }

// Resolve validates a plugin-relative path and returns its absolute location
// inside the root.
//
// It rejects absolute paths and any path that escapes the root, whether
// lexically (via "..") or through a symlink. A leading "./" is accepted because
// the specification uses that form for plugin-relative values.
//
// The path need not exist: for a path whose parent exists but whose leaf does
// not, containment is checked against the resolved parent. That matters for
// paths we are about to create, such as PLUGIN_DATA subdirectories.
func (r *Root) Resolve(rel string) (string, error) {
	if rel == "" {
		return "", ErrEmpty
	}
	if filepath.IsAbs(rel) || isWindowsAbs(rel) || isRooted(rel) {
		return "", fmt.Errorf("%w: %q", ErrAbsolute, rel)
	}

	clean := filepath.Clean(filepath.FromSlash(rel))
	// Clean turns "./x" into "x" and collapses interior "..", but a path that
	// climbs above the root still starts with "..".
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrEscapes, rel)
	}

	joined := filepath.Join(r.resolved, clean)

	// Lexical containment is necessary but not sufficient; a component of the
	// path may be a symlink pointing elsewhere.
	resolved, err := resolveExisting(joined)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", rel, err)
	}
	if !r.contains(resolved) {
		return "", fmt.Errorf("%w: %q resolves to %q", ErrEscapes, rel, resolved)
	}
	return joined, nil
}

// Contains reports whether an absolute path lies within the root after symlink
// evaluation.
func (r *Root) Contains(abs string) bool {
	resolved, err := resolveExisting(abs)
	if err != nil {
		return false
	}
	return r.contains(resolved)
}

func (r *Root) contains(resolved string) bool {
	if resolved == r.resolved {
		return true
	}
	rel, err := filepath.Rel(r.resolved, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting evaluates symlinks for the longest existing prefix of path,
// then re-appends the non-existent remainder. filepath.EvalSymlinks fails
// outright on a non-existent path, which would make it impossible to check a
// path before creating it.
func resolveExisting(path string) (string, error) {
	remainder := ""
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if remainder == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, remainder), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the filesystem root without finding anything that
			// exists. Nothing to resolve; the lexical form is the answer.
			return path, nil
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}

// isRooted catches a path anchored at a filesystem root on *any* platform.
//
// The mirror of isWindowsAbs, and it was missing. filepath.IsAbs on Windows
// answers false for "/etc/passwd" — it is rooted but has no drive — so a
// manifest authored on Linux carrying a Unix absolute path was rejected on
// Linux and quietly accepted as relative on Windows. The rule this package
// exists to enforce is that component paths are plugin-relative, and that
// cannot depend on which machine happens to be reading the manifest.
func isRooted(p string) bool {
	return len(p) > 0 && (p[0] == '/' || p[0] == '\\')
}

// isWindowsAbs catches Windows-style absolute paths ("C:\x", "\\server\share")
// even when running on a non-Windows host. A manifest is a portable artifact
// and may have been authored anywhere, so a Linux CI run must still reject a
// Windows absolute path rather than silently treating it as relative.
func isWindowsAbs(p string) bool {
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `\`)
}
