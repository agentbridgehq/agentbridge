package conformance_test

import (
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/conformance"
)

const corpus = "../../conformance/cases"

// Our own loader must pass every case. A corpus we publish and then fail would
// be worse than not publishing one.
func TestSelfConformance(t *testing.T) {
	report, err := conformance.RunSelf(corpus)
	if err != nil {
		t.Fatal(err)
	}

	for _, res := range report.Results {
		if res.Outcome != conformance.Pass {
			t.Errorf("%s (§%s): %s\n    %s",
				res.Case.ID, res.Case.Section, res.Outcome, res.Detail)
		}
	}
	if report.Count(conformance.Pass) < 15 {
		t.Errorf("only %d cases ran; the corpus should be substantial enough to be worth citing",
			report.Count(conformance.Pass))
	}
}

// Every case must be usable by a human testing a client this runner cannot
// drive. Without that, the corpus only measures us, which is the least
// interesting thing it could do.
func TestEveryCaseIsManuallyRunnable(t *testing.T) {
	cases, err := conformance.LoadCases(corpus)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range cases {
		if c.Section == "" {
			t.Errorf("%s cites no specification section", c.ID)
		}
		if c.Requirement == "" {
			t.Errorf("%s does not say whether it is a MUST or a SHOULD", c.ID)
		}
		if strings.TrimSpace(c.Observe) == "" {
			t.Errorf("%s has no observation note, so nobody can check another client with it", c.ID)
		}
		if len(c.Observe) < 40 {
			t.Errorf("%s: the observation note is too short to act on: %q", c.ID, c.Observe)
		}
	}
}

// An untested client is represented explicitly rather than omitted. A blank row
// in a compatibility matrix invites the reader to assume something; a row that
// says nobody has checked does not.
func TestBlankReportIsAllUnmeasured(t *testing.T) {
	report, err := conformance.Blank(corpus, "some-client")
	if err != nil {
		t.Fatal(err)
	}

	if report.Count(conformance.Unmeasured) != len(report.Results) {
		t.Error("a blank report must not claim any result")
	}
	if report.Conformant() {
		t.Error("an unmeasured client must never read as conformant")
	}
}

// The corpus should cover the requirements most likely to be got wrong, not
// just the easy ones.
func TestCorpusCoversTheHardRequirements(t *testing.T) {
	cases, err := conformance.LoadCases(corpus)
	if err != nil {
		t.Fatal(err)
	}

	sections := map[string]bool{}
	for _, c := range cases {
		sections[c.Section] = true
	}

	// Each of these is a rule JSON Schema cannot express, or one where
	// following the published schema literally produces the wrong behaviour.
	for _, want := range []string{
		"5.2",   // unknown fields and non-object extensions are non-fatal
		"5.5",   // the name rule, whose regex Go cannot compile
		"7.1",   // skills are immediate children only
		"7.2.1", // transport security, command form, cwd containment
		"7.2.2", // one bad server does not sink the rest
		"9.2",   // reserved environment names
		"10.1",  // version mismatch disables MCP alone
	} {
		if !sections[want] {
			t.Errorf("the corpus does not cover §%s", want)
		}
	}
}
