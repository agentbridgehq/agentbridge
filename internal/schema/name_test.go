package schema_test

import (
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/schema"
)

// The canonical schema expresses this rule with a negative lookahead that Go's
// regexp engine cannot compile, so it is enforced in code instead. These cases
// are the contract that keeps the code equivalent to the pattern it replaced:
//
//	^(?!.*(?:--|\.\.))[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$
func TestValidateName(t *testing.T) {
	valid := []string{
		"a",
		"9",
		"acme",
		"acme-tools",
		"acme.db-tools",
		"a.b.c",
		"a-b.c-d",
		"a-.b", // mixed separators are permitted; only doubled ones are not
		"a.-b",
		strings.Repeat("a", 64),
	}
	for _, name := range valid {
		if err := schema.ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}

	invalid := map[string]string{
		"":                      "empty",
		"Acme":                  "lowercase",
		"acme--tools":           "consecutive hyphens",
		"acme---tools":          "consecutive hyphens",
		"acme..tools":           "consecutive periods",
		"-acme":                 "must start",
		"acme-":                 "must end",
		".acme":                 "must start",
		"acme.":                 "must end",
		"acme_tools":            "may only contain",
		"acme tools":            "may only contain",
		"acme/tools":            "may only contain",
		strings.Repeat("a", 65): "at most 64",
	}
	for name, want := range invalid {
		err := schema.ValidateName(name)
		if err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error mentioning %q", name, want)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateName(%q) = %q, want it to mention %q", name, err, want)
		}
	}
}

// Length is counted in runes, not bytes: a 64-character name of multi-byte
// characters would be rejected by a byte count even though it is the right
// length. Such a name fails on the character-set rule anyway, so this asserts
// the failure is reported for the right reason.
func TestValidateNameCountsRunes(t *testing.T) {
	err := schema.ValidateName(strings.Repeat("é", 30))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "at most") {
		t.Errorf("reported as too long, want a character-set error: %v", err)
	}
}
