package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/ir"
)

// TestMatchDoesNotWidenAnAllowList is the test that matters most in this
// package.
//
// Every other rule fails safe: a mistake denies something that should have
// been allowed, somebody notices immediately, and the fix is a one-line edit.
// The matcher is the one place where a mistake fails *open* — a pattern that
// matches more than its author meant admits sources nobody approved, and
// nothing about that looks wrong from the outside. So the cases below are
// weighted towards what must NOT match.
func TestMatchDoesNotWidenAnAllowList(t *testing.T) {
	for _, tc := range []struct {
		pattern, source string
		want            bool
		why             string
	}{
		{"github.com/acme/*", "github.com/acme/db", true, "the ordinary case"},
		{"github.com/acme/*", "github.com/acme/db-tools", true, "hyphens are part of a segment"},
		{"github.com/acme/*", "github.com/acme", false, "the org itself is not a repository under it"},
		{"github.com/acme/*", "github.com/acme/db/sub", false, "* stops at a separator"},
		{"github.com/acme/**", "github.com/acme/db/sub", true, "** is how you ask to cross one"},
		{"github.com/acme/**", "github.com/acme", true, "a trailing /** includes the prefix itself"},

		// The dangerous family: a pattern meant for one org matching another.
		{"github.com/acme/*", "github.com/acme-evil/db", false, "a longer org name must not match"},
		{"github.com/acme/*", "github.com/notacme/db", false, "a different org entirely"},
		{"github.com/acme/*", "evil.com/github.com/acme/db", false, "the pattern is not a substring search"},
		{"github.com/acme/*", "github.com.evil.io/acme/db", false, "a suffixed host must not match"},
		{"oci://ghcr.io/acme/*", "oci://ghcr.io.evil/acme/x", false, "same trick on a registry host"},

		{"*", "github.com/acme/db", false, "a bare * is one segment, not everything"},
		{"**", "github.com/acme/db", true, "** is everything"},
		{"github.com/acme/db", "github.com/acme/db", true, "an exact pattern"},
		{"github.com/acme/db", "github.com/acme/dbx", false, "an exact pattern is exact"},
	} {
		if got := match(tc.pattern, tc.source); got != tc.want {
			t.Errorf("match(%q, %q) = %v, want %v — %s", tc.pattern, tc.source, got, tc.want, tc.why)
		}
	}
}

func write(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, body string) *Policy {
	t.Helper()
	p, err := Load(write(t, t.TempDir(), body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p == nil {
		t.Fatal("Load returned no policy for a file that exists")
	}
	return p
}

func TestAnAllowListBlocksWhatIsNotOnIt(t *testing.T) {
	p := load(t, `
version: 1
sources:
  allow: ["github.com/acme/*"]
`)
	allowed := Subject{Name: "db", Source: "github.com/acme/db"}
	if v := Check([]*Policy{p}, allowed); len(v) != 0 {
		t.Errorf("an allowed source was blocked: %v", v)
	}

	blocked := Subject{Name: "x", Source: "github.com/somebody/x"}
	v := Check([]*Policy{p}, blocked)
	if len(v) != 1 {
		t.Fatalf("violations = %d, want 1", len(v))
	}
	if v[0].Rule != "sources.allow" {
		t.Errorf("rule = %q, want sources.allow", v[0].Rule)
	}
	// The refusal has to name the file, or the reader cannot find the rule to
	// change it.
	if v[0].Policy == "" {
		t.Error("the violation does not say which policy file it came from")
	}
}

func TestDenyBeatsAllow(t *testing.T) {
	p := load(t, `
version: 1
sources:
  allow: ["github.com/acme/**"]
  deny: ["github.com/acme/experimental-*"]
`)
	if v := Check([]*Policy{p}, Subject{Name: "ok", Source: "github.com/acme/db"}); len(v) != 0 {
		t.Errorf("a source inside the allow-list was blocked: %v", v)
	}
	v := Check([]*Policy{p}, Subject{Name: "x", Source: "github.com/acme/experimental-thing"})
	if len(v) != 1 || v[0].Rule != "sources.deny" {
		t.Errorf("violations = %v, want one sources.deny", v)
	}
}

// A capability ceiling is the rule an org reaches for first, and it is checked
// against what the package was measured to do rather than what it claims.
func TestACapabilityCeilingIsEnforced(t *testing.T) {
	p := load(t, `
version: 1
capabilities:
  deny: [network]
`)
	quiet := Subject{Name: "quiet", Capabilities: ir.Capabilities{Exec: true}}
	if v := Check([]*Policy{p}, quiet); len(v) != 0 {
		t.Errorf("a plugin without the denied capability was blocked: %v", v)
	}

	noisy := Subject{Name: "noisy", Capabilities: ir.Capabilities{Network: true}}
	v := Check([]*Policy{p}, noisy)
	if len(v) != 1 || v[0].Rule != "capabilities.deny" {
		t.Fatalf("violations = %v, want one capabilities.deny", v)
	}
	if !strings.Contains(v[0].Detail, "network") {
		t.Errorf("the refusal does not name the capability: %q", v[0].Detail)
	}
}

// Local directories are the obvious way around an allow-list: copy the package
// to /tmp and install from there. Allowing them is the default because
// forbidding it stops an author testing their own work, but an org that closes
// the hole must have it actually closed.
func TestLocalDirectoriesAreGovernedSeparately(t *testing.T) {
	local := Subject{Name: "wip", Source: "/tmp/wip", Local: true}

	relaxed := load(t, `
version: 1
sources:
  allow: ["github.com/acme/*"]
`)
	if v := Check([]*Policy{relaxed}, local); len(v) != 0 {
		t.Errorf("a local directory was blocked by default: %v", v)
	}

	strict := load(t, `
version: 1
sources:
  allow: ["github.com/acme/*"]
  local: deny
`)
	v := Check([]*Policy{strict}, local)
	if len(v) != 1 || v[0].Rule != "sources.local" {
		t.Errorf("violations = %v, want one sources.local", v)
	}
}

// Policies restrict and never grant. A second policy must not be able to
// re-permit what the first forbade, or a user-level file would defeat the
// project-level one that exists precisely to constrain it.
func TestASecondPolicyCanOnlyNarrow(t *testing.T) {
	project := load(t, `
version: 1
sources:
  allow: ["github.com/acme/*"]
`)
	permissive := load(t, `
version: 1
sources:
  allow: ["**"]
`)
	s := Subject{Name: "x", Source: "github.com/somebody/x"}
	if v := Check([]*Policy{project, permissive}, s); len(v) == 0 {
		t.Error("a permissive second policy re-permitted what the first denied")
	}
	if v := Check([]*Policy{permissive, project}, s); len(v) == 0 {
		t.Error("order changed the outcome; policies must compose the same way either way round")
	}
}

// Every violation is reported, not just the first. Someone fixing this wants
// the whole list; finding a second refusal after satisfying the first is how a
// five-minute change becomes an afternoon.
func TestEveryViolationIsReported(t *testing.T) {
	p := load(t, `
version: 1
sources:
  allow: ["github.com/acme/*"]
capabilities:
  deny: [network, exec]
plugins:
  deny: [banned]
`)
	s := Subject{
		Name: "banned", Source: "github.com/elsewhere/banned",
		Capabilities: ir.Capabilities{Network: true, Exec: true},
	}
	v := Check([]*Policy{p}, s)
	if len(v) != 4 {
		t.Errorf("violations = %d, want 4 (name, source, and two capabilities): %v", len(v), v)
	}
}

// A rule that silently stops applying is worse than no rule: the enforcement
// disappears and the file stays in the repository, still looking like
// protection. So anything malformed is an error.
func TestAMisspelledRuleIsAnErrorNotSilence(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"misspelled section", "version: 1\nsourcez:\n  allow: [\"x\"]\n"},
		{"misspelled key", "version: 1\nsources:\n  allowed: [\"x\"]\n"},
		{"unknown capability", "version: 1\ncapabilities:\n  deny: [netwrok]\n"},
		{"unknown local mode", "version: 1\nsources:\n  local: maybe\n"},
		{"future version", "version: 2\n"},
		{"no version", "sources:\n  allow: [\"x\"]\n"},
		{"not yaml", "{{{\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(write(t, t.TempDir(), tc.body)); err == nil {
				t.Error("accepted a policy that would not do what it appears to say")
			}
		})
	}
}

// No policy is the normal case and must not be an error, or every user without
// one pays for a feature they did not ask for.
func TestNoPolicyIsNotAnError(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatalf("a missing policy file was an error: %v", err)
	}
	if p != nil {
		t.Error("a missing policy file produced a policy")
	}
	if v := Check(nil, Subject{Name: "x", Source: "anywhere"}); len(v) != 0 {
		t.Errorf("no policy blocked something: %v", v)
	}
}

// A policy applies from anywhere inside the repository, the way git finds its
// root. Applying only at the top would mean `cd services/api && agentbridge
// install …` silently escapes it.
func TestAProjectPolicyAppliesFromASubdirectory(t *testing.T) {
	root := t.TempDir()
	write(t, root, "version: 1\nsources:\n  allow: [\"github.com/acme/*\"]\n")
	deep := filepath.Join(root, "services", "api", "internal")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	found, err := Discover(deep, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("no policy found from a subdirectory of the repository")
	}
	if v := Check(found, Subject{Name: "x", Source: "github.com/elsewhere/x"}); len(v) == 0 {
		t.Error("the policy was found but not enforced")
	}
}

// Discovering the same file twice would report every violation twice, which
// makes a refusal read as though two separate rules were broken.
func TestAPolicyIsNotCountedTwice(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "version: 1\nsources:\n  allow: [\"github.com/acme/*\"]\n")

	found, err := Discover(dir, dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d policies for one file", len(found))
	}
	if v := Check(found, Subject{Name: "x", Source: "github.com/elsewhere/x"}); len(v) != 1 {
		t.Errorf("violations = %d, want 1", len(v))
	}
}
