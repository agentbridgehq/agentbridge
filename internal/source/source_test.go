package source_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/source"
)

// ------------------------------------------------------------ reference parsing

func TestParseRef(t *testing.T) {
	cases := []struct {
		raw    string
		kind   source.Kind
		url    string
		rev    string
		subdir string
	}{
		{raw: "./plugins/db", kind: source.KindLocal},
		{raw: "../sibling", kind: source.KindLocal},
		{raw: "/abs/path", kind: source.KindLocal},
		{raw: ".", kind: source.KindLocal},

		{raw: "https://github.com/org/repo", kind: source.KindGit, url: "https://github.com/org/repo"},
		{raw: "https://github.com/org/repo@v1.2.0", kind: source.KindGit, url: "https://github.com/org/repo", rev: "v1.2.0"},
		{raw: "https://github.com/org/repo@main#plugins/db", kind: source.KindGit, url: "https://github.com/org/repo", rev: "main", subdir: "plugins/db"},
		{raw: "https://github.com/org/repo#plugins/db", kind: source.KindGit, url: "https://github.com/org/repo", subdir: "plugins/db"},

		// Host-qualified shorthand gains a scheme.
		{raw: "github.com/org/repo", kind: source.KindGit, url: "https://github.com/org/repo"},
		{raw: "gitlab.example.com/team/repo@v2", kind: source.KindGit, url: "https://gitlab.example.com/team/repo", rev: "v2"},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			ref, err := source.ParseRef(tc.raw)
			if err != nil {
				t.Fatalf("ParseRef(%q) = %v", tc.raw, err)
			}
			if ref.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", ref.Kind, tc.kind)
			}
			if ref.URL != tc.url {
				t.Errorf("url = %q, want %q", ref.URL, tc.url)
			}
			if ref.Rev != tc.rev {
				t.Errorf("rev = %q, want %q", ref.Rev, tc.rev)
			}
			if ref.Subdir != tc.subdir {
				t.Errorf("subdir = %q, want %q", ref.Subdir, tc.subdir)
			}
		})
	}
}

// An SCP-style remote contains an "@" that is not a revision separator. Reading
// it as one would silently fetch the wrong thing, or nothing.
func TestParseRefSCPStyle(t *testing.T) {
	ref, err := source.ParseRef("git@github.com:org/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	if ref.URL != "git@github.com:org/repo.git" || ref.Rev != "" {
		t.Errorf("url=%q rev=%q, want the whole thing as the url", ref.URL, ref.Rev)
	}

	withRev, err := source.ParseRef("git@github.com:org/repo.git@v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if withRev.URL != "git@github.com:org/repo.git" || withRev.Rev != "v1.0.0" {
		t.Errorf("url=%q rev=%q", withRev.URL, withRev.Rev)
	}
}

// A bare org/repo is an ordinary relative path. Turning it into a network fetch
// because it looks like a GitHub shorthand would quietly install someone else's
// code when the user meant their own directory.
func TestParseRefDoesNotGuessGitHubShorthand(t *testing.T) {
	if _, err := source.ParseRef("org/repo"); err == nil {
		t.Error("a bare org/repo should be rejected rather than assumed to be GitHub")
	}

	// ...and when such a directory exists, it is local.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "org", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	ref, err := source.ParseRef("org/repo")
	if err != nil {
		t.Fatalf("an existing directory should parse as local: %v", err)
	}
	if ref.Kind != source.KindLocal {
		t.Errorf("kind = %q, want local", ref.Kind)
	}
}

func TestParseRefRejectsDangerousInput(t *testing.T) {
	for _, raw := range []string{
		"",
		"--upload-pack=evil",
		"-oProxyCommand=evil",
		"https://github.com/org/repo#../../etc",
		"https://github.com/org/repo#/etc/passwd",
	} {
		if _, err := source.ParseRef(raw); err == nil {
			t.Errorf("ParseRef(%q) should have been rejected", raw)
		}
	}
}

// ------------------------------------------------------------ tree digest

func TestTreeDigestIsContentSensitive(t *testing.T) {
	a := writeTree(t, map[string]string{"plugin.json": `{"name":"x"}`, "skills/s/SKILL.md": "body"})
	b := writeTree(t, map[string]string{"plugin.json": `{"name":"x"}`, "skills/s/SKILL.md": "body"})

	da := digest(t, a)
	if da != digest(t, b) {
		t.Error("identical trees hashed differently")
	}

	// A change to a file the IR does not model at all must still change the
	// digest — that is the whole reason this exists alongside the IR digest.
	if err := os.WriteFile(filepath.Join(b, "skills", "s", "helper.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if digest(t, b) == da {
		t.Error("adding a file did not change the digest")
	}
}

func TestTreeDigestCoversExecutableBit(t *testing.T) {
	dir := writeTree(t, map[string]string{"bin/run": "#!/bin/sh\n"})
	before := digest(t, dir)

	if err := os.Chmod(filepath.Join(dir, "bin", "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if digest(t, dir) == before {
		t.Error("making a file executable did not change the digest; that is a behavior change")
	}
}

// A plugin fetched from git and the same plugin unpacked from an archive are
// the same artifact and must hash the same.
func TestTreeDigestIgnoresGitMetadata(t *testing.T) {
	dir := writeTree(t, map[string]string{"plugin.json": "{}"})
	before := digest(t, dir)

	if err := os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if digest(t, dir) != before {
		t.Error("git metadata contributed to the digest")
	}
}

func TestVerifyTreeDigest(t *testing.T) {
	dir := writeTree(t, map[string]string{"plugin.json": "{}"})
	d := digest(t, dir)

	if err := source.VerifyTreeDigest(dir, d); err != nil {
		t.Errorf("matching digest reported as a failure: %v", err)
	}
	if err := source.VerifyTreeDigest(dir, ""); err != nil {
		t.Errorf("an absent expectation should verify trivially: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := source.VerifyTreeDigest(dir, d)
	if err == nil {
		t.Fatal("tampering was not detected")
	}
	// The message must name both digests: "verification failed" with no detail
	// sends people straight to disabling the check.
	if !strings.Contains(err.Error(), "expected") || !strings.Contains(err.Error(), "actual") {
		t.Errorf("unhelpful error: %v", err)
	}
}

// ------------------------------------------------------------ local resolution

func TestResolveLocal(t *testing.T) {
	dir := writeTree(t, map[string]string{"plugin.json": "{}"})

	got, err := source.ResolveString(context.Background(), dir, source.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != dir {
		t.Errorf("dir = %q, want the directory itself — a local plugin under development must not be served from a cache", got.Dir)
	}
	if got.TreeDigest == "" {
		t.Error("no digest computed")
	}
}

func TestResolveLocalHonoursExpectedDigest(t *testing.T) {
	dir := writeTree(t, map[string]string{"plugin.json": "{}"})

	if _, err := source.ResolveString(context.Background(), dir, source.Options{
		ExpectedDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}); err == nil {
		t.Error("a mismatched expected digest must fail the resolve")
	}
}

// ------------------------------------------------------------ git resolution

// gitRepo builds a real repository on disk so the git path is exercised without
// network access.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := writeTree(t, files)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-q", "-m", "initial")
	run("tag", "v1.0.0")
	// Fetching an arbitrary commit by id needs the server to advertise it.
	run("config", "uploadpack.allowAnySHA1InWant", "true")
	return dir
}

func TestResolveGitPinsToCommit(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"plugin.json":       `{"name":"git-plugin"}`,
		"skills/s/SKILL.md": "---\nname: s\n---\nbody\n",
	})
	cache := source.NewCache(t.TempDir())

	got, err := source.ResolveString(context.Background(), "file://"+repo+"@v1.0.0", source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Commit) != 40 {
		t.Errorf("commit = %q, want a full object id", got.Commit)
	}
	if got.FromCache {
		t.Error("first fetch should not report as cached")
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "plugin.json")); err != nil {
		t.Errorf("package not materialized: %v", err)
	}
	// A tag is mutable; the pinned form must name the commit instead.
	if !strings.Contains(got.Pinned(), got.Commit) {
		t.Errorf("Pinned() = %q, want it to carry the commit", got.Pinned())
	}
	// The checkout's own history must not travel into the package.
	if _, err := os.Stat(filepath.Join(got.Dir, ".git")); !os.IsNotExist(err) {
		t.Error(".git was copied into the package")
	}
}

func TestResolveGitUsesCacheAndWorksOffline(t *testing.T) {
	repo := gitRepo(t, map[string]string{"plugin.json": `{"name":"git-plugin"}`})
	cache := source.NewCache(t.TempDir())
	ref := "file://" + repo + "@v1.0.0"

	first, err := source.ResolveString(context.Background(), ref, source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}

	second, err := source.ResolveString(context.Background(), ref, source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if !second.FromCache {
		t.Error("a second resolve of the same pin should come from the cache")
	}
	if second.TreeDigest != first.TreeDigest {
		t.Error("cached copy hashed differently")
	}

	// Once pinned to a commit, resolution needs no network at all.
	offline, err := source.ResolveString(context.Background(),
		"file://"+repo+"@"+first.Commit, source.Options{Cache: cache, Offline: true})
	if err != nil {
		t.Fatalf("offline resolve of a pinned reference failed: %v", err)
	}
	if !offline.FromCache {
		t.Error("offline resolve should have come from the cache")
	}
}

// Offline mode must fail rather than quietly reach out, and the message has to
// say which of the two problems it hit.
func TestOfflineRefusesWhatItHasNeverSeen(t *testing.T) {
	repo := gitRepo(t, map[string]string{"plugin.json": "{}"})
	cache := source.NewCache(t.TempDir())

	_, err := source.ResolveString(context.Background(), "file://"+repo+"@v1.0.0",
		source.Options{Cache: cache, Offline: true})
	if err == nil || !strings.Contains(err.Error(), "never been resolved") {
		t.Errorf("a tag never resolved on this machine: %v", err)
	}

	_, err = source.ResolveString(context.Background(),
		"file://"+repo+"@0000000000000000000000000000000000000000",
		source.Options{Cache: cache, Offline: true})
	if err == nil || !strings.Contains(err.Error(), "not in the cache") {
		t.Errorf("a pinned commit that was never fetched: %v", err)
	}
}

// "Offline re-install works" is only true if it works for the reference the
// user actually typed. Resolving a tag needs the remote, so re-installing the
// same tag would otherwise demand they go and find its commit id by hand —
// which is not a workflow anyone would use.
func TestOfflineReusesAPreviouslyResolvedTag(t *testing.T) {
	repo := gitRepo(t, map[string]string{"plugin.json": `{"name":"x"}`})
	cache := source.NewCache(t.TempDir())
	ref := "file://" + repo + "@v1.0.0"

	first, err := source.ResolveString(context.Background(), ref, source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}

	offline, err := source.ResolveString(context.Background(), ref, source.Options{Cache: cache, Offline: true})
	if err != nil {
		t.Fatalf("re-installing a previously seen tag offline failed: %v", err)
	}
	if offline.Commit != first.Commit {
		t.Errorf("commit = %s, want the one seen previously (%s)", offline.Commit, first.Commit)
	}
	if !offline.FromCache {
		t.Error("offline resolve should report as cached")
	}
}

// The cache lives in a writable directory on a developer's machine. Poison one
// entry and every future install of that plugin is compromised, with a
// perfectly valid-looking pin — so a modified entry has to be an error, not a
// silent re-fetch.
func TestCacheTamperingIsDetected(t *testing.T) {
	repo := gitRepo(t, map[string]string{"plugin.json": `{"name":"git-plugin"}`})
	cache := source.NewCache(t.TempDir())
	ref := "file://" + repo + "@v1.0.0"

	got, err := source.ResolveString(context.Background(), ref, source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(got.Dir, "evil.sh"), []byte("rm -rf /\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = source.ResolveString(context.Background(), ref, source.Options{Cache: cache})
	if err == nil {
		t.Fatal("a modified cache entry was served without complaint")
	}
	if !strings.Contains(err.Error(), "modified") {
		t.Errorf("error should say what happened: %v", err)
	}
}

func TestResolveGitSubdirectory(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"README.md":                    "monorepo",
		"plugins/db/plugin.json":       `{"name":"db"}`,
		"plugins/db/skills/s/SKILL.md": "body",
		"plugins/other/plugin.json":    `{"name":"other"}`,
	})
	cache := source.NewCache(t.TempDir())

	got, err := source.ResolveString(context.Background(),
		"file://"+repo+"@v1.0.0#plugins/db", source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "plugin.json")); err != nil {
		t.Errorf("subdirectory not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "plugins")); !os.IsNotExist(err) {
		t.Error("the whole repository was extracted instead of the subdirectory")
	}

	if _, err := source.ResolveString(context.Background(),
		"file://"+repo+"@v1.0.0#plugins/missing", source.Options{Cache: cache}); err == nil {
		t.Error("a subdirectory that does not exist should fail")
	}
}

// Two different subdirectories of one repository are different packages and
// must not share a cache entry.
func TestCacheKeyDistinguishesSubdirectories(t *testing.T) {
	repo := gitRepo(t, map[string]string{
		"plugins/a/plugin.json": `{"name":"a"}`,
		"plugins/b/plugin.json": `{"name":"b"}`,
	})
	cache := source.NewCache(t.TempDir())

	a, err := source.ResolveString(context.Background(), "file://"+repo+"@v1.0.0#plugins/a", source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	b, err := source.ResolveString(context.Background(), "file://"+repo+"@v1.0.0#plugins/b", source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if a.Dir == b.Dir {
		t.Error("two subdirectories collided on one cache entry")
	}
	if a.TreeDigest == b.TreeDigest {
		t.Error("different packages produced the same digest")
	}
}

func TestExpectedDigestRejectsSubstitutedPackage(t *testing.T) {
	repo := gitRepo(t, map[string]string{"plugin.json": `{"name":"x"}`})
	cache := source.NewCache(t.TempDir())
	ref := "file://" + repo + "@v1.0.0"

	got, err := source.ResolveString(context.Background(), ref, source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}

	// The recorded digest still verifies.
	if _, err := source.ResolveString(context.Background(), ref, source.Options{
		Cache: cache, ExpectedDigest: got.TreeDigest,
	}); err != nil {
		t.Errorf("matching digest rejected: %v", err)
	}

	// A different one does not.
	if _, err := source.ResolveString(context.Background(), ref, source.Options{
		Cache:          cache,
		ExpectedDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}); err == nil {
		t.Error("a package not matching the recorded digest must not install")
	}
}

// ------------------------------------------------------------ helpers

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func digest(t *testing.T, dir string) string {
	t.Helper()
	d, err := source.TreeDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
