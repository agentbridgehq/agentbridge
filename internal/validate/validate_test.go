package validate_test

import (
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/validate"
)

func run(t *testing.T, dir string) *validate.Report {
	t.Helper()
	r, err := validate.Run(dir)
	if err != nil {
		t.Fatalf("Run(%s): %v", dir, err)
	}
	return r
}

func find(r *validate.Report, sev validate.Severity, substr string) bool {
	for _, f := range r.Findings {
		if f.Severity == sev && strings.Contains(f.Message, substr) {
			return true
		}
	}
	return false
}

// The author-facing counterpart of the loader must be stricter, not merely
// different: everything a conformant client is obliged to tolerate still shows
// up here.
func TestReportsWhatTheLoaderTolerates(t *testing.T) {
	r := run(t, "../importer/agentplugins/testdata/valid")

	if !r.Loaded {
		t.Fatal("plugin should have loaded")
	}
	// §5.2: a client must report-and-ignore an unknown field. An author wants
	// to know, because it is almost always a typo.
	if !find(r, validate.Advisory, "unknownField") {
		t.Errorf("unknown top-level field not surfaced: %+v", r.Findings)
	}
	// §7.2.1: plain HTTP to a non-loopback host.
	if !find(r, validate.Violation, "requires https") {
		t.Errorf("insecure url not reported as a violation: %+v", r.Findings)
	}
	if r.Conformant() {
		t.Error("a plugin with violations must not be reported as conformant")
	}
}

// §9.2 and §7.2.1 forbid credentials in env values and headers. No client can
// enforce this — the values are already package data by the time a client sees
// them — so the author-side check is the only place it will ever be reported.
func TestReportsSecretsNoClientCanCatch(t *testing.T) {
	r := run(t, "../importer/agentplugins/testdata/valid")

	if !find(r, validate.Advisory, "env values are visible package data") {
		t.Errorf("secret in env not surfaced: %+v", r.Findings)
	}
	if !find(r, validate.Advisory, "header values are visible package data") {
		t.Errorf("credential-bearing header not surfaced: %+v", r.Findings)
	}

	// And it must be said once, not twice in different words.
	n := 0
	for _, f := range r.Findings {
		if strings.Contains(f.Message, "DB_API_TOKEN") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the same finding was reported %d times", n)
	}
}

// Every finding cites the clause it comes from, so an author can check the
// claim rather than take our word for it.
func TestFindingsCiteSpecSections(t *testing.T) {
	r := run(t, "../importer/agentplugins/testdata/valid")

	for _, f := range r.Findings {
		if f.Severity == validate.Note {
			continue
		}
		if f.Section == "" {
			t.Errorf("finding without a section citation: %+v", f)
		}
	}
}

// A plugin in another dialect is not breaking the manifest rules — it never
// claimed to follow them. The useful question is what it would take to publish
// it portably.
func TestForeignDialectIsReportedAsPortability(t *testing.T) {
	r := run(t, "../importer/claudecode/testdata/full")

	if !r.ForeignDialect() {
		t.Fatalf("dialect = %q", r.Dialect)
	}
	if !find(r, validate.Note, "not an Agent Plugins package") {
		t.Errorf("dialect not explained: %+v", r.Findings)
	}
	// A component with no portable equivalent is an advisory, not a spec
	// violation: the plugin is not breaking a rule by having hooks.
	if !find(r, validate.Advisory, "no Agent Plugins equivalent") {
		t.Errorf("unsupported components should be advisories: %+v", r.Findings)
	}
}

// A rejected plugin must still produce a report rather than an error, so the
// author sees why.
func TestRejectedPluginStillReports(t *testing.T) {
	r := run(t, "../importer/agentplugins/testdata/bad-name")

	if r.Loaded {
		t.Error("plugin should have been rejected")
	}
	if r.Count(validate.Violation) == 0 {
		t.Errorf("rejection not reported: %+v", r.Findings)
	}
	if !find(r, validate.Violation, "consecutive hyphens") {
		t.Errorf("the reason should be specific: %+v", r.Findings)
	}
}

func TestJSONOutput(t *testing.T) {
	r := run(t, "../importer/agentplugins/testdata/valid")

	raw, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"severity"`, `"section"`, `"dialect"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %s in JSON output", want)
		}
	}
}
