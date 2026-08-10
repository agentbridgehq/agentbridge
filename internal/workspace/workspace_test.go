package workspace_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	"github.com/agentbridge/agentbridge/internal/lockfile"
	"github.com/agentbridge/agentbridge/internal/workspace"
)

// fakeMachine builds a home directory a client will be detected in.
func fakeMachine(t *testing.T) adapter.Env {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	return adapter.Env{HomeDir: home, GOOS: "darwin"}
}

// pluginRepo builds a git repository containing a plugin, so the pinning path
// is exercised without network access.
func pluginRepo(t *testing.T, name, version string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()
	write(t, dir, "plugin.json", `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "`+name+`",
  "version": "`+version+`"
}`)
	write(t, dir, "skills/query/SKILL.md", "---\nname: query\ndescription: d\n---\nbody\n")
	write(t, dir, "mcp.json", `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": { "db": { "type": "stdio", "command": "npx", "args": ["@acme/db"] } }
}`)

	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "initial")
	gitRun(t, dir, "tag", "v1.0.0")
	gitRun(t, dir, "config", "uploadpack.allowAnySHA1InWant", "true")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// declare writes a project manifest and returns the resolution for it.
func declare(t *testing.T, env adapter.Env, projectDir string, sources ...string) lockfile.Resolution {
	t.Helper()
	ws := lockfile.ProjectWorkspace(projectDir)

	m, err := lockfile.LoadManifest(ws.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	m.Plugins = nil
	for _, s := range sources {
		m.Add(lockfile.Entry{Source: s})
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}

	user := lockfile.UserWorkspace(adapterreg.StateDir(env))
	userManifest, err := lockfile.LoadManifest(user.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	return lockfile.Merge(userManifest, m, user, ws)
}

func sync(t *testing.T, env adapter.Env, res lockfile.Resolution, opts workspace.Options) *workspace.Result {
	t.Helper()
	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	opts.Env = env

	result, err := workspace.Sync(context.Background(), res, store, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range result.Plugins {
		if p.Err != nil {
			t.Fatalf("%s: %v", p.Entry.Source, p.Err)
		}
	}
	return result
}

func TestSyncInstallsAndWritesLock(t *testing.T) {
	env := fakeMachine(t)
	repo := pluginRepo(t, "acme.db", "1.0.0")
	project := t.TempDir()

	res := declare(t, env, project, "file://"+repo+"@v1.0.0")
	result := sync(t, env, res, workspace.Options{Prune: true})

	if len(result.Plugins) != 1 {
		t.Fatalf("plugins = %d", len(result.Plugins))
	}

	lock, err := lockfile.LoadLock(lockfile.ProjectWorkspace(project).LockPath())
	if err != nil {
		t.Fatal(err)
	}
	locked, ok := lock.FindByName("acme.db")
	if !ok {
		t.Fatalf("plugin not recorded in the lock: %v", lock.Names())
	}

	// A tag is mutable; the lock must record the commit it pointed at.
	if !strings.Contains(locked.Resolved, "@") || strings.HasSuffix(locked.Resolved, "@v1.0.0") {
		t.Errorf("resolved = %q, want the commit rather than the tag", locked.Resolved)
	}
	if locked.TreeDigest == "" || locked.IRDigest == "" {
		t.Error("both digests should be recorded; they answer different questions")
	}
	// The capability line is the point of the lock for a reviewer.
	if len(locked.Capabilities) == 0 {
		t.Error("no capabilities recorded")
	}
	if len(locked.Skills) == 0 || len(locked.Servers) == 0 {
		t.Errorf("contents not recorded: skills=%v servers=%v", locked.Skills, locked.Servers)
	}
}

// Convergence means running twice changes nothing the second time.
func TestSyncIsIdempotent(t *testing.T) {
	env := fakeMachine(t)
	repo := pluginRepo(t, "acme.db", "1.0.0")
	project := t.TempDir()

	res := declare(t, env, project, "file://"+repo+"@v1.0.0")
	sync(t, env, res, workspace.Options{Prune: true})

	second := sync(t, env, res, workspace.Options{Prune: true})
	if !second.Diff.Empty() {
		t.Errorf("second sync changed the lock: %+v", second.Diff)
	}
	for _, p := range second.Plugins {
		for _, plan := range p.Plans {
			if plan.Changed() {
				t.Errorf("second sync would rewrite %s", plan.Ops[0].Path)
			}
		}
	}
}

// The pin is what makes a lock worth having: after the tag moves, a plain sync
// must still install the commit that was recorded.
func TestSyncHonoursThePin(t *testing.T) {
	env := fakeMachine(t)
	repo := pluginRepo(t, "acme.db", "1.0.0")
	project := t.TempDir()

	res := declare(t, env, project, "file://"+repo+"@v1.0.0")
	first := sync(t, env, res, workspace.Options{Prune: true})
	pinned := first.Plugins[0].Resolved.Commit

	// Move the tag to a new commit.
	write(t, repo, "skills/extra/SKILL.md", "---\nname: extra\ndescription: d\n---\nmore\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "second")
	gitRun(t, repo, "tag", "-f", "v1.0.0")

	again := sync(t, env, res, workspace.Options{Prune: true})
	if got := again.Plugins[0].Resolved.Commit; got != pinned {
		t.Errorf("sync followed the moved tag: commit = %s, want the pinned %s", got, pinned)
	}

	// update is the command that deliberately re-resolves.
	updated := sync(t, env, res, workspace.Options{Prune: false, Update: true})
	if got := updated.Plugins[0].Resolved.Commit; got == pinned {
		t.Error("update did not re-resolve the tag")
	}
	if len(updated.Diff.Changed) != 1 {
		t.Fatalf("expected one changed plugin, got %+v", updated.Diff)
	}
	// The reviewable part: a new skill appeared.
	change := updated.Diff.Changed[0]
	if len(change.After.Skills) <= len(change.Before.Skills) {
		t.Errorf("the added skill is not visible in the lock diff: %v -> %v",
			change.Before.Skills, change.After.Skills)
	}
}

// A sync that deletes work a developer did by hand is a tool nobody runs twice.
func TestPruneRemovesOnlyManagedPlugins(t *testing.T) {
	env := fakeMachine(t)
	managed := pluginRepo(t, "acme.managed", "1.0.0")
	adhoc := pluginRepo(t, "acme.adhoc", "1.0.0")
	project := t.TempDir()

	// One declared plugin, and one installed by hand.
	res := declare(t, env, project, "file://"+managed+"@v1.0.0")
	sync(t, env, res, workspace.Options{Prune: true})

	adhocRes := lockfile.Resolution{Entries: []lockfile.ScopedEntry{{
		Entry:     lockfile.Entry{Source: "file://" + adhoc + "@v1.0.0"},
		Workspace: lockfile.ProjectWorkspace(t.TempDir()),
	}}}
	// The store is re-opened rather than reused: it is a whole-file document,
	// so an instance loaded before the previous sync would write back a
	// snapshot that predates it and silently drop the receipt just recorded.
	adhocStore, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	// DeclaredIn is empty, which is what an ad-hoc install looks like.
	if _, err := workspace.Sync(context.Background(), adhocRes, adhocStore, workspace.Options{Env: env}); err != nil {
		t.Fatal(err)
	}

	// Now undeclare the managed one.
	empty := declare(t, env, project)
	result := sync(t, env, empty, workspace.Options{Prune: true})

	if len(result.Pruned) != 1 || result.Pruned[0] != "acme.managed" {
		t.Errorf("pruned = %v, want only the declared-then-undeclared plugin", result.Pruned)
	}

	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.ForPlugin("acme.adhoc")) == 0 {
		t.Error("prune removed a plugin the user installed by hand")
	}
	if len(store.ForPlugin("acme.managed")) != 0 {
		t.Error("prune left the undeclared plugin installed")
	}
}

// A dry run must compute the whole answer and touch nothing.
func TestDryRunWritesNothing(t *testing.T) {
	env := fakeMachine(t)
	repo := pluginRepo(t, "acme.db", "1.0.0")
	project := t.TempDir()

	res := declare(t, env, project, "file://"+repo+"@v1.0.0")
	result := sync(t, env, res, workspace.Options{DryRun: true, Prune: true})

	if len(result.Diff.Added) != 1 {
		t.Errorf("dry run should still report what would change: %+v", result.Diff)
	}
	if _, err := os.Stat(lockfile.ProjectWorkspace(project).LockPath()); !os.IsNotExist(err) {
		t.Error("dry run wrote the lock")
	}
	if _, err := os.Stat(filepath.Join(env.HomeDir, ".cursor", "mcp.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote a client config")
	}
}

// One unreachable plugin must not stop the others, for the same reason the
// specification isolates component failures.
func TestOnePluginFailureDoesNotStopTheRest(t *testing.T) {
	env := fakeMachine(t)
	good := pluginRepo(t, "acme.good", "1.0.0")
	project := t.TempDir()

	res := declare(t, env, project,
		"file:///nonexistent/repo@v1.0.0",
		"file://"+good+"@v1.0.0")

	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	result, err := workspace.Sync(context.Background(), res, store, workspace.Options{Env: env})
	if err != nil {
		t.Fatal(err)
	}

	if !result.Failed() {
		t.Error("the unreachable plugin should be reported as failed")
	}
	var installed bool
	for _, p := range result.Plugins {
		if p.Err == nil && p.Plugin.Name == "acme.good" {
			installed = true
		}
	}
	if !installed {
		t.Error("the reachable plugin was not installed")
	}
}

// Project declarations win over user ones, and the same source is installed
// once rather than twice.
func TestProjectScopeWinsOverUser(t *testing.T) {
	env := fakeMachine(t)
	repo := pluginRepo(t, "acme.db", "1.0.0")
	project := t.TempDir()
	ref := "file://" + repo + "@v1.0.0"

	userWS := lockfile.UserWorkspace(adapterreg.StateDir(env))
	userManifest, err := lockfile.LoadManifest(userWS.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	userManifest.Add(lockfile.Entry{Source: ref, Clients: []string{"gemini-cli"}})
	if err := userManifest.Save(); err != nil {
		t.Fatal(err)
	}

	projectWS := lockfile.ProjectWorkspace(project)
	projectManifest, err := lockfile.LoadManifest(projectWS.ManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	projectManifest.Add(lockfile.Entry{Source: ref, Clients: []string{"cursor"}})
	if err := projectManifest.Save(); err != nil {
		t.Fatal(err)
	}

	res := lockfile.Merge(userManifest, projectManifest, userWS, projectWS)
	if len(res.Entries) != 1 {
		t.Fatalf("entries = %d, want the same source declared once", len(res.Entries))
	}
	if res.Entries[0].DeclaredIn != lockfile.ScopeProject {
		t.Errorf("declaredIn = %q, want project to win", res.Entries[0].DeclaredIn)
	}
	if res.Entries[0].Clients[0] != "cursor" {
		t.Errorf("clients = %v, want the project's declaration", res.Entries[0].Clients)
	}
}

// Removing a line from the manifest must remove it from the lock too: the lock
// follows the manifest, never the other way round.
func TestUndeclaringRemovesFromLock(t *testing.T) {
	env := fakeMachine(t)
	repo := pluginRepo(t, "acme.db", "1.0.0")
	project := t.TempDir()

	res := declare(t, env, project, "file://"+repo+"@v1.0.0")
	sync(t, env, res, workspace.Options{Prune: true})

	empty := declare(t, env, project)
	sync(t, env, empty, workspace.Options{Prune: true})

	lock, err := lockfile.LoadLock(lockfile.ProjectWorkspace(project).LockPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Plugins) != 0 {
		t.Errorf("lock still lists %v", lock.Names())
	}
}
