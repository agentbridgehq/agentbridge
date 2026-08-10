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

// sourceFiles walks the packages that ship in the binary.
func sourceFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}

	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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
		if m := networkCalls.FindString(content); m != "" {
			t.Errorf("%s contains a network call (%s).\n"+
				"The CLI is documented as making none: fetching a plugin shells out to git against a "+
				"remote the user named, and nothing else opens a connection. If this is deliberate, "+
				"update docs/telemetry.md and this test together — never one without the other.", path, m)
		}
	}
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
