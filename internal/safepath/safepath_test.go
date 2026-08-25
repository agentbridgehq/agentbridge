package safepath_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// newTestRoot builds a plugin root with a file inside it and a sibling
// directory outside it, which is what an escape attempt aims at.
func newTestRoot(t *testing.T) (*safepath.Root, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "plugin")
	outside := filepath.Join(base, "outside")

	for _, d := range []string{root, filepath.Join(root, "skills", "a"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "skills", "a", "SKILL.md"), "inside")
	write(t, filepath.Join(outside, "secret.txt"), "outside")

	r, err := safepath.NewRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return r, base
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAcceptsContainedPaths(t *testing.T) {
	r, _ := newTestRoot(t)

	for _, rel := range []string{
		"skills/a/SKILL.md",
		"./skills/a/SKILL.md",
		"skills/../skills/a",     // interior .. that stays inside
		"does/not/exist/yet.txt", // must work before creation
	} {
		if _, err := r.Resolve(rel); err != nil {
			t.Errorf("Resolve(%q) = %v, want success", rel, err)
		}
	}
}

func TestResolveRejectsEscapes(t *testing.T) {
	r, _ := newTestRoot(t)

	for _, rel := range []string{
		"..",
		"../outside/secret.txt",
		"skills/../../outside/secret.txt",
		"./../../etc/passwd",
	} {
		_, err := r.Resolve(rel)
		if !errors.Is(err, safepath.ErrEscapes) {
			t.Errorf("Resolve(%q) = %v, want ErrEscapes", rel, err)
		}
	}
}

func TestResolveRejectsAbsolutePaths(t *testing.T) {
	r, _ := newTestRoot(t)

	paths := []string{"/etc/passwd"}
	// A manifest is a portable artifact: a Windows absolute path must be
	// rejected on every host, not silently treated as a relative path on Unix.
	paths = append(paths, `C:\Windows\System32`, `\\server\share`)

	for _, p := range paths {
		_, err := r.Resolve(p)
		if !errors.Is(err, safepath.ErrAbsolute) {
			t.Errorf("Resolve(%q) = %v, want ErrAbsolute", p, err)
		}
	}
}

// The specification says paths must stay within the *filesystem-resolved*
// plugin root. A path can be lexically clean and still escape through a
// symlink, which is why containment is checked after evaluation. This is
// threat T7.
func TestResolveRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	r, base := newTestRoot(t)

	link := filepath.Join(r.Path(), "escape")
	if err := os.Symlink(filepath.Join(base, "outside"), link); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Resolve("escape/secret.txt"); !errors.Is(err, safepath.ErrEscapes) {
		t.Errorf("symlinked path resolved to %v, want ErrEscapes", err)
	}
}

// A symlink that stays inside the root is legitimate; plugins use them to share
// files. Rejecting all symlinks would be simpler and wrong.
func TestResolveAllowsInternalSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	r, _ := newTestRoot(t)

	link := filepath.Join(r.Path(), "alias")
	if err := os.Symlink(filepath.Join(r.Path(), "skills"), link); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Resolve("alias/a/SKILL.md"); err != nil {
		t.Errorf("internal symlink rejected: %v", err)
	}
}

// A root that is itself reached through a symlink must still work. On macOS
// this is the default case for anything under /tmp, so getting it wrong breaks
// every test and every plugin installed in a temp directory.
func TestSymlinkedRootIsUsable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	r, err := safepath.NewRoot(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("skills"); err != nil {
		t.Errorf("Resolve through symlinked root: %v", err)
	}
}

func TestNewRootRejectsFilesAndMissingDirs(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "f")
	write(t, file, "x")

	if _, err := safepath.NewRoot(file); err == nil {
		t.Error("a file was accepted as a plugin root")
	}
	if _, err := safepath.NewRoot(filepath.Join(base, "nope")); err == nil {
		t.Error("a missing directory was accepted as a plugin root")
	}
	if _, err := safepath.NewRoot(""); !errors.Is(err, safepath.ErrEmpty) {
		t.Errorf("empty root = %v, want ErrEmpty", err)
	}
}

func TestContains(t *testing.T) {
	r, base := newTestRoot(t)

	if !r.Contains(filepath.Join(r.Path(), "skills")) {
		t.Error("Contains reported an inside path as outside")
	}
	if r.Contains(filepath.Join(base, "outside")) {
		t.Error("Contains reported an outside path as inside")
	}
}

// Fuzzing here is cheap and targets the property that actually matters: no
// input string, however malformed, may produce a path outside the root.
func FuzzResolveNeverEscapes(f *testing.F) {
	for _, seed := range []string{
		"a", "./a", "../a", "a/../../b", "..", "....//", "a/./b",
		`C:\x`, `\\s\h`, "a\x00b", "//x", "/a", "a//b", "./././..",
	} {
		f.Add(seed)
	}

	base := f.TempDir()
	root := filepath.Join(base, "plugin")
	if err := os.MkdirAll(root, 0o755); err != nil {
		f.Fatal(err)
	}
	r, err := safepath.NewRoot(root)
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, rel string) {
		got, err := r.Resolve(rel)
		if err != nil {
			return
		}
		if !r.Contains(got) {
			t.Fatalf("Resolve(%q) returned %q, which is outside %q", rel, got, r.Path())
		}
	})
}
