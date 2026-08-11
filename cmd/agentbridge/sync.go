package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	"github.com/agentbridge/agentbridge/internal/lockfile"
	"github.com/agentbridge/agentbridge/internal/scanner"
	"github.com/agentbridge/agentbridge/internal/workspace"
)

// syncFlags are the options both sync and update take.
type syncFlags struct {
	update       bool
	dryRun       bool
	offline      bool
	prune        bool
	clientList   string
	scope        string
	asJSON       bool
	allowFlagged bool
}

// syncCmd makes the machine match what the manifests declare.
func syncCmd(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	var opts syncFlags
	fs.BoolVar(&opts.dryRun, "dry-run", false, "show what would change, write nothing")
	fs.BoolVar(&opts.offline, "offline", false, "never access the network")
	fs.BoolVar(&opts.prune, "prune", true, "remove plugins a manifest no longer declares")
	fs.StringVar(&opts.clientList, "client", "", "comma-separated client ids")
	fs.StringVar(&opts.scope, "scope", "", "user or project")
	fs.BoolVar(&opts.asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&opts.allowFlagged, "allow-flagged-content", false, "install even when the content scan reports high-severity findings")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	return runSync(fs, opts)
}

// updateCmd re-resolves every declared reference, ignoring the pins.
func updateCmd(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	opts := syncFlags{update: true}
	fs.BoolVar(&opts.dryRun, "dry-run", false, "show what would change, write nothing")
	fs.StringVar(&opts.clientList, "client", "", "comma-separated client ids")
	fs.StringVar(&opts.scope, "scope", "", "user or project")
	fs.BoolVar(&opts.asJSON, "json", false, "machine-readable output")
	fs.BoolVar(&opts.allowFlagged, "allow-flagged-content", false, "install even when the content scan reports high-severity findings")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	return runSync(fs, opts)
}

func runSync(fs *flag.FlagSet, opts syncFlags) error {
	if fs.NArg() != 0 {
		return fmt.Errorf("%s takes no arguments; it acts on the declared manifests", fs.Name())
	}
	update, dryRun, asJSON := opts.update, opts.dryRun, opts.asJSON

	env, err := currentEnv()
	if err != nil {
		return err
	}
	res, workspaces, err := loadResolution(env)
	if err != nil {
		return err
	}
	if len(res.Entries) == 0 {
		// --json must always produce JSON, including for the empty case. A
		// script piping to jq should not have to special-case a workspace that
		// happens to declare nothing.
		if asJSON {
			return emitJSON(&workspace.Result{Locks: map[string]*lockfile.Lock{}})
		}
		fmt.Printf("Nothing declared. Create %s, or use `agentbridge install --save <ref>`.\n",
			workspaces.project.ManifestPath())
		return nil
	}

	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		return err
	}

	result, err := workspace.Sync(context.Background(), res, store, workspace.Options{
		Env:          env,
		Update:       update,
		DryRun:       dryRun,
		Offline:      opts.offline,
		Prune:        opts.prune,
		Clients:      splitList(opts.clientList),
		Scope:        adapter.Scope(opts.scope),
		Plan:         planOptions(false),
		AllowFlagged: opts.allowFlagged,
	})
	if err != nil {
		return err
	}

	if asJSON {
		if err := emitJSON(result); err != nil {
			return err
		}
	} else {
		printSync(result, update, dryRun)
	}

	if result.Failed() {
		return fmt.Errorf("%d plugin(s) failed", countFailed(result))
	}
	return nil
}

type workspaces struct {
	user    lockfile.Workspace
	project lockfile.Workspace
}

// loadResolution merges the user and project manifests.
func loadResolution(env adapter.Env) (lockfile.Resolution, workspaces, error) {
	var ws workspaces

	ws.user = lockfile.UserWorkspace(adapterreg.StateDir(env))
	projectDir := env.ProjectDir
	if root, ok := lockfile.FindProjectRoot(projectDir); ok {
		projectDir = root
	}
	ws.project = lockfile.ProjectWorkspace(projectDir)

	userManifest, err := lockfile.LoadManifest(ws.user.ManifestPath())
	if err != nil {
		return lockfile.Resolution{}, ws, err
	}
	projectManifest, err := lockfile.LoadManifest(ws.project.ManifestPath())
	if err != nil {
		return lockfile.Resolution{}, ws, err
	}

	return lockfile.Merge(userManifest, projectManifest, ws.user, ws.project), ws, nil
}

func countFailed(r *workspace.Result) int {
	n := 0
	for _, p := range r.Plugins {
		if p.Err != nil {
			n++
		}
	}
	return n
}

func printSync(r *workspace.Result, update, dryRun bool) {
	for _, p := range r.Plugins {
		if p.Err != nil {
			fmt.Printf("  xx %-28s %v\n", p.Entry.Source, p.Err)
			continue
		}

		state := "=="
		if planChanged(p) {
			state = "ok"
		}
		fmt.Printf("  %s %-28s %s\n", state, p.Plugin.Name, shortRef(p.Resolved.Pinned()))

		for _, plan := range p.Plans {
			marker := " "
			if plan.Fidelity.Degraded() {
				marker = "!"
			}
			fmt.Printf("     %s %-14s %-9s skills %-7s mcp %s\n",
				marker, plan.Installation.Client.ID, plan.Installation.Scope,
				plan.Fidelity.Skills, plan.Fidelity.MCPServers)
		}

		// Findings that did not block still belong in the output: the whole
		// point of scanning on sync is that the content of a pinned plugin can
		// change under a reader who is only watching versions.
		if p.Scan != nil {
			if n := len(p.Scan.AtLeast(scanner.Low)); n > 0 {
				fmt.Printf("     ? %d content finding(s), worst %s — `agentbridge scan %s`\n",
					n, p.Scan.Worst(), shortRef(p.Entry.Source))
			}
		}
	}

	for _, name := range r.Pruned {
		fmt.Printf("  -- %-28s no longer declared, removed\n", name)
	}

	printLockDiff(r.Diff)

	switch {
	case dryRun:
		fmt.Printf("\nDry run: nothing was written.\n")
	case r.Diff.Empty() && len(r.Pruned) == 0:
		fmt.Printf("\nAlready up to date.\n")
	case update:
		fmt.Printf("\nUpdated.\n")
	default:
		fmt.Printf("\nSynced.\n")
	}
}

// printLockDiff reports how the lock moved, leading with capability changes.
//
// A version bump that grants an agent the ability to execute processes is a
// different event from one that does not, and the difference is invisible
// unless something says so out loud.
func printLockDiff(d lockfile.Diff) {
	if d.Empty() {
		return
	}
	fmt.Printf("\n  lock changes\n")

	for _, p := range d.Added {
		fmt.Printf("    + %-24s %s\n", p.Name, strings.Join(p.Capabilities, ", "))
	}
	for _, p := range d.Removed {
		fmt.Printf("    - %s\n", p.Name)
	}
	for _, c := range d.Changed {
		fmt.Printf("    ~ %-24s %s\n", c.After.Name, shortDigest(c.After.TreeDigest))
		if c.Before.Version != c.After.Version && c.After.Version != "" {
			fmt.Printf("        version %s -> %s\n", orNone(c.Before.Version), c.After.Version)
		}
		if gained := c.CapabilitiesGained(); len(gained) > 0 {
			fmt.Printf("        !! gains capability: %s\n", strings.Join(gained, ", "))
		}
		for _, s := range addedStrings(c.Before.Skills, c.After.Skills) {
			fmt.Printf("        + skill %s\n", s)
		}
		for _, s := range addedStrings(serverNames(c.Before.Servers), serverNames(c.After.Servers)) {
			fmt.Printf("        + server %s\n", s)
		}
	}
}

func addedStrings(before, after []string) []string {
	had := map[string]bool{}
	for _, s := range before {
		had[s] = true
	}
	var out []string
	for _, s := range after {
		if !had[s] {
			out = append(out, s)
		}
	}
	return out
}

func shortRef(ref string) string {
	const max = 72
	if len(ref) <= max {
		return ref
	}
	return "…" + ref[len(ref)-max:]
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// saveToManifest records an installed reference so it can be synced later.
func saveToManifest(env adapter.Env, ref string, clients []string, scope, toScope string) (string, error) {
	_, ws, err := loadResolution(env)
	if err != nil {
		return "", err
	}

	target := ws.project
	if toScope == string(lockfile.ScopeUser) {
		target = ws.user
	}

	m, err := lockfile.LoadManifest(target.ManifestPath())
	if err != nil {
		return "", err
	}
	m.Add(lockfile.Entry{Source: ref, Clients: clients, Scope: scope})
	if err := m.Save(); err != nil {
		return "", err
	}
	return target.ManifestPath(), nil
}

// planChanged reports whether any client plan for this plugin would alter
// anything, so an already-converged sync can say so instead of implying work.
func planChanged(p workspace.PluginResult) bool {
	for _, plan := range p.Plans {
		if plan.Changed() {
			return true
		}
	}
	return false
}

func serverNames(servers []lockfile.LockedServer) []string {
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.Name
	}
	return out
}
