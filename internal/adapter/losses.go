package adapter

import (
	"fmt"
	"sort"
)

// The loss catalogue: every way a component can fail to survive translation
// into a client, enumerated and explained.
//
// Silent degradation is the failure this project exists to fix, so "nothing was
// dropped without a reason" cannot be a matter of discipline — it has to be
// enforced. Three rules make it so, each with a test:
//
//   - Every loss code appears here with a meaning and, where one exists, a
//     remedy.
//   - Every adapter declares the codes it can emit, so what a client might not
//     carry is knowable before installing anything.
//   - An adapter may not emit a code it did not declare. That is what stops a
//     new drop being added quietly: a new failure mode must be catalogued and
//     declared before it can reach a user.
//
// The Expected flag carries a distinction that matters to whoever reads a
// report. Some losses are permanent facts about an ecosystem where clients
// genuinely differ — Gemini CLI has no skills mechanism, and no amount of
// effort changes that. Others mean something is wrong and can be fixed. A user
// looking at six warnings should be able to tell which two deserve their
// attention.

// LossInfo documents one loss code.
type LossInfo struct {
	Code  string `json:"code"`
	Title string `json:"title"`
	// Meaning states what actually happened, in terms of the component.
	Meaning string `json:"meaning"`
	// Expected marks a loss that follows from a client's design rather than
	// from a problem. An expected loss is still reported — it is the reason a
	// plugin behaves differently in one client — but it is not a fault.
	Expected bool `json:"expected"`
	// Remedy is what the user can do, empty when nothing can be done.
	Remedy string `json:"remedy,omitempty"`
}

var lossCatalog = map[string]LossInfo{
	LossSkillsUnsupported: {
		Code:     LossSkillsUnsupported,
		Title:    "client has no skills mechanism",
		Meaning:  "the plugin's skills were not installed, because this client has no concept of them at all.",
		Expected: true,
		Remedy:   "use a client that loads skills if the plugin's value is in its skills rather than its servers.",
	},
	LossSkillsUndocumented: {
		Code:  LossSkillsUndocumented,
		Title: "skills location is undocumented",
		Meaning: "the plugin's skills were not installed. This client loads Agent Plugins, but its vendor has not " +
			"published where a portable package is installed, and we will not write to a path we have not verified.",
		Expected: true,
		Remedy:   "install the plugin through the client's own mechanism until the location is documented and measured.",
	},
	LossTransportUnsupported: {
		Code:     LossTransportUnsupported,
		Title:    "transport not supported by this client",
		Meaning:  "one MCP server was not installed, because the client cannot connect over the transport the server declares.",
		Expected: true,
		Remedy:   "ask the plugin author for a server using a transport this client supports.",
	},
	LossExtensionsDropped: {
		Code:  LossExtensionsDropped,
		Title: "extension namespace not carried",
		Meaning: "client-specific data under a reverse-domain namespace was not written. The specification gives " +
			"this data no portable meaning, and a client that does not own the namespace must ignore it (§8.1).",
		Expected: true,
	},
	LossNativeComponentDropped: {
		Code:  LossNativeComponentDropped,
		Title: "component has no equivalent in this client",
		Meaning: "a component the source dialect defines but Agent Plugins does not — hooks, agents, workflows and " +
			"the like — was not installed.",
		Expected: true,
	},
	LossFlatSkillRestructured: {
		Code:  LossFlatSkillRestructured,
		Title: "flat Markdown skill",
		Meaning: "a skill supplied as a single Markdown file rather than a directory containing SKILL.md. This " +
			"client accepts it, but Agent Plugins §7.1 does not, so it cannot be published portably as-is.",
		Expected: true,
		Remedy:   "move the file to skills/<name>/SKILL.md to make the plugin portable.",
	},
	LossCwdUnenforceable: {
		Code:  LossCwdUnenforceable,
		Title: "working directory could not be enforced",
		Meaning: "this client accepts a server's working directory and does not use it, so §7.2.1's rule that a " +
			"plugin server runs in the plugin root cannot be met by writing the value. agentbridge normally " +
			"routes such a server through its own launcher, which changes directory and then hands over; here " +
			"no launcher path was available.",
		Remedy: "make sure the agentbridge binary is on a stable path, or expect a server that resolves relative " +
			"paths to read files from wherever the client was started.",
	},
	LossSecretInPlaintext: {
		Code:  LossSecretInPlaintext,
		Title: "credential written as plaintext",
		Meaning: "a value that looks like a credential was written into a client configuration file, because " +
			"--allow-plaintext-secrets was given. §9.2 states that env values are visible package data.",
		Remedy: "store it with `agentbridge secret set`, then reference it as ${secret:name} in the plugin.",
	},
	LossSecretPlaintextRefused: {
		Code:  LossSecretPlaintextRefused,
		Title: "credential refused",
		Meaning: "an MCP server was not installed, because its environment holds a value that looks like a " +
			"credential and these files are routinely committed.",
		Remedy: "store it with `agentbridge secret set` and reference it, or re-run with --allow-plaintext-secrets.",
	},
	LossSecretMissing: {
		Code:  LossSecretMissing,
		Title: "referenced secret is not stored",
		Meaning: "an MCP server was not installed, because it references a secret that the credential store does " +
			"not have. Installing it would produce a server that fails on start with no visible diagnostic.",
		Remedy: "run `agentbridge secret set <name>`.",
	},
	LossSecretNoLauncher: {
		Code:  LossSecretNoLauncher,
		Title: "no launcher available for secret injection",
		Meaning: "an MCP server was not installed, because its secrets can only be supplied at launch and the " +
			"agentbridge binary could not be located.",
		Remedy: "reinstall agentbridge so its path can be resolved.",
	},
	LossSecretPartialRef: {
		Code:  LossSecretPartialRef,
		Title: "secret reference embedded in a larger value",
		Meaning: "an MCP server was not installed, because a reference appears inside a longer string. It cannot " +
			"be resolved, and §9.2 requires unrecognized placeholder text to be passed through literally — so the " +
			"server would receive the placeholder itself.",
		Remedy: "make the reference the whole value.",
	},
}

// LossCatalog returns every documented loss, ordered by code.
func LossCatalog() []LossInfo {
	out := make([]LossInfo, 0, len(lossCatalog))
	for _, info := range lossCatalog {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// LookupLoss returns the documentation for a loss code.
func LookupLoss(code string) (LossInfo, bool) {
	info, ok := lossCatalog[code]
	return info, ok
}

// CommonLosses are the codes any adapter can emit, because they arise from the
// plugin or the policy rather than from the client's own capabilities.
func CommonLosses() []string {
	return []string{
		LossExtensionsDropped,
		LossSecretInPlaintext,
		LossSecretMissing,
		LossSecretNoLauncher,
		LossSecretPartialRef,
		LossSecretPlaintextRefused,
		LossTransportUnsupported,
	}
}

// DeclaredLosses combines the common codes with a client's own.
func DeclaredLosses(extra ...string) []string {
	out := append(CommonLosses(), extra...)
	sort.Strings(out)
	return out
}

// Validate reports loss codes that are not catalogued.
//
// Called by tests rather than at runtime: the invariant is about what the code
// can emit, which is knowable without waiting for a user to hit it.
func (f Fidelity) Validate() error {
	for _, l := range f.Losses {
		if _, ok := lossCatalog[l.Code]; !ok {
			return fmt.Errorf("loss code %q is not in the catalogue; add it to internal/adapter/losses.go "+
				"and declare it on the adapters that can emit it", l.Code)
		}
	}
	return nil
}

// ValidateAgainst reports losses whose codes the client did not declare.
//
// This is the rule that keeps the catalogue honest. Without it, a new drop can
// be added to an adapter and reported perfectly at runtime while never
// appearing in the list of what that client might not carry — which is exactly
// the surprise the fidelity report exists to prevent.
func (f Fidelity) ValidateAgainst(c Client) error {
	declared := map[string]bool{}
	for _, code := range c.Losses {
		declared[code] = true
	}
	for _, l := range f.Losses {
		if !declared[l.Code] {
			return fmt.Errorf("%s emitted loss %q without declaring it; add it to the client's Losses list",
				c.ID, l.Code)
		}
	}
	return nil
}

// Unexpected returns the losses that indicate a problem rather than a
// difference between clients.
func (f Fidelity) Unexpected() []Loss {
	var out []Loss
	for _, l := range f.Losses {
		if info, ok := lossCatalog[l.Code]; ok && info.Expected {
			continue
		}
		out = append(out, l)
	}
	return out
}
