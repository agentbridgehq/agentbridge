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
	"fmt"
	"sort"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	importreg "github.com/agentbridge/agentbridge/internal/importer/registry"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/lockfile"
	"github.com/agentbridge/agentbridge/internal/safepath"
	"github.com/agentbridge/agentbridge/internal/source"
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
}

// PluginResult is what happened to one declared plugin.
type PluginResult struct {
	Entry    lockfile.ScopedEntry
	Resolved *source.Resolved
	Plugin   *ir.Plugin
	Locked   lockfile.Locked
	Plans    []*adapter.Plan
	// Err records a failure for this plugin alone. One plugin that cannot be
	// resolved must not stop the rest, for the same reason the specification
	// isolates component failures: a partially working machine beats a machine
	// that refused to change at all.
	Err error
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

	if opts.DryRun {
		return result
	}

	if err := adapterreg.ApplyInstall(opts.Env, store, imported.Plugin, plans, adapterreg.Provenance{
		Source:     resolved.Pinned(),
		TreeDigest: resolved.TreeDigest,
		Managed:    string(entry.DeclaredIn),
	}); err != nil {
		result.Err = err
	}
	return result
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
		if err := adapterreg.ApplyRemove(store, name, plans); err != nil {
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
