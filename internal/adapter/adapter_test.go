package adapter_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/ir"
)

func TestMaterializeResolvesPlaceholders(t *testing.T) {
	root := filepath.Join("/plugins", "acme")
	data := filepath.Join("/state", "data", "acme")

	got := adapter.Materialize(ir.MCPServer{
		Name:      "db",
		Transport: ir.TransportStdio,
		Command:   "./bin/server",
		Args:      []string{"--config", "${PLUGIN_ROOT}/config.json"},
		Env:       map[string]string{"CACHE": "${PLUGIN_DATA}/cache"},
		Cwd:       "${PLUGIN_ROOT}/work",
	}, root, data)

	if want := filepath.Join(root, "bin", "server"); got.Command != want {
		t.Errorf("command = %q, want %q", got.Command, want)
	}
	if !strings.Contains(got.Args[1], root) || strings.Contains(got.Args[1], "${") {
		t.Errorf("args = %v", got.Args)
	}
	if !strings.Contains(got.Env["CACHE"], data) {
		t.Errorf("env = %v", got.Env)
	}
	if strings.Contains(got.Cwd, "${") {
		t.Errorf("cwd = %q", got.Cwd)
	}
}

// A bare command is resolved by the platform's executable search. Turning it
// into a path would break every server launched via npx or uvx.
func TestMaterializeLeavesBareCommandsAlone(t *testing.T) {
	got := adapter.Materialize(ir.MCPServer{
		Transport: ir.TransportStdio,
		Command:   "npx",
	}, "/plugins/acme", "/state/acme")

	if got.Command != "npx" {
		t.Errorf("command = %q, want npx", got.Command)
	}
}

// Materialize must not write through to the caller's server, or the same
// plugin installed into a second client would see the first client's resolved
// paths.
func TestMaterializeDoesNotMutateInput(t *testing.T) {
	in := ir.MCPServer{
		Transport: ir.TransportStdio,
		Command:   "./bin/x",
		Args:      []string{"${PLUGIN_ROOT}/a"},
		Env:       map[string]string{"K": "${PLUGIN_DATA}/b"},
	}
	adapter.Materialize(in, "/root", "/data")

	if in.Command != "./bin/x" || in.Args[0] != "${PLUGIN_ROOT}/a" || in.Env["K"] != "${PLUGIN_DATA}/b" {
		t.Errorf("input was mutated: %+v", in)
	}
}

func TestNoteSecretsWritten(t *testing.T) {
	var f adapter.Fidelity
	adapter.NoteSecretsWritten(&f, "db", map[string]string{
		"DB_API_TOKEN": "literal",
		"LOG_LEVEL":    "debug",
	}, "/home/u/.cursor/mcp.json")

	if len(f.Losses) != 1 {
		t.Fatalf("losses = %+v, want exactly the token", f.Losses)
	}
	if f.Losses[0].Code != adapter.LossSecretInPlaintext {
		t.Errorf("code = %q", f.Losses[0].Code)
	}
}

// A crash mid-write must leave the user's existing config intact rather than
// truncated, so writes go through a temp file and a rename.
func TestApplyWritesAtomicallyAndPreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := &adapter.Plan{Ops: []adapter.Op{{
		Kind:   adapter.OpWriteFile,
		Path:   path,
		Before: []byte("old\n"),
		After:  []byte("new\n"),
	}}}
	if err := adapter.Apply(plan); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new\n" {
		t.Errorf("content = %q", raw)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		// Some clients ship config at 0600; silently widening it would be a
		// quiet security regression.
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600 preserved", info.Mode().Perm())
		}
	}

	// No temp files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".agentbridge-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestApplySkipsUnchangedOps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("same\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	op := adapter.Op{Kind: adapter.OpWriteFile, Path: path, Before: []byte("same\n"), After: []byte("same\n")}
	if !op.Unchanged() {
		t.Fatal("identical content should report as unchanged")
	}
	if err := adapter.Apply(&adapter.Plan{Ops: []adapter.Op{op}}); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unchanged file was rewritten, which wakes every client's file watcher")
	}
}

// A plugin that ships a symlink could otherwise place a link pointing anywhere
// on the machine inside a client's configuration directory.
func TestCopyTreeSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	base := t.TempDir()
	src := filepath.Join(base, "src")
	secret := filepath.Join(base, "secret.txt")

	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(base, "dst")
	if err := adapter.Apply(&adapter.Plan{Ops: []adapter.Op{{
		Kind: adapter.OpCopyTree, Path: dst, SourceDir: src,
	}}}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dst, "real.txt")); err != nil {
		t.Errorf("regular file not copied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "link.txt")); err == nil {
		t.Error("symlink was copied into the client's directory")
	}
}

func TestDiffRendersChanges(t *testing.T) {
	d := adapter.Diff(adapter.Op{
		Kind:   adapter.OpWriteFile,
		Path:   "/x/mcp.json",
		Before: []byte("a\nb\nc\n"),
		After:  []byte("a\nB\nc\n"),
	})

	if !strings.Contains(d, "-b") || !strings.Contains(d, "+B") {
		t.Errorf("diff did not show the change:\n%s", d)
	}
	if !strings.Contains(d, "@@") {
		t.Errorf("diff has no hunk header:\n%s", d)
	}
	// Unchanged context should not be reported as edits.
	if strings.Contains(d, "-a") || strings.Contains(d, "+c") {
		t.Errorf("diff reported unchanged lines as edits:\n%s", d)
	}
}

func TestDiffOfNewFile(t *testing.T) {
	d := adapter.Diff(adapter.Op{Kind: adapter.OpWriteFile, Path: "/x/new.json", After: []byte("hello\n")})

	if !strings.Contains(d, "/dev/null") {
		t.Errorf("a new file should diff against /dev/null:\n%s", d)
	}
	if !strings.Contains(d, "+hello") {
		t.Errorf("diff:\n%s", d)
	}
}

func TestCoverageAndFidelity(t *testing.T) {
	f := adapter.Fidelity{
		Skills:     adapter.Coverage{Carried: 3, Total: 3},
		MCPServers: adapter.Coverage{Carried: 1, Total: 2},
	}
	if f.Skills.String() != "3/3" || !f.Skills.Complete() {
		t.Errorf("skills = %s", f.Skills)
	}
	if !f.Degraded() {
		t.Error("incomplete MCP coverage must count as degraded")
	}

	full := adapter.Fidelity{
		Skills:     adapter.Coverage{Carried: 1, Total: 1},
		MCPServers: adapter.Coverage{Carried: 1, Total: 1},
	}
	if full.Degraded() {
		t.Error("full coverage with no losses must not be degraded")
	}
}

func TestManagedKeyNamespacesByPlugin(t *testing.T) {
	if got := adapter.ManagedKey("acme.db-tools", "db"); got != "acme.db-tools.db" {
		t.Errorf("ManagedKey = %q", got)
	}
}
