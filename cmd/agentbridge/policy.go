package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"sort"

	"github.com/agentbridgehq/agentbridge/internal/adapter"
	"github.com/agentbridgehq/agentbridge/internal/adapter/receipt"
	adapterreg "github.com/agentbridgehq/agentbridge/internal/adapter/registry"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/lockfile"
	"github.com/agentbridgehq/agentbridge/internal/policy"
	"github.com/agentbridgehq/agentbridge/internal/source"
	"github.com/agentbridgehq/agentbridge/internal/workspace"
)

// policyRefusal is the machine-readable form of a blocked install.
//
// It carries the violations rather than a message about them, for the same
// reason the scan refusal carries its findings: a script that can see only
// "refused" has to run the tool a second time to learn why, and a CI log that
// says "policy" without saying which rule sends someone reading YAML by hand.
type policyRefusal struct {
	Plugin     string             `json:"plugin"`
	Refused    bool               `json:"refused"`
	Reason     string             `json:"reason"`
	Violations []policy.Violation `json:"violations"`
	Remedy     string             `json:"remedy"`
}

// loadPolicies finds every policy that applies to the working directory.
func loadPolicies(env adapter.Env) ([]*policy.Policy, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return policy.Discover(wd, adapterreg.StateDir(env))
}

// policyGate refuses an install that a policy forbids.
//
// There is deliberately no flag to override it. A rule a developer can step
// past on the command line is not a policy, it is a warning with extra
// ceremony — and the person who wrote the rule is not there to be asked. The
// way to make an exception is to edit the file, which happens in a pull
// request where somebody can see it.
//
// What this is not: a sandbox. It binds the machines that run this binary
// against the policy files it can find. Someone who wants to install a plugin
// by hand still can, and nothing here pretends otherwise — the value is in
// making the supported path the governed one, and in producing evidence that
// it was.
func policyGate(p *ir.Plugin, resolved *source.Resolved, policies []*policy.Policy, quiet bool) error {
	violations := policy.Check(policies, workspace.PolicySubject(p, resolved))
	if len(violations) == 0 {
		return nil
	}

	if quiet {
		if err := emitJSON(policyRefusal{
			Plugin:     p.Name,
			Refused:    true,
			Reason:     "blocked by policy",
			Violations: violations,
			Remedy:     "the rule lives in the policy file named on each violation; changing it is a pull request, not a flag",
		}); err != nil {
			return err
		}
		return fmt.Errorf("%s is blocked by %d policy rule(s)", p.Name, len(violations))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n%s is blocked by policy:\n\n", p.Name)
	for _, v := range violations {
		fmt.Fprintf(&b, "  %-20s %s\n", v.Rule, v.Detail)
		fmt.Fprintf(&b, "  %-20s %s\n\n", "", v.Policy)
	}
	b.WriteString("There is no flag for this. The rule is in the file above, and\n")
	b.WriteString("changing it is a pull request somebody can review.\n")
	fmt.Fprint(os.Stderr, b.String())

	return fmt.Errorf("%s is blocked by %d policy rule(s)", p.Name, len(violations))
}

// policyCmd shows what is in force, and where each rule comes from.
//
// A policy nobody can inspect is a policy nobody trusts. This is also the
// answer to the auditor's question — "show me the controls" — without asking
// anyone to reconstruct them from several files by hand.
func policyCmd(args []string) error {
	fs := flag.NewFlagSet("policy", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	audit := fs.Bool("audit", false, "check what is already installed against the policy in force")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	env, err := currentEnv()
	if err != nil {
		return err
	}
	policies, err := loadPolicies(env)
	if err != nil {
		return err
	}

	if *audit {
		return auditInstalled(env, policies, *asJSON)
	}

	if *asJSON {
		return emitJSON(map[string]any{"policies": policies})
	}

	if len(policies) == 0 {
		fmt.Printf("No policy in force.\n\n")
		fmt.Printf("A policy is a file named %s, in this repository or in\n", policy.FileName)
		fmt.Printf("%s. It restricts what may be installed on the\n", adapterreg.StateDir(env))
		fmt.Printf("machines that run this tool:\n\n")
		fmt.Print(examplePolicy)
		return nil
	}

	for _, p := range policies {
		fmt.Printf("%s\n", p.Path)
		if len(p.Sources.Allow) > 0 {
			fmt.Printf("  sources.allow      %s\n", strings.Join(p.Sources.Allow, ", "))
		}
		if len(p.Sources.Deny) > 0 {
			fmt.Printf("  sources.deny       %s\n", strings.Join(p.Sources.Deny, ", "))
		}
		fmt.Printf("  sources.local      %s\n", p.Sources.Local)
		if len(p.Capabilities.Deny) > 0 {
			fmt.Printf("  capabilities.deny  %s\n", strings.Join(p.Capabilities.Deny, ", "))
		}
		if len(p.Plugins.Deny) > 0 {
			fmt.Printf("  plugins.deny       %s\n", strings.Join(p.Plugins.Deny, ", "))
		}
		fmt.Println()
	}

	fmt.Printf("Every policy listed is enforced. They restrict and never grant, so a\n")
	fmt.Printf("second file can only narrow what the first allows.\n")
	return nil
}

const examplePolicy = `  version: 1
  sources:
    allow: ["github.com/acme/*"]     # * stops at a /, ** crosses it
    deny:  ["github.com/acme/experimental-*"]
    local: allow                     # or deny, to close the /tmp route
  capabilities:
    deny: [network]                  # inferred from the package, not claimed by it
  plugins:
    deny: []
`

// auditInstalled checks what is already on this machine against the policy.
//
// The gates answer "may I install this?". This answers "what did we install
// before the rule existed?", which is the question an organisation actually
// has — a policy adopted on Monday says nothing about the six months of
// installs preceding it, and a control with no way to see its own violations
// is a control nobody can act on.
//
// It reports rather than removes. Uninstalling on a policy change would mean a
// rule edit silently reaching into every developer's machine, and the blast
// radius of a typo in a YAML file should not be that. `agentbridge remove` is
// one command away and it is a decision somebody should make.
func auditInstalled(env adapter.Env, policies []*policy.Policy, asJSON bool) error {
	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		return err
	}

	// Capabilities are not in a receipt — they are inferred at resolve time and
	// recorded in the lock. Reading them from there keeps the audit honest
	// about what it can see: a plugin installed ad hoc, with no lock entry, is
	// checked on name and source but not on capability, and says so.
	caps := map[string][]string{}
	for _, ws := range policyWorkspaces(env) {
		lock, err := lockfile.LoadLock(ws.LockPath())
		if err != nil {
			continue
		}
		for _, p := range lock.Plugins {
			caps[p.Name] = p.Capabilities
		}
	}

	type finding struct {
		Plugin     string             `json:"plugin"`
		Clients    []string           `json:"clients"`
		Violations []policy.Violation `json:"violations"`
		Partial    bool               `json:"capabilitiesUnknown"`
	}

	seen := map[string]bool{}
	var findings []finding
	checked := 0

	for _, e := range store.All() {
		if seen[e.Plugin] {
			continue
		}
		seen[e.Plugin] = true
		checked++

		recorded, known := caps[e.Plugin]
		s := policy.Subject{
			Name:         e.Plugin,
			Source:       e.SourceIdentity,
			Local:        e.SourceIdentity == "" || strings.HasPrefix(e.SourceIdentity, "/"),
			Capabilities: capabilitiesFromNames(recorded),
		}
		v := policy.Check(policies, s)
		if len(v) == 0 {
			continue
		}
		var clients []string
		for _, r := range store.ForPlugin(e.Plugin) {
			clients = append(clients, r.Client)
		}
		sort.Strings(clients)
		findings = append(findings, finding{
			Plugin: e.Plugin, Clients: clients, Violations: v, Partial: !known,
		})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Plugin < findings[j].Plugin })

	if asJSON {
		if err := emitJSON(map[string]any{
			"checked": checked, "findings": findings,
		}); err != nil {
			return err
		}
		// The exit code carries as much as the document does. A CI job that
		// pipes this to jq still decides whether to fail on the status, and an
		// audit that reports breaches while exiting 0 is one nobody notices.
		if len(findings) > 0 {
			return fmt.Errorf("%d installed plugin(s) breach the policy", len(findings))
		}
		return nil
	}

	if len(policies) == 0 {
		fmt.Printf("No policy in force, so there is nothing to audit against.\n")
		return nil
	}
	if checked == 0 {
		fmt.Printf("Nothing is installed by agentbridge on this machine.\n")
		return nil
	}
	if len(findings) == 0 {
		fmt.Printf("%d installed plugin(s), none in breach of the policy in force.\n", checked)
		return nil
	}

	fmt.Printf("%d of %d installed plugin(s) breach the policy in force:\n\n", len(findings), checked)
	for _, f := range findings {
		fmt.Printf("  %s  (%s)\n", f.Plugin, strings.Join(f.Clients, ", "))
		for _, v := range f.Violations {
			fmt.Printf("    %-20s %s\n", v.Rule, v.Detail)
		}
		if f.Partial {
			fmt.Printf("    %-20s no lock entry, so capabilities were not checked\n", "note")
		}
		fmt.Println()
	}
	fmt.Printf("Nothing was changed. Remove one with `agentbridge remove <name>`.\n")
	return fmt.Errorf("%d installed plugin(s) breach the policy", len(findings))
}

// policyWorkspaces lists the lock files an audit should read, using the same
// project-root discovery `sync` uses so the two agree on which project this is.
func policyWorkspaces(env adapter.Env) []lockfile.Workspace {
	projectDir := env.ProjectDir
	if root, ok := lockfile.FindProjectRoot(projectDir); ok {
		projectDir = root
	}
	return []lockfile.Workspace{
		lockfile.UserWorkspace(adapterreg.StateDir(env)),
		lockfile.ProjectWorkspace(projectDir),
	}
}

// capabilitiesFromNames turns a lock's capability list back into the struct
// the policy engine checks.
func capabilitiesFromNames(names []string) ir.Capabilities {
	var c ir.Capabilities
	for _, n := range names {
		switch ir.Capability(n) {
		case ir.CapExec:
			c.Exec = true
		case ir.CapNetwork:
			c.Network = true
		case ir.CapFilesystem:
			c.Filesystem = true
		case ir.CapSecrets:
			c.Secrets = true
		}
	}
	return c
}
