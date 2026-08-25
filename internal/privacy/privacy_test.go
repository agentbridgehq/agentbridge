// Package privacy holds no code. It exists for a test that turns a promise
// into a property.
//
// "We collect nothing" is easy to write in a README and easy to erode: one
// well-meant crash reporter, one version check, one "anonymous" usage ping, and
// a tool that runs on every developer machine in a company is phoning home.
// Nobody notices until a security review does, and by then the claim in the
// documentation is false.
//
// So the claim is asserted here instead. The CLI makes no network calls of its
// own: fetching a plugin shells out to git against a remote the user named, and
// nothing else opens a connection. A change that breaks that fails this test
// before it reaches anyone.
package privacy_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// networkCalls matches the ways Go code reaches the network. Deliberately
// broad: the point is to force a conversation about any new one, not to
// enumerate the current set.
var networkCalls = regexp.MustCompile(
	`\bhttp\.(Get|Post|Head|PostForm|NewRequest|NewRequestWithContext|Client\{|DefaultClient)\b` +
		`|\bnet\.Dial\b` +
		`|\bwebsocket\.\b` +
		`|\bgrpc\.Dial\b`)

// devToolPrefix holds programs run by maintainers, never compiled into the
// binary users install: the licence checker, the documentation generator, and
// the upstream watcher, which fetches the canonical schemas precisely so a
// human is told when they change.
//
// Excluding them from the network scan would be a loophole if it stood alone —
// somebody could add a telemetry package here and the scan would say nothing.
// TestShippedBinaryExcludesDevTools closes it by proving from the real build
// graph that none of this reaches the binary.
const devToolPrefix = "internal/tools/"

// networkAllowed names the files permitted to open a connection.
//
// Until M3-5 the answer was "none": fetching shelled out to git, and the scan
// above was a blanket ban. Pulling from an OCI registry cannot be delegated to
// a subprocess the same way, so this is a real relaxation and is treated as
// one — the list is exact filenames rather than a directory, so a second file
// cannot quietly join it.
//
// A blanket ban is only worth giving up for something stronger, and the
// stronger property is below: TestFetchersDeriveEveryHostFromTheReference
// checks that these files contain no hardcoded destination at all, so the only
// host they can reach is the one written in the reference — or, for the
// classifier, the endpoint — that the user supplied.
// TestNoCallsToOperatedInfrastructure continues to apply to them unchanged.
//
// The classifier (M11-11) is the second entry and the more consequential one,
// because it sends the *contents* of a plugin rather than fetching them. Three
// things keep it inside the promise this project makes:
//
//   - It is off unless a caller passes a classifier, which scanner.Scan cannot
//     do — TestClassifierIsOffByDefault asserts the CLI leaves it off.
//   - It has no default endpoint. The user names the host, exactly as they name
//     a git remote or a registry, and this file's no-hardcoded-URL rule holds
//     it to that.
//   - What it sends, and to whom, is documented in docs/telemetry.md.
var networkAllowed = map[string]bool{
	"internal/source/oci.go":       true,
	"internal/scanner/classify.go": true,
}

// sourceFiles walks the packages that ship in the binary.
func sourceFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}

	// Every directory holding Go that reaches the binary. `conformance` is a
	// top-level package rather than an internal one because the corpus is meant
	// to be imported by client vendors — which also means it would have sat
	// outside this scan had it not been added here deliberately.
	for _, root := range []string{"../../internal", "../../cmd", "../../conformance"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if strings.Contains(filepath.ToSlash(path), devToolPrefix) {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = string(raw)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// The CLI must work with no network access to anything we operate. That is what
// makes it deployable in the regulated and air-gapped environments the
// enterprise story depends on, and it is the reason a security team can approve
// it without a data-flow review.
func TestCLIMakesNoNetworkCalls(t *testing.T) {
	for path, content := range sourceFiles(t) {
		if networkAllowed[relativeToRepo(path)] {
			continue
		}
		if m := networkCalls.FindString(content); m != "" {
			t.Errorf("%s contains a network call (%s).\n"+
				"The CLI opens a connection in exactly one place — the OCI client in "+
				"internal/source/oci.go, against the registry named in the reference — and shells out "+
				"to git for everything else. If this is deliberate, update docs/telemetry.md, "+
				"networkAllowed and this test together — never one without the others.", path, m)
		}
	}
}

// The fetchers may reach the network, but only where the user pointed them.
//
// This is what replaces the blanket ban, and it is the property that actually
// matters: a hardcoded host in a fetcher is how a "check for updates" or a
// "report a failed pull" arrives, and neither would be caught by a rule that
// merely permits HTTP in this file. Every destination must be assembled from
// the reference, so the set of hosts the CLI can contact is exactly the set the
// user typed.
func TestFetchersDeriveEveryHostFromTheReference(t *testing.T) {
	// Any absolute URL literal. The scheme-relative form is enough to catch a
	// hardcoded destination however it is spelled.
	literal := regexp.MustCompile(`"[a-zA-Z][a-zA-Z0-9+.-]*://[^"]*"`)

	for path, content := range sourceFiles(t) {
		if !networkAllowed[relativeToRepo(path)] {
			continue
		}
		for _, m := range literal.FindAllString(content, -1) {
			// Media types and the reference scheme itself are not
			// destinations; a host is what this is looking for.
			if strings.HasPrefix(m, `"application/`) || m == `"oci://"` {
				continue
			}
			t.Errorf("%s contains the absolute URL %s.\n"+
				"A fetcher must derive every destination from the reference it was given, so the only "+
				"hosts this tool can contact are the ones the user named.", path, m)
		}
	}
}

// relativeToRepo turns a walk path into the repository-relative form used by
// networkAllowed, so the list reads as filenames rather than as walk artifacts.
func relativeToRepo(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "../../")
}

// Reaching a service we run is the specific failure this guards against. A
// generic HTTP call could be a plugin registry the user asked for; a call to
// our own infrastructure could not.
func TestNoCallsToOperatedInfrastructure(t *testing.T) {
	// .invalid is reserved and unresolvable by design, which is why derived
	// schema identifiers use it: they can never be fetched even by accident,
	// and the specification forbids retrieving a schema while loading (§5.2).
	operated := regexp.MustCompile(`https?://[a-zA-Z0-9.-]*agentbridge\.(dev|io|com|org|net|app|sh)\b`)

	for path, content := range sourceFiles(t) {
		if m := operated.FindString(content); m != "" {
			t.Errorf("%s references %s. Nothing in the CLI may contact infrastructure we operate.", path, m)
		}
	}
}

// The exclusion above is only sound if those packages genuinely do not reach
// the binary, so that is checked against the real build graph rather than
// assumed. `go list -deps` reports exactly what the linker will include.
func TestShippedBinaryExcludesDevTools(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../../cmd/agentbridge").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	for _, pkg := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(pkg, devToolPrefix) {
			t.Errorf("the agentbridge binary depends on %s.\n"+
				"Development tools are excluded from the network scan on the grounds that they are "+
				"not shipped. Importing one from the CLI turns that exclusion into a hole.", pkg)
		}
	}
}

// The scan is only worth its roots, and the roots are a hand-written list.
//
// Adding `conformance` as a top-level package — so client vendors can import
// the corpus — put first-party code that ships in the binary outside
// `internal/` and `cmd/` for the first time, and the scan would have said
// nothing about it. Checking the roots against the real build graph turns
// "remember to update the list" into a property, which is the only version of
// that instruction that survives contact with a busy afternoon.
func TestEveryShippedPackageIsScanned(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../../cmd/agentbridge").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	const module = "github.com/agentbridgehq/agentbridge"
	scanned := sourceFiles(t)

	for _, pkg := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(pkg, module) {
			continue // a dependency, not ours
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(pkg, module), "/")
		if rel == "" {
			rel = "."
		}

		var covered bool
		for path := range scanned {
			if filepath.Dir(filepath.ToSlash(path)) == "../../"+rel {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("%s ships in the binary but no file of it is scanned.\n"+
				"Add its directory to the roots in sourceFiles, or the network and "+
				"infrastructure checks silently do not apply to it.", pkg)
		}
	}
}

// The specification is explicit: a client "MUST NOT retrieve a schema while
// loading a plugin" (§5.2, §7.2.1). Embedding them is also what lets the tool
// work offline at all.
func TestSchemasAreEmbeddedNotFetched(t *testing.T) {
	raw, err := os.ReadFile("../schema/schema.go")
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)

	if !strings.Contains(content, "//go:embed plugin.schema.json") {
		t.Error("the plugin schema is no longer embedded")
	}
	if !strings.Contains(content, "//go:embed mcp.schema.json") {
		t.Error("the MCP schema is no longer embedded")
	}
}

// The documented claim and the enforced one must be the same claim.
func TestTelemetryStatementMatchesTheCode(t *testing.T) {
	raw, err := os.ReadFile("../../docs/telemetry.md")
	if err != nil {
		t.Fatalf("docs/telemetry.md is missing: %v", err)
	}
	statement := string(raw)

	for _, want := range []string{
		"no telemetry",
		"internal/privacy",
	} {
		if !strings.Contains(strings.ToLower(statement), strings.ToLower(want)) {
			t.Errorf("docs/telemetry.md should mention %q, so the claim and its enforcement stay linked", want)
		}
	}
}
