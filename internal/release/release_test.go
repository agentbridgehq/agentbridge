// Package release holds no code. It exists for tests that keep the release
// configuration honest against the rest of the repository.
//
// Distribution breaks in a characteristic way: a platform is added to CI and
// not to the release build, or an archive naming template changes and the
// installers keep constructing the old name. Nothing catches either until a tag
// is pushed, which is the worst moment to find out, because the fix requires
// another release. These tests move both failures to every commit.
package release_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// withoutShellComments strips whole-line comments so a check can look at what
// a script does rather than at what it says about itself.
func withoutShellComments(script string) string {
	var kept []string
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(raw)
}

// makefilePlatforms reads the matrix CI cross-compiles on every change.
func makefilePlatforms(t *testing.T) []string {
	t.Helper()
	makefile := read(t, "../../Makefile")

	m := regexp.MustCompile(`(?m)^PLATFORMS\s*:?=\s*(.+)$`).FindStringSubmatch(makefile)
	if m == nil {
		t.Fatal("Makefile has no PLATFORMS line")
	}
	out := strings.Fields(m[1])
	sort.Strings(out)
	return out
}

// goreleaserPlatforms expands the goos/goarch lists the release builds.
func goreleaserPlatforms(t *testing.T) []string {
	t.Helper()
	config := read(t, "../../.goreleaser.yaml")

	list := func(key string) []string {
		// The lists are simple YAML sequences under a known key; a full parser
		// would mean a dependency in the build for one assertion.
		idx := strings.Index(config, "\n    "+key+":\n")
		if idx < 0 {
			t.Fatalf(".goreleaser.yaml has no %s list", key)
		}
		var out []string
		for _, line := range strings.Split(config[idx+1:], "\n")[1:] {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "- ") {
				break
			}
			out = append(out, strings.TrimPrefix(trimmed, "- "))
		}
		return out
	}

	var out []string
	for _, os := range list("goos") {
		for _, arch := range list("goarch") {
			out = append(out, os+"/"+arch)
		}
	}
	sort.Strings(out)
	return out
}

// A platform that CI compiles but the release does not ship is a platform whose
// users are told it works and then cannot install it.
func TestReleaseShipsEveryPlatformCIBuilds(t *testing.T) {
	ci := makefilePlatforms(t)
	released := goreleaserPlatforms(t)

	if strings.Join(ci, " ") != strings.Join(released, " ") {
		t.Errorf("the release matrix and the CI matrix disagree:\n  Makefile:      %v\n  .goreleaser:   %v",
			ci, released)
	}
}

// Both installers construct artifact names by hand. If the release template
// changes without them, every download 404s on the day of the release.
func TestInstallersMatchTheArchiveNamingTemplate(t *testing.T) {
	config := read(t, "../../.goreleaser.yaml")

	// The template as written in the release configuration.
	if !strings.Contains(config, "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}") {
		t.Fatal(".goreleaser.yaml archive name_template changed; update install.sh and npm/platform.js to match")
	}

	installer := read(t, "../../install.sh")
	if !strings.Contains(installer, `agentbridge_${stripped}_${os}_${arch}.tar.gz`) {
		t.Error("install.sh no longer builds the release archive name")
	}

	platformJS := read(t, "../../npm/platform.js")
	if !strings.Contains(platformJS, "agentbridge_${stripped}_${os}_${goarch}.${ext}") {
		t.Error("npm/platform.js no longer builds the release archive name")
	}
}

// Verification is the point. An installer that downloads without checking would
// contradict everything the tool says about provenance, so the checks are
// asserted to exist rather than left to review.
func TestInstallersVerifyBeforeInstalling(t *testing.T) {
	installer := read(t, "../../install.sh")

	for _, want := range []string{
		"verify_checksum",             // always runs
		"refusing to install",         // an unlisted artifact is refused
		"checksum mismatch",           // tampering is refused
		"certificate-identity-regexp", // a signature is pinned to our workflow
		"AGENTBRIDGE_REQUIRE_SIGNATURE",
	} {
		if !strings.Contains(installer, want) {
			t.Errorf("install.sh is missing %q", want)
		}
	}

	// --ignore-missing would let a checksums file that does not mention our
	// artifact pass, which defeats the check entirely.
	//
	// Comments are stripped first: the script explains why it avoids the flag,
	// and a naive scan flagged that explanation as the thing it warns about.
	if strings.Contains(withoutShellComments(installer), "--ignore-missing") {
		t.Error("install.sh uses --ignore-missing, which lets an unlisted artifact pass verification")
	}

	install := read(t, "../../npm/install.js")
	for _, want := range []string{"verifyChecksum", "does not list", "checksum mismatch"} {
		if !strings.Contains(install, want) {
			t.Errorf("npm/install.js is missing %q", want)
		}
	}
}

// We sign the checksum file and nothing else, so the certificate identity must
// be pinned. Verifying a signature without pinning the identity only proves
// somebody signed it.
func TestReleaseSigningIsKeylessAndPinned(t *testing.T) {
	config := read(t, "../../.goreleaser.yaml")

	if !strings.Contains(config, "cosign") || !strings.Contains(config, "sign-blob") {
		t.Error(".goreleaser.yaml does not sign the release")
	}
	if !strings.Contains(config, "artifacts: checksum") {
		t.Error("signing should cover the checksum file, which transitively covers every artifact")
	}

	workflow := read(t, "../../.github/workflows/release.yml")
	if !strings.Contains(workflow, "id-token: write") {
		t.Error("keyless signing needs the workflow's OIDC identity")
	}
	if !strings.Contains(workflow, "attest-build-provenance") {
		t.Error("release does not attest build provenance")
	}
	// Selling provenance while shipping an untested tree would not survive its
	// own argument.
	if !strings.Contains(workflow, "go test ./...") {
		t.Error("the release workflow does not run the tests")
	}
}

// No source file may be excluded from the repository by .gitignore.
//
// This exists because one was — all of them, in fact. A bare `agentbridge` line
// in .gitignore, meant for the built binary at the repository root, matched at
// *any* depth and so silently excluded the entire `cmd/agentbridge/` source
// directory, the whole CLI, from every commit. It also swallowed
// `npm/bin/agentbridge`, the npm package's declared entry point.
//
// Nothing local could reveal it: builds, tests and every other gate read the
// working tree, not the index, so all of them passed on a repository that no
// clone could build. The failure surfaces only where nobody looks until release.
//
// The check is on *ignored*, not merely untracked. An untracked file is ordinary
// work in progress and failing on it would make this test fire during every
// edit; an ignored source file is always the bug.
func TestNoSourceFileIsGitIgnored(t *testing.T) {
	var candidates []string

	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "dist", "bin", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel("../..", p)
		if rerr != nil {
			return nil
		}
		slashed := filepath.ToSlash(rel)
		if isSourceFile(slashed) {
			candidates = append(candidates, slashed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("found no source files to check, which means the walk is wrong")
	}

	for _, path := range gitIgnored(t, candidates) {
		t.Errorf("%s is excluded by .gitignore: a clone would not contain it", path)
	}
}

// The npm package declares its own contents, and every declared path must
// exist and be committed — otherwise `npm publish` ships a package whose entry
// point is missing, which is how the same .gitignore bug reached npm.
func TestNpmPackageContentsAreTracked(t *testing.T) {
	tracked := gitLsFiles(t, "npm/*")
	if len(tracked) == 0 {
		t.Skip("not a git checkout")
	}

	var pkg struct {
		Bin   map[string]string `json:"bin"`
		Files []string          `json:"files"`
	}
	if err := json.Unmarshal([]byte(read(t, "../../npm/package.json")), &pkg); err != nil {
		t.Fatalf("npm/package.json: %v", err)
	}

	declared := append([]string(nil), pkg.Files...)
	for _, target := range pkg.Bin {
		declared = append(declared, target)
	}
	if len(declared) == 0 {
		t.Fatal("npm/package.json declares neither bin nor files")
	}

	for _, entry := range declared {
		entry = strings.TrimSuffix(entry, "/")
		full := "npm/" + entry
		if _, err := os.Stat("../../" + full); err != nil {
			t.Errorf("npm/package.json declares %q, which does not exist", entry)
			continue
		}
		// A directory is satisfied by any tracked file beneath it.
		var found bool
		for path := range tracked {
			if path == full || strings.HasPrefix(path, full+"/") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("npm/package.json declares %q but nothing under %s is committed", entry, full)
		}
	}
}

// isSourceFile reports whether a file is part of what the repository ships,
// rather than build output or a local artifact.
// It takes the repository-relative path, not the basename, because the two
// extensionless files that matter share one: `npm/bin/agentbridge` is the npm
// package's entry point and must be committed, while `/agentbridge` at the root
// is what `make` builds and must stay ignored. Matching on the name alone made
// this test fail for anyone who ran `make` before `go test` — which is the
// order TESTING.md tells them to use.
func isSourceFile(rel string) bool {
	switch filepath.Ext(rel) {
	case ".go", ".js", ".sh", ".yaml", ".yml", ".json", ".md":
		return true
	}
	return rel == "npm/bin/agentbridge" || rel == "Makefile"
}

// gitIgnored returns the subset of paths excluded by .gitignore. Paths already
// tracked are never reported: git honours the index over the ignore rules, and
// a tracked file is in the repository whatever the patterns say.
func gitIgnored(t *testing.T, paths []string) []string {
	t.Helper()

	tracked := gitLsFiles(t, "")
	if len(tracked) == 0 {
		t.Skip("not a git checkout")
	}

	cmd := exec.Command("git", "check-ignore", "--stdin", "-z")
	cmd.Dir = "../.."
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00"))
	out, _ := cmd.Output() // exit status 1 simply means nothing was ignored

	var ignored []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" && !tracked[p] {
			ignored = append(ignored, p)
		}
	}
	sort.Strings(ignored)
	return ignored
}

// gitLsFiles returns the tracked paths matching a pattern, relative to the
// repository root. An empty result means this is not a git checkout — a source
// tarball, say — where the question does not apply.
func gitLsFiles(t *testing.T, pattern string) map[string]bool {
	t.Helper()

	args := []string{"ls-files", "-z"}
	if pattern != "" {
		args = append(args, "--", pattern)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	paths := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths[p] = true
		}
	}
	return paths
}

// No directory the tests depend on may be empty.
//
// Git cannot store an empty directory. A fixture that *is* one — say a
// directory named `mcp.json`, built to prove that a non-regular file at a fixed
// location is rejected — exists perfectly on the machine that made it and is
// simply absent from every clone. The test then passes for its author and fails
// for everyone else, and no local gate can see the difference, because they all
// read the working tree rather than the index.
//
// That is exactly how it happened here: `testdata/mcp-not-file/mcp.json` was an
// empty directory, and the failure surfaced only when a clone from GitHub was
// built. Such a fixture must be constructed at runtime instead.
func TestNoEmptyDirectoriesInTheWorkingTree(t *testing.T) {
	if len(gitLsFiles(t, "")) == 0 {
		t.Skip("not a git checkout")
	}

	var empty []string
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		switch d.Name() {
		case ".git", "dist", "bin", "node_modules":
			return filepath.SkipDir
		}
		entries, rerr := os.ReadDir(p)
		if rerr != nil {
			return nil
		}
		if len(entries) == 0 {
			rel, _ := filepath.Rel("../..", p)
			empty = append(empty, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	sort.Strings(empty)
	for _, dir := range empty {
		t.Errorf("%s is an empty directory, so git will not store it and a clone will not have it.\n"+
			"If a test depends on it, build it at runtime with t.TempDir() instead.", dir)
	}
}
