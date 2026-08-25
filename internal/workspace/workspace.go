// Package workspace turns declared intent into an installed machine.
//
// It is the layer where the pieces meet: a manifest says what is wanted, the
// source package fetches and pins it, the importers normalize it, the adapters
// write it, the lock records what happened, and receipts remember what we own
// so it can be taken back out again.
//
// Two properties are worth stating up front because everything here is shaped
// by them:
//
//   - **Convergence, not accumulation.** `sync` makes the machine match the
//     lock from whatever state it is in — installing what is missing, updating
//     what has moved, and removing what is no longer declared. A tool that only
//     ever adds leaves an install drifting further from its declaration every
//     week, which is precisely the state enterprises are trying to escape.
//
//   - **Removal is bounded by ownership.** Convergence may only remove plugins
//     a manifest previously declared. Anything installed by hand is left alone,
//     because a sync that deletes a developer's own work is a tool nobody runs
//     twice.
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/adapter"
	"github.com/agentbridgehq/agentbridge/internal/adapter/receipt"
	adapterreg "github.com/agentbridgehq/agentbridge/internal/adapter/registry"
	importreg "github.com/agentbridgehq/agentbridge/internal/importer/registry"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/lockfile"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
	"github.com/agentbridgehq/agentbridge/internal/scanner"
	"github.com/agentbridgehq/agentbridge/internal/source"
)

// Options control a sync.
type Options struct {
	Env adapter.Env
	// Update re-resolves every declared reference, ignoring the pins in the
	// lock. This is the difference between `sync` and `update`.
	Update bool
	// DryRun computes everything and writes nothing — not the client configs,
	// not the lock.
	DryRun bool
	// Offline forbids network access.
	Offline bool
	// Prune removes managed plugins that are no longer declared.
	Prune bool
	// Clients and Scope narrow which client targets are written.
	Clients []string
	Scope   adapter.Scope
	// Plan carries install-time secret policy into the adapters.
	Plan adapter.PlanOptions
	// Classifier, when set, adds a model pass to the content scan. Nil means
	// the scan is local and deterministic, which is the default everywhere.
	Classifier scanner.Classifier
	// AllowFlagged installs a plugin whose instruction text the scanner rates
	// high severity.
	//
	// Sync is gated as well as install because the interesting case is not the
	// first install — it is the plugin that was clean when it was reviewed and
	// gained an injected instruction three commits later. That change arrives
	// through sync, unattended, and is exactly what a lockfile alone cannot
	// catch: the digest changes honestly, and the content is the problem.
	AllowFlagged bool
}

// PluginResult is what happened to one declared plugin.
type PluginResult struct {
	Entry    lockfile.ScopedEntry
	Resolved *source.Resolved
	Plugin   *ir.Plugin
	Locked   lockfile.Locked
	Plans    []*adapter.Plan
	// Scan is the content scan for this plugin, reported whether or not it
	// blocked the install.
	Scan *scanner.Report
	// Delta is that scan compared against the findings accepted when this
	// plugin was last locked. Nil when the scan could not run.
	Delta *scanner.Delta
	// Err records a failure for this plugin alone. One plugin that cannot be
	// resolved must not stop the rest, for the same reason the specification
	// isolates component failures: a partially working machine beats a machine
	// that refused to change at all.
	Err error
}

// MarshalJSON renders the failure as text.
//
// Go's error interface marshals to `{}`, so without this a consumer of
// `sync --json` cannot tell that a plugin failed, let alone why — which matters
// more now that the most common failure is a content finding a script may well
// want to act on.
func (p PluginResult) MarshalJSON() ([]byte, error) {
	type plain PluginResult // shadow the method, or this recurses forever
	out := struct {
		plain
		Error string `json:"Error,omitempty"`
	}{plain: plain(p)}
	if p.Err != nil {
		out.Error = p.Err.Error()
	}
	return json.Marshal(out)
}

// Result is the outcome of a sync.
type Result struct {
	Plugins []PluginResult
	// Diff is how the lock changed.
	Diff lockfile.Diff
	// Pruned names plugins removed because they are no longer declared.
	Pruned []string
	// Locks are the lock files that would be written, keyed by path.
	Locks map[string]*lockfile.Lock
}

// Failed reports whether any plugin failed.
func (r *Result) Failed() bool {
	for _, p := range r.Plugins {
		if p.Err != nil {
			return true
		}
	}
	return false
}

// Sync makes the machine match the declared set.
func Sync(ctx context.Context, res lockfile.Resolution, store *receipt.Store, opts Options) (*Result, error) {
	out := &Result{Locks: map[string]*lockfile.Lock{}}
	cache := source.NewCache(adapterreg.CacheDir(opts.Env))

	// Every consulted workspace is loaded, not only those that declared
	// something. A manifest emptied of its last entry still has a lock, and
	// that lock still needs clearing.
	before := map[string]*lockfile.Lock{}
	paths := map[string]bool{}
	for _, w := range res.Workspaces {
		paths[w.LockPath()] = true
	}
	for _, e := range res.Entries {
		paths[e.Workspace.LockPath()] = true
	}

	for path := range paths {
		if _, done := out.Locks[path]; done {
			continue
		}
		loaded, err := lockfile.LoadLock(path)
		if err != nil {
			return nil, err
		}
		snapshot := *loaded
		snapshot.Plugins = append([]lockfile.Locked(nil), loaded.Plugins...)
		before[path] = &snapshot
		out.Locks[path] = loaded
	}

	// A lock that exists only because a workspace was consulted, and which was
	// already empty, should not be created on disk.
	defer func() {
		for path, lock := range out.Locks {
			if len(lock.Plugins) == 0 && len(before[path].Plugins) == 0 {
				delete(out.Locks, path)
			}
		}
	}()

	declared := map[string]bool{}

	for _, entry := range res.Entries {
		result := syncOne(ctx, entry, cache, store, opts)
		out.Plugins = append(out.Plugins, result)
		if result.Err != nil {
			continue
		}
		declared[result.Plugin.Name] = true
		out.Locks[entry.Workspace.LockPath()].Set(result.Locked)
	}

	// A lock entry whose source is no longer declared means someone removed a
	// line from the manifest. The lock follows the manifest, not the other way
	// round, so the stale entry goes.
	for _, lock := range out.Locks {
		var keep []lockfile.Locked
		for _, p := range lock.Plugins {
			if stillDeclared(res, p.Source) {
				keep = append(keep, p)
			}
		}
		lock.Plugins = keep
	}

	if opts.Prune {
		pruned, err := prune(opts.Env, store, declared, opts)
		if err != nil {
			return nil, err
		}
		out.Pruned = pruned
	}

	for path, lock := range out.Locks {
		out.Diff = mergeDiff(out.Diff, lockfile.DiffLocks(before[path], lock))
	}

	if opts.DryRun {
		return out, nil
	}

	for _, lock := range out.Locks {
		if err := lock.Save(); err != nil {
			return nil, err
		}
	}
	return out, store.Save()
}

// syncOne resolves, imports and installs a single declared plugin.
func syncOne(ctx context.Context, entry lockfile.ScopedEntry, cache *source.Cache, store *receipt.Store, opts Options) PluginResult {
	result := PluginResult{Entry: entry}

	lock, err := lockfile.LoadLock(entry.Workspace.LockPath())
	if err != nil {
		result.Err = err
		return result
	}

	// The pin is the whole point of the lock: unless asked to update, resolve
	// the commit that was recorded rather than whatever the declared branch or
	// tag points at now, and require the bytes to match what was recorded.
	ref := entry.Source
	expected := ""
	if locked, ok := lock.FindBySource(entry.Source); ok && !opts.Update {
		if locked.Resolved != "" {
			ref = locked.Resolved
		}
		expected = locked.TreeDigest
	}

	resolved, err := source.ResolveString(ctx, ref, source.Options{
		Cache:          cache,
		ExpectedDigest: expected,
		Offline:        opts.Offline,
	})
	if err != nil {
		result.Err = err
		return result
	}
	result.Resolved = resolved

	imported, err := importreg.Open(resolved.Dir)
	if err != nil {
		result.Err = err
		return result
	}
	result.Plugin = imported.Plugin

	root, err := safepath.NewRoot(resolved.Dir)
	if err != nil {
		result.Err = err
		return result
	}

	// Scanned before planning: a plugin that will be refused should not produce
	// a fidelity report describing an install that is not going to happen.
	//
	// Compared against what was accepted when this plugin was last locked, so
	// what blocks is a finding that is *new* rather than one already reviewed.
	// Re-reporting an accepted finding on every sync would make the override
	// flag routine, and a routine override stops being a decision.
	if report, err := scanner.ScanWith(ctx, root, imported.Plugin, opts.Classifier); err == nil {
		result.Scan = report
		delta := report.Against(baselineFor(lock, entry.Source))
		result.Delta = &delta

		if fresh := delta.NewAtLeast(scanner.High); len(fresh) > 0 && !opts.AllowFlagged {
			_, wasLocked := lock.FindBySource(entry.Source)
			result.Err = newFindingError(fresh, entry.Source, wasLocked)
			return result
		}
	}

	sel := adapterreg.Selection{
		Clients: chooseClients(entry.Clients, opts.Clients),
		Scope:   chooseScope(entry.Scope, opts.Scope),
	}
	plans, err := adapterreg.PlanInstall(opts.Env, imported.Plugin, root, sel, opts.Plan)
	if err != nil {
		result.Err = err
		return result
	}
	result.Plans = plans

	result.Locked, err = lockEntry(entry, resolved, imported.Plugin)
	if err != nil {
		result.Err = err
		return result
	}
	// Reached only once the install is going ahead, so the record is of
	// findings that were actually accepted rather than merely observed.
	if result.Scan != nil {
		result.Locked.Scan = scanner.NewBaseline(result.Scan.Findings)
	}

	if opts.DryRun {
		return result
	}

	if err := adapterreg.ApplyInstall(opts.Env, store, imported.Plugin, plans, adapterreg.Provenance{
		Source:     resolved.Pinned(),
		TreeDigest: resolved.TreeDigest,
		Identity:   resolved.Identity(),
		Managed:    string(entry.DeclaredIn),
	}); err != nil {
		result.Err = err
	}
	return result
}

// baselineFor returns the findings accepted when this plugin was last locked.
//
// A plugin with no lock entry, or one locked by a build that did not record
// findings, yields an empty baseline — so everything is new, which is the right
// reading in both cases and matches what a gate with no history should do.
func baselineFor(lock *lockfile.Lock, source string) scanner.Baseline {
	if locked, ok := lock.FindBySource(source); ok {
		return locked.Scan
	}
	return nil
}

// newFindingError explains a block in terms of what changed.
//
// The distinction it draws is the point of the whole comparison: "this plugin
// contains an instruction override" is a fact about the plugin, while "this
// plugin gained an instruction override since the version you approved" is an
// event, and only the second one deserves to stop a sync. A reader who has
// approved this plugin before needs to be told which of the two they are
// looking at, because it changes what they should do about it.
func newFindingError(fresh []scanner.Finding, source string, wasLocked bool) error {
	var b strings.Builder
	if wasLocked {
		fmt.Fprintf(&b, "%d new high-severity content finding(s) since the locked version", len(fresh))
	} else {
		fmt.Fprintf(&b, "%d high-severity content finding(s)", len(fresh))
	}
	for _, f := range fresh {
		fmt.Fprintf(&b, "\n      %s", f.Title)
		if f.File != "" {
			fmt.Fprintf(&b, " at %s", f.File)
			if f.Line > 0 {
				fmt.Fprintf(&b, ":%d", f.Line)
			}
		}
	}
	fmt.Fprintf(&b, "\n      run `agentbridge scan %s`, then --allow-flagged-content to accept", source)
	return errors.New(b.String())
}

// lockEntry builds the record that makes a change reviewable.
func lockEntry(entry lockfile.ScopedEntry, resolved *source.Resolved, p *ir.Plugin) (lockfile.Locked, error) {
	irDigest, err := p.Digest()
	if err != nil {
		return lockfile.Locked{}, err
	}

	locked := lockfile.Locked{
		Name:       p.Name,
		Source:     entry.Source,
		Resolved:   resolved.Pinned(),
		Version:    p.Version,
		TreeDigest: resolved.TreeDigest,
		IRDigest:   irDigest,
		Clients:    entry.Clients,
		Scope:      entry.Scope,
	}
	for _, c := range p.Capabilities.List() {
		locked.Capabilities = append(locked.Capabilities, string(c))
	}
	for _, s := range p.Skills {
		locked.Skills = append(locked.Skills, s.Name)
	}
	for _, s := range p.MCPServers {
		locked.Servers = append(locked.Servers, lockfile.LockedServer{
			Name:      s.Name,
			Transport: string(s.Transport),
			Target:    serverTarget(s),
		})
	}
	return locked, nil
}

func serverTarget(s ir.MCPServer) string {
	if s.Transport != ir.TransportStdio {
		return s.URL
	}
	target := s.Command
	for _, a := range s.Args {
		target += " " + a
	}
	const max = 120
	if len(target) > max {
		return target[:max] + "…"
	}
	return target
}

// prune removes plugins a manifest used to declare and no longer does.
func prune(env adapter.Env, store *receipt.Store, declared map[string]bool, opts Options) ([]string, error) {
	var names []string
	seen := map[string]bool{}

	for _, e := range store.All() {
		// Ad-hoc installs are not ours to remove. Only something a manifest
		// once declared can be un-declared.
		if e.Managed == "" || declared[e.Plugin] || seen[e.Plugin] {
			continue
		}
		seen[e.Plugin] = true
		names = append(names, e.Plugin)
	}
	sort.Strings(names)

	if opts.DryRun {
		return names, nil
	}
	for _, name := range names {
		plans, err := adapterreg.PlanRemove(env, store, name, adapterreg.Selection{})
		if err != nil {
			return nil, fmt.Errorf("pruning %s: %w", name, err)
		}
		if _, err := adapterreg.ApplyRemove(env, store, name, plans); err != nil {
			return nil, fmt.Errorf("pruning %s: %w", name, err)
		}
	}
	return names, nil
}

func stillDeclared(res lockfile.Resolution, sourceRef string) bool {
	for _, e := range res.Entries {
		if e.Source == sourceRef {
			return true
		}
	}
	return false
}

// chooseClients narrows a manifest declaration by a command-line selection.
// The command line can only ever restrict further: a `--client` flag must not
// install into something the manifest deliberately excluded.
func chooseClients(declared, flag []string) []string {
	if len(flag) == 0 {
		return declared
	}
	if len(declared) == 0 {
		return flag
	}
	allowed := map[string]bool{}
	for _, c := range declared {
		allowed[c] = true
	}
	var out []string
	for _, c := range flag {
		if allowed[c] {
			out = append(out, c)
		}
	}
	// An empty intersection must not be read as "no restriction". Returning a
	// sentinel that matches nothing keeps the narrower meaning.
	if len(out) == 0 {
		return []string{"\x00none"}
	}
	return out
}

func chooseScope(declared string, flag adapter.Scope) adapter.Scope {
	if flag != "" {
		return flag
	}
	return adapter.Scope(declared)
}

func mergeDiff(a, b lockfile.Diff) lockfile.Diff {
	a.Added = append(a.Added, b.Added...)
	a.Removed = append(a.Removed, b.Removed...)
	a.Changed = append(a.Changed, b.Changed...)
	return a
}
