package registry_test

import (
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/adapter"
	"github.com/agentbridgehq/agentbridge/internal/adapter/registry"
)

// The declaration rule enforced against the real adapters, not a stub.
//
// This is what stops a new drop being added quietly. An adapter can report a
// loss perfectly at runtime and still be a surprise, if the list of what that
// client might not carry never mentioned it.
func TestAdaptersOnlyEmitDeclaredLosses(t *testing.T) {
	env := fakeMachine(t, "claude-code", "cursor", "vscode", "codex", "gemini-cli")
	plugin, src := loadFixture(t, ccFixture)

	// Both policies, since secret handling produces its own losses and the
	// default path refuses where the permissive one writes.
	for name, opts := range map[string]adapter.PlanOptions{
		"default":          {},
		"allowing secrets": {AllowPlaintextSecrets: true},
	} {
		t.Run(name, func(t *testing.T) {
			plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range plans {
				if err := p.Fidelity.Validate(); err != nil {
					t.Errorf("%s: %v", p.Installation.Client.ID, err)
				}
				if err := p.Fidelity.ValidateAgainst(p.Installation.Client); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

// Every adapter must declare something, and only codes that exist.
func TestEveryAdapterDeclaresItsLosses(t *testing.T) {
	env := fakeMachine(t)

	for _, a := range registry.Adapters(env) {
		c := a.Client()
		if len(c.Losses) == 0 {
			t.Errorf("%s declares no losses; every client differs from the portable format somehow", c.ID)
			continue
		}
		for _, code := range c.Losses {
			if _, ok := adapter.LookupLoss(code); !ok {
				t.Errorf("%s declares %q, which is not catalogued", c.ID, code)
			}
		}
	}
}

// A client that cannot take skills must say which way it cannot: a hard "no
// mechanism exists" and "the vendor has not documented where" are different
// facts, and only one of them might change.
func TestSkillLossMatchesDeclaredSupport(t *testing.T) {
	env := fakeMachine(t)

	for _, a := range registry.Adapters(env) {
		c := a.Client()
		declared := map[string]bool{}
		for _, code := range c.Losses {
			declared[code] = true
		}

		switch c.Skills {
		case adapter.SupportNone:
			if !declared[adapter.LossSkillsUnsupported] {
				t.Errorf("%s has no skills mechanism but does not declare %s", c.ID, adapter.LossSkillsUnsupported)
			}
		case adapter.SupportUndocumented:
			if !declared[adapter.LossSkillsUndocumented] {
				t.Errorf("%s has an undocumented skills location but does not declare %s", c.ID, adapter.LossSkillsUndocumented)
			}
		}
	}
}
