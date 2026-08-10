package adapter_test

import (
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/adapter"
)

// allLossCodes lists every loss constant the package defines.
//
// Maintained by hand on purpose: adding a code here is the deliberate act that
// admits a new way for a component to disappear, and the tests below then
// insist it be documented and declared before it can reach anyone.
var allLossCodes = []string{
	adapter.LossSkillsUnsupported,
	adapter.LossSkillsUndocumented,
	adapter.LossTransportUnsupported,
	adapter.LossExtensionsDropped,
	adapter.LossNativeComponentDropped,
	adapter.LossFlatSkillRestructured,
	adapter.LossSecretInPlaintext,
	adapter.LossSecretPlaintextRefused,
	adapter.LossSecretMissing,
	adapter.LossSecretNoLauncher,
	adapter.LossSecretPartialRef,
}

// Every code is documented. A loss reported by a code nobody can look up is
// only marginally better than a silent one.
func TestEveryLossCodeIsDocumented(t *testing.T) {
	for _, code := range allLossCodes {
		info, ok := adapter.LookupLoss(code)
		if !ok {
			t.Errorf("%s is not in the catalogue", code)
			continue
		}
		if info.Title == "" || info.Meaning == "" {
			t.Errorf("%s is catalogued without %s", code, missingFields(info))
		}
		// The meaning must explain, not restate. Matching on particular
		// phrasing was tried and dropped: it tested the wording rather than
		// the contract, and failed on a description that was perfectly clear.
		if strings.EqualFold(info.Meaning, info.Title) {
			t.Errorf("%s: meaning merely repeats the title", code)
		}
		if len(info.Meaning) < 40 {
			t.Errorf("%s: meaning is too short to explain anything: %q", code, info.Meaning)
		}
	}
}

// A loss that is a problem rather than a difference between clients must say
// what to do about it.
func TestUnexpectedLossesCarryARemedy(t *testing.T) {
	for _, info := range adapter.LossCatalog() {
		if !info.Expected && info.Remedy == "" {
			t.Errorf("%s is not an expected consequence of client differences, so it needs a remedy", info.Code)
		}
	}
}

// The catalogue must not accumulate entries nothing can produce, and no code
// may exist outside it.
func TestCatalogMatchesTheConstants(t *testing.T) {
	known := map[string]bool{}
	for _, c := range allLossCodes {
		known[c] = true
	}
	for _, info := range adapter.LossCatalog() {
		if !known[info.Code] {
			t.Errorf("catalogue documents %q, which is not a defined loss code", info.Code)
		}
	}
	if len(adapter.LossCatalog()) != len(allLossCodes) {
		t.Errorf("catalogue has %d entries for %d codes", len(adapter.LossCatalog()), len(allLossCodes))
	}
}

func TestValidateRejectsUncataloguedCodes(t *testing.T) {
	var f adapter.Fidelity
	f.AddLoss("made.up.code", "", "something")

	err := f.Validate()
	if err == nil {
		t.Fatal("an uncatalogued code should not validate")
	}
	// The message must say what to do, since this fires on a developer who is
	// mid-change.
	if !strings.Contains(err.Error(), "losses.go") {
		t.Errorf("error should point at the catalogue: %v", err)
	}
}

// The rule that keeps the catalogue honest: an adapter may not emit a code it
// did not declare, so a new drop cannot appear at runtime without also
// appearing in the list of what that client might not carry.
func TestValidateAgainstRejectsUndeclaredCodes(t *testing.T) {
	client := adapter.Client{ID: "test", Losses: []string{adapter.LossExtensionsDropped}}

	var f adapter.Fidelity
	f.AddLoss(adapter.LossExtensionsDropped, "", "fine")
	if err := f.ValidateAgainst(client); err != nil {
		t.Errorf("a declared code should validate: %v", err)
	}

	f.AddLoss(adapter.LossSecretMissing, "", "not declared")
	err := f.ValidateAgainst(client)
	if err == nil {
		t.Fatal("an undeclared code should not validate")
	}
	if !strings.Contains(err.Error(), "declaring") {
		t.Errorf("error should say what is missing: %v", err)
	}
}

// The distinction a reader needs: which of these warnings is a fact about the
// ecosystem, and which is something they can act on.
func TestUnexpectedSeparatesFaultsFromFacts(t *testing.T) {
	var f adapter.Fidelity
	f.AddLoss(adapter.LossSkillsUndocumented, "", "a permanent client difference")
	f.AddLoss(adapter.LossSecretMissing, "db", "something to fix")

	unexpected := f.Unexpected()
	if len(unexpected) != 1 || unexpected[0].Code != adapter.LossSecretMissing {
		t.Errorf("Unexpected = %+v, want only the actionable one", unexpected)
	}
}

func TestCommonLossesAreCatalogued(t *testing.T) {
	for _, code := range adapter.CommonLosses() {
		if _, ok := adapter.LookupLoss(code); !ok {
			t.Errorf("common loss %q is not catalogued", code)
		}
	}
}

func TestDeclaredLossesIncludesCommonOnes(t *testing.T) {
	got := adapter.DeclaredLosses(adapter.LossSkillsUnsupported)

	has := map[string]bool{}
	for _, c := range got {
		has[c] = true
	}
	if !has[adapter.LossSkillsUnsupported] {
		t.Error("the client's own code is missing")
	}
	for _, c := range adapter.CommonLosses() {
		if !has[c] {
			t.Errorf("common code %q is missing", c)
		}
	}
}

func missingFields(info adapter.LossInfo) string {
	var missing []string
	if info.Title == "" {
		missing = append(missing, "a title")
	}
	if info.Meaning == "" {
		missing = append(missing, "a meaning")
	}
	return strings.Join(missing, " and ")
}
