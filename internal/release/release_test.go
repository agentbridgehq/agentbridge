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
	"os"
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
