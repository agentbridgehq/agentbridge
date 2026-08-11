// Command agentbridge is the AgentBridge CLI.
//
// It loads a plugin in any supported dialect and installs it into every agent
// client on the machine, reporting exactly what each client received and what
// it could not take. Around that: pinned fetching from a repository or an OCI
// registry, a reviewable lockfile, secrets held in the OS credential store, and
// a scanner that reads the instruction text before it reaches an agent.
//
// What remains is not code — no release has been cut and no third-party client
// has been measured. See MVP.md, and README's "What is not done".
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	"github.com/agentbridge/agentbridge/internal/diag"
	"github.com/agentbridge/agentbridge/internal/importer"
	"github.com/agentbridge/agentbridge/internal/importer/registry"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
	"github.com/agentbridge/agentbridge/internal/schema"
	"github.com/agentbridge/agentbridge/internal/secrets"
	"github.com/agentbridge/agentbridge/internal/source"
	"github.com/agentbridge/agentbridge/internal/validate"
)

// version is stamped at release time by the build; a development build says so
// rather than claiming a release it is not.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "agentbridge: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "inspect":
		return inspect(args[1:])
	case "install":
		return install(args[1:])
	case "remove", "uninstall":
		return remove(args[1:])
	case "validate":
		return validateCmd(args[1:])
	case "scan":
		return scanCmd(args[1:])
	case "doctor":
		return doctorCmd(args[1:])
	case "sync":
		return syncCmd(args[1:])
	case "update":
		return updateCmd(args[1:])
	case "list":
		return list(args[1:])
	case "conformance":
		return conformanceCmd(args[1:])
	case "losses":
		return lossesCmd(args[1:])
	case "cache":
		return cacheCmd(args[1:])
	case "secret":
		return secretCmd(args[1:])
	case "run":
		return runServer(args[1:])
	case "clients":
		return clients(args[1:])
	case "version", "--version", "-v":
		return versionCmd(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `agentbridge - the supply chain for agent extensions

Usage:
  agentbridge clients                  List agent clients detected on this machine
  agentbridge inspect <dir>            Load a plugin and print its normalized form
  agentbridge validate <dir>           Check a plugin against Agent Plugins v1.0.0
  agentbridge scan <ref>               Read a plugin's instructions for content that steers an agent
  agentbridge doctor [plugin]          Explain why a plugin is not doing anything
  agentbridge install <ref>            Install a plugin into every detected client
  agentbridge remove <name>            Remove a plugin from the clients it was installed into
  agentbridge sync                     Make this machine match agentbridge.yaml + .lock
  agentbridge update                   Re-resolve declared refs and rewrite the lock
  agentbridge list                     List plugins installed by agentbridge
  agentbridge cache [--clear]          Show or clear the fetched-package cache
  agentbridge losses                   What each client might not carry, and why
  agentbridge conformance [--list]     Run the Agent Plugins conformance corpus
  agentbridge version                  Print the version and target spec version
  agentbridge secret set|list|rm|scan  Keep credentials out of client configs
  agentbridge run --secret K=name -- <cmd>   Launch a server with secrets injected

The run command is written into client configs by install; you rarely type it.
It is listed because you will see it in a config and want to know what it is.

A <ref> is a local directory, a repository, or a registry artifact:
  ./plugins/db                          a directory on this machine
  github.com/org/repo@v1.2.0            pinned to a tag
  https://github.com/org/repo@main#plugins/db   branch and subdirectory
  oci://ghcr.io/org/plugin:v1.2.0       an OCI registry, resolved to a digest
  oci://ghcr.io/org/plugin@sha256:…     pinned to an exact manifest

Common flags:
  --dry-run          Show exactly what would change, write nothing
  --offline          Never touch the network; only pinned, cached refs resolve
  --digest sha256:…  Require the package to match this content address
  --client=a,b       Restrict to these clients
  --scope=user|project
  --json             Machine-readable output

Flags may appear before or after the argument. Every command supports --json.

This is a pre-release build: no release has been cut and no third-party client
has been measured against the conformance corpus. See MVP.md.
`)
}

// ---------------------------------------------------------------- inspect

func inspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit the internal representation as JSON")
	dialect := fs.String("dialect", "", "force a dialect instead of detecting it")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("inspect takes exactly one directory")
	}

	result, err := open(positional[0], *dialect)
	if err != nil {
		return err
	}

	if *asJSON {
		return emitJSON(map[string]any{"plugin": result.Plugin, "diagnostics": result.Diagnostics})
	}
	return report(result)
}

func open(dir, dialect string) (*importer.Result, error) {
	if dialect != "" {
		return registry.OpenAs(dir, ir.Dialect(dialect))
	}
	return registry.Open(dir)
}

// ---------------------------------------------------------------- validate

// validateCmd is the author-facing counterpart to inspect.
//
// The loader is deliberately forgiving, because the specification requires a
// client to load what it can. An author wants the opposite: everything a
// conformant client is obliged to tolerate, plus the requirements that bind the
// author and which therefore no client will ever report — most importantly the
// normative rule that env values and headers are visible package data and MUST
// NOT carry secrets.
func validateCmd(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	strict := fs.Bool("strict", false, "treat advisories as failures")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("validate takes exactly one plugin directory")
	}

	report, err := validate.Run(positional[0])
	if err != nil {
		return err
	}

	if *asJSON {
		raw, err := report.JSON()
		if err != nil {
			return err
		}
		fmt.Println(string(raw))
	} else {
		printReport(report)
	}

	if report.Count(validate.Violation) > 0 {
		if report.ForeignDialect() {
			return fmt.Errorf("cannot be published as an Agent Plugins %s package as it stands", schema.SpecVersion)
		}
		return fmt.Errorf("not conformant with Agent Plugins %s", schema.SpecVersion)
	}
	if *strict && report.Count(validate.Advisory) > 0 {
		return fmt.Errorf("%d advisory finding(s) with --strict", report.Count(validate.Advisory))
	}
	return nil
}

func printReport(r *validate.Report) {
	fmt.Printf("%s\n", r.Dir)
	if r.Name != "" {
		fmt.Printf("  plugin    %s\n", r.Name)
	}
	fmt.Printf("  dialect   %s\n", r.Dialect)
	fmt.Printf("  spec      Agent Plugins %s\n\n", schema.SpecVersion)

	if len(r.Findings) == 0 {
		fmt.Println("  no findings")
	}
	for _, sev := range []validate.Severity{validate.Violation, validate.Advisory, validate.Note} {
		marker := map[validate.Severity]string{
			validate.Violation: "x", validate.Advisory: "!", validate.Note: "-",
		}[sev]
		for _, f := range r.Findings {
			if f.Severity != sev {
				continue
			}
			section := f.Section
			if section != "" {
				section = " " + section
			}
			fmt.Printf("  %s%s %s\n", marker, section, f.Message)
			if f.Where != "" {
				fmt.Printf("      %s\n", f.Where)
			}
		}
	}

	fmt.Printf("\n  %d violation(s), %d advisory(ies), %d note(s)\n",
		r.Count(validate.Violation), r.Count(validate.Advisory), r.Count(validate.Note))
	switch {
	case r.Conformant() && r.ForeignDialect():
		fmt.Printf("  can be published as an Agent Plugins %s package\n", schema.SpecVersion)
	case r.Conformant():
		fmt.Printf("  conformant with Agent Plugins %s\n", schema.SpecVersion)
	}
}

// ---------------------------------------------------------------- install

func install(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change, write nothing")
	clientList := fs.String("client", "", "comma-separated client ids to install into")
	scope := fs.String("scope", "", "user or project")
	asJSON := fs.Bool("json", false, "machine-readable output")
	dialect := fs.String("dialect", "", "force a dialect instead of detecting it")
	digest := fs.String("digest", "", "require the package to match this tree digest")
	offline := fs.Bool("offline", false, "never access the network; only pinned, cached references resolve")
	refresh := fs.Bool("refresh", false, "ignore any cached copy and fetch again")
	allowPlaintext := fs.Bool("allow-plaintext-secrets", false, "write credential-looking values into client configs as-is")
	allowFlagged := fs.Bool("allow-flagged-content", false, "install even when the content scan reports high-severity findings")
	var classify classifierFlags
	registerClassifierFlags(fs, &classify)
	save := fs.Bool("save", false, "record the reference in agentbridge.yaml and the lock")
	saveTo := fs.String("save-to", "project", "which manifest --save writes to: project or user")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("install takes exactly one plugin reference")
	}
	ref := positional[0]

	env, err := currentEnv()
	if err != nil {
		return err
	}

	resolved, err := source.ResolveString(context.Background(), ref, source.Options{
		Cache:          source.NewCache(adapterreg.CacheDir(env)),
		ExpectedDigest: *digest,
		Offline:        *offline,
		Refresh:        *refresh,
	})
	if err != nil {
		return err
	}
	dir := resolved.Dir

	if !*asJSON && resolved.Ref.Kind != source.KindLocal {
		origin := resolved.ShortID()
		via := "fetched"
		if resolved.FromCache {
			via = "cached"
		}
		fmt.Printf("%s  %s %s\n\n", resolved.Ref.URL, via, origin)
	}

	result, err := open(dir, *dialect)
	if err != nil {
		return err
	}
	if result.Diagnostics.HasErrors() && !*asJSON {
		fmt.Fprintf(os.Stderr, "note: the plugin loaded with %d error diagnostic(s); run `agentbridge inspect %s` for detail\n\n",
			len(result.Diagnostics.Filter(diag.Error)), dir)
	}

	src, err := safepath.NewRoot(dir)
	if err != nil {
		return err
	}

	// Checked before anything is printed: showing a full fidelity report for an
	// install that cannot proceed wastes the reader's attention on a decision
	// that has already been made.
	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		return err
	}
	if err := adapterreg.CheckNameConflict(store, result.Plugin.Name, resolved.Identity()); err != nil {
		return err
	}
	model, err := classify.build(*offline)
	if err != nil {
		return err
	}
	if err := scanGate(src, result.Plugin, model, *allowFlagged, *asJSON); err != nil {
		return err
	}

	sel := adapterreg.Selection{Clients: splitList(*clientList), Scope: adapter.Scope(*scope)}
	plans, err := adapterreg.PlanInstall(env, result.Plugin, src, sel, planOptions(*allowPlaintext))
	if err != nil {
		return err
	}

	if *asJSON {
		return emitJSON(map[string]any{
			"plugin": result.Plugin.Name, "dryRun": *dryRun,
			"source": resolved, "plans": plans,
		})
	}

	printPlans(result.Plugin.Name, plans, *dryRun)
	if *dryRun {
		fmt.Printf("\nDry run: nothing was written.\n")
		return nil
	}

	if err := adapterreg.ApplyInstall(env, store, result.Plugin, plans, adapterreg.Provenance{
		Source:     resolved.Pinned(),
		TreeDigest: resolved.TreeDigest,
		Identity:   resolved.Identity(),
	}); err != nil {
		return err
	}
	if resolved.Ref.Kind != source.KindLocal {
		fmt.Printf("  pinned to %s\n", resolved.Pinned())
	}
	fmt.Printf("  digest    %s\n", resolved.TreeDigest)

	if *save {
		// The manifest records what the user asked for, not what it resolved
		// to. Saving the pinned form would freeze the declaration at one
		// commit and make `update` meaningless.
		path, err := saveToManifest(env, ref, splitList(*clientList), *scope, *saveTo)
		if err != nil {
			return err
		}
		fmt.Printf("  declared in %s — run `agentbridge sync` to record it in the lock\n", path)
	}
	fmt.Printf("\nInstalled %s into %d client target(s).\n", result.Plugin.Name, len(plans))
	return nil
}

// ---------------------------------------------------------------- remove

func remove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change, write nothing")
	clientList := fs.String("client", "", "comma-separated client ids to remove from")
	scope := fs.String("scope", "", "user or project")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("remove takes exactly one plugin name")
	}
	name := positional[0]

	env, err := currentEnv()
	if err != nil {
		return err
	}
	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		return err
	}

	sel := adapterreg.Selection{Clients: splitList(*clientList), Scope: adapter.Scope(*scope)}
	plans, err := adapterreg.PlanRemove(env, store, name, sel)
	if err != nil {
		return err
	}

	if *asJSON {
		return emitJSON(map[string]any{"plugin": name, "dryRun": *dryRun, "plans": plans})
	}

	printPlans(name, plans, *dryRun)
	if *dryRun {
		fmt.Printf("\nDry run: nothing was written.\n")
		return nil
	}
	if err := adapterreg.ApplyRemove(store, name, plans); err != nil {
		return err
	}
	fmt.Printf("\nRemoved %s from %d client target(s).\n", name, len(plans))
	return nil
}

// ---------------------------------------------------------------- list

func list(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	env, err := currentEnv()
	if err != nil {
		return err
	}
	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		return err
	}
	entries := store.All()

	if *asJSON {
		return emitJSON(entries)
	}
	if len(entries) == 0 {
		fmt.Println("No plugins installed by agentbridge.")
		return nil
	}
	fmt.Printf("%-24s %-14s %-9s %s\n", "PLUGIN", "CLIENT", "SCOPE", "SOURCE")
	for _, e := range entries {
		origin := e.Source
		if origin == "" {
			origin = "(local)"
		}
		fmt.Printf("%-24s %-14s %-9s %s\n", e.Plugin, e.Client, e.Scope, origin)
	}
	return nil
}

// ---------------------------------------------------------------- cache

func cacheCmd(args []string) error {
	fs := flag.NewFlagSet("cache", flag.ContinueOnError)
	clear := fs.Bool("clear", false, "remove every cached package")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	env, err := currentEnv()
	if err != nil {
		return err
	}
	c := source.NewCache(adapterreg.CacheDir(env))

	if *clear {
		if err := c.Clear(); err != nil {
			return err
		}
		if *asJSON {
			return emitJSON(map[string]any{"root": c.Root(), "cleared": true})
		}
		fmt.Printf("Cleared %s\n", c.Root())
		return nil
	}
	if *asJSON {
		return emitJSON(map[string]any{"root": c.Root(), "cleared": false})
	}
	fmt.Printf("%s\n", c.Root())
	return nil
}

// versionCmd reports the build and the specification it targets.
//
// The spec version is part of the answer, not decoration: "which agentbridge"
// and "which Agent Plugins" are different questions, and a machine deciding
// whether a package will load needs the second one.
func versionCmd(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *asJSON {
		return emitJSON(map[string]string{
			"version":     version,
			"specVersion": schema.SpecVersion,
		})
	}
	fmt.Printf("agentbridge %s (Agent Plugins %s)\n", version, schema.SpecVersion)
	return nil
}

// ---------------------------------------------------------------- clients

func clients(args []string) error {
	fs := flag.NewFlagSet("clients", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	all := fs.Bool("all", false, "include clients that were not detected")
	if err := fs.Parse(args); err != nil {
		return err
	}

	env, err := currentEnv()
	if err != nil {
		return err
	}
	found := adapterreg.Detect(env)

	if *asJSON {
		return emitJSON(found)
	}

	fmt.Printf("%-14s %-9s %-13s %-11s %s\n", "CLIENT", "SCOPE", "SKILLS", "MCP", "CONFIG")
	for _, inst := range found {
		target := inst.ConfigPath
		if target == "" {
			target = inst.PackageDir
		}
		fmt.Printf("%-14s %-9s %-13s %-11s %s\n",
			inst.Client.ID, inst.Scope, inst.Client.Skills, inst.Client.MCP, target)
	}
	if len(found) == 0 {
		fmt.Println("(none detected)")
	}

	if *all {
		fmt.Printf("\nAll known clients:\n")
		for _, a := range adapterreg.Adapters(env) {
			c := a.Client()
			fmt.Printf("  %-14s skills=%-13s mcp=%-11s %s\n", c.ID, c.Skills, c.MCP, c.ConfigDoc)
		}
	}

	// The undocumented cases are the honest gap, and saying so is more useful
	// than a blank column the user has to interpret.
	var undocumented []string
	for _, a := range adapterreg.Adapters(env) {
		if a.Client().Skills == adapter.SupportUndocumented {
			undocumented = append(undocumented, a.Client().Name)
		}
	}
	if len(undocumented) > 0 {
		fmt.Printf("\nSkills are not installed into %s: these clients load Agent Plugins,\n"+
			"but their vendors have not documented where packages go, and we will not\n"+
			"write to a guessed path. MCP servers are installed normally.\n",
			strings.Join(undocumented, ", "))
	}
	return nil
}

// ---------------------------------------------------------------- output

// printPlans renders the fidelity report: the per-client account of what
// landed and what did not. This is the output the whole product is arguing
// for, so it is the default view rather than something behind a flag.
func printPlans(pluginName string, plans []*adapter.Plan, showDiff bool) {
	fmt.Printf("%s\n\n", pluginName)

	for _, plan := range plans {
		inst := plan.Installation
		marker := "ok"
		if plan.Fidelity.Degraded() {
			marker = "!!"
		}
		if !plan.Changed() {
			marker = "=="
		}

		fmt.Printf("  %s %-14s %-9s skills %-7s mcp %s\n",
			marker, inst.Client.ID, inst.Scope,
			plan.Fidelity.Skills, plan.Fidelity.MCPServers)

		for _, loss := range plan.Fidelity.Losses {
			fmt.Printf("       %s\n", describeLoss(loss))
			fmt.Printf("         [%s]\n", loss.Code)
		}
		for _, note := range plan.SecretNotes {
			fmt.Printf("       - %s (%s)\n", note.Detail, note.Server)
		}
		for _, op := range plan.Ops {
			state := ""
			if op.Unchanged() {
				state = " [no change]"
			}
			fmt.Printf("       %s: %s%s\n", op.Note, op.Path, state)
		}

		if showDiff {
			for _, op := range plan.Ops {
				if op.Unchanged() {
					continue
				}
				d := adapter.Diff(op)
				if d == "" {
					continue
				}
				for _, line := range strings.Split(strings.TrimRight(d, "\n"), "\n") {
					fmt.Printf("       | %s\n", line)
				}
			}
		}
		fmt.Println()
	}

	fmt.Printf("  legend: ok carried in full · !! degraded, see reasons · == already up to date\n")
	fmt.Printf("          ! something to fix · - a difference between clients (`agentbridge losses`)\n")
}

func report(r *importer.Result) error {
	p := r.Plugin
	digest, err := p.Digest()
	if err != nil {
		return err
	}

	fmt.Printf("%s", p.Name)
	if p.Version != "" {
		fmt.Printf("@%s", p.Version)
	}
	fmt.Printf("\n  dialect   %s\n", p.Origin.Dialect)
	fmt.Printf("  digest    %s\n", digest)
	if p.Description != "" {
		fmt.Printf("  summary   %s\n", p.Description)
	}

	fmt.Printf("\n  skills      %d\n", len(p.Skills))
	for _, s := range p.Skills {
		kind := ""
		if s.Kind == ir.SkillFlatFile {
			kind = "  [flat file]"
		}
		fmt.Printf("    - %-24s %s%s\n", s.Name, s.Entrypoint, kind)
	}

	fmt.Printf("  mcp servers %d\n", len(p.MCPServers))
	for _, s := range p.MCPServers {
		target := s.URL
		if s.Transport == ir.TransportStdio {
			target = strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
		}
		fmt.Printf("    - %-24s %-16s %s\n", s.Name, s.Transport, target)
	}

	if caps := p.Capabilities.List(); len(caps) > 0 {
		fmt.Printf("\n  capabilities\n")
		for _, c := range caps {
			fmt.Printf("    ! %s\n", c)
		}
		for _, e := range p.Capabilities.Evidence {
			fmt.Printf("      %-11s %s\n", e.Capability, e.Reason)
		}
	}

	if len(p.Extensions) > 0 {
		fmt.Printf("\n  extension namespaces (preserved, not interpreted)\n")
		for ns := range p.Extensions {
			fmt.Printf("    - %s\n", ns)
		}
	}

	printDiagnostics(r.Diagnostics)
	return nil
}

func printDiagnostics(ds diag.Diagnostics) {
	if len(ds) == 0 {
		fmt.Printf("\n  no diagnostics\n")
		return
	}
	counts := map[diag.Severity]int{}
	for _, d := range ds {
		counts[d.Severity]++
	}
	fmt.Printf("\n  diagnostics: %d error(s), %d warning(s), %d note(s)\n",
		counts[diag.Error], counts[diag.Warning], counts[diag.Info])

	for _, sev := range []diag.Severity{diag.Error, diag.Warning, diag.Info} {
		for _, d := range ds.Filter(sev) {
			marker := map[diag.Severity]string{diag.Error: "x", diag.Warning: "!", diag.Info: "-"}[sev]
			where := d.Path
			if d.Component != "" {
				where = strings.TrimSpace(d.Path + " " + d.Component)
			}
			fmt.Printf("    %s [%s] %s\n", marker, d.Code, d.Message)
			if where != "" {
				fmt.Printf("        %s\n", where)
			}
		}
	}
}

// ---------------------------------------------------------------- helpers

func currentEnv() (adapter.Env, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return adapter.Env{}, err
	}
	return adapterreg.DefaultEnv(cwd)
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// planOptions builds the install-time policy the adapters need.
//
// The launcher path is resolved here rather than in the adapters so that every
// client writes the same absolute path, and so a test can substitute one.
func planOptions(allowPlaintext bool) adapter.PlanOptions {
	return adapter.PlanOptions{
		AllowPlaintextSecrets: allowPlaintext,
		Launcher:              adapter.LauncherPath(),
		Secrets:               secrets.Open(),
	}
}

// parseFlags parses a flag set while allowing flags to follow positional
// arguments.
//
// The standard library stops at the first non-flag, so `install ./plugin
// --dry-run` silently ignores the flag and installs for real. That is a trap
// people fall into constantly, and the failure is the worst kind: the command
// appears to work and does the opposite of what was asked. Parsing repeatedly
// and collecting positionals as they appear accepts both orders.
//
// Commands that must pass arguments through verbatim — `run`, which forwards
// everything after `--` to another program — parse directly instead.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}
