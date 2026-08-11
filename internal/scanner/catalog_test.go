package scanner_test

import (
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/scanner"
)

// allRuleIDs lists every rule the package defines.
//
// Maintained by hand for the same reason as the loss codes: adding one here is
// the deliberate act that admits a new way to interrupt somebody, and the tests
// below then insist it be documented and actionable before it can.
var allRuleIDs = []string{
	scanner.RuleInstructionOverride,
	scanner.RuleConcealFromUser,
	scanner.RuleBypassConfirmation,
	scanner.RuleCredentialAccess,
	scanner.RuleExfiltration,
	scanner.RuleDestructiveAction,
	scanner.RuleBidiControl,
	scanner.RuleZeroWidth,
	scanner.RuleHomoglyph,
	scanner.RuleHiddenComment,
	scanner.RuleEncodedBlob,
	scanner.RuleScriptDestructive,
	scanner.RuleScriptRemoteExec,
	scanner.RuleServerSecretLiteral,
	scanner.RuleServerRemoteEgress,
}

func TestEveryRuleIsDocumented(t *testing.T) {
	for _, id := range allRuleIDs {
		rule, ok := scanner.Lookup(id)
		if !ok {
			t.Errorf("%s is not in the catalogue", id)
			continue
		}
		if rule.Title == "" {
			t.Errorf("%s has no title", id)
		}
		// A rationale is what lets a reader disagree with the tool. Without it a
		// finding is an assertion, and an assertion from a heuristic is worth
		// very little.
		if len(rule.Rationale) < 60 {
			t.Errorf("%s: rationale is too short to justify the finding: %q", id, rule.Rationale)
		}
		if rule.Remedy == "" {
			t.Errorf("%s has no remedy; a finding nobody can act on is an interruption", id)
		}
	}
}

// The catalogue and the constants must not drift apart. A rule present in one
// and not the other is either an undocumented finding or dead documentation.
func TestCatalogMatchesConstants(t *testing.T) {
	declared := map[string]bool{}
	for _, id := range allRuleIDs {
		declared[id] = true
	}
	for _, rule := range scanner.Catalog() {
		if !declared[rule.ID] {
			t.Errorf("%s is catalogued but not declared in allRuleIDs", rule.ID)
		}
	}
	if got, want := len(scanner.Catalog()), len(allRuleIDs); got != want {
		t.Errorf("catalogue has %d rules, %d declared", got, want)
	}
}

// Rule identifiers appear in SARIF and in policy, so their shape is a
// compatibility surface.
func TestRuleIDsAreStableShaped(t *testing.T) {
	for _, rule := range scanner.Catalog() {
		if strings.ToLower(rule.ID) != rule.ID {
			t.Errorf("%s: rule ids are lowercase", rule.ID)
		}
		if strings.Count(rule.ID, ".") != 1 {
			t.Errorf("%s: expected exactly one dot, as category.rule", rule.ID)
		}
	}
}

func TestParseSeverity(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want scanner.Severity
	}{
		{"high", scanner.High},
		{"HIGH", scanner.High},
		{"medium", scanner.Medium},
		{"low", scanner.Low},
		{"info", scanner.Info},
	} {
		got, err := scanner.ParseSeverity(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseSeverity(%q) = %q, %v", tc.in, got, err)
		}
	}
	if _, err := scanner.ParseSeverity("critical"); err == nil {
		t.Error("expected an error for an unknown severity")
	}
}

func TestSeverityOrdering(t *testing.T) {
	if !scanner.High.AtLeast(scanner.Medium) {
		t.Error("high should meet a medium threshold")
	}
	if scanner.Low.AtLeast(scanner.High) {
		t.Error("low should not meet a high threshold")
	}
	if !scanner.Info.AtLeast(scanner.Info) {
		t.Error("a threshold should include its own level")
	}
}
