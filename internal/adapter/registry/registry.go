// Package registry assembles the adapters and orchestrates install and
// removal across every client on a machine.
//
// It is separate from package adapter so that the adapter contract has no
// dependency on the concrete clients, which would be an import cycle.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/adapter/clients/claudecode"
	"github.com/agentbridge/agentbridge/internal/adapter/clients/codex"
	"github.com/agentbridge/agentbridge/internal/adapter/clients/cursor"
	"github.com/agentbridge/agentbridge/internal/adapter/clients/gemini"
	"github.com/agentbridge/agentbridge/internal/adapter/clients/vscode"
	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
)

// StateDirName is the directory holding AgentBridge's own state.
const StateDirName = ".agentbridge"

// DefaultEnv builds an Env from the current process.
func DefaultEnv(projectDir string) (adapter.Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return adapter.Env{}, err
	}
	configDir, _ := os.UserConfigDir()
	return adapter.Env{
		HomeDir:    home,
		ConfigDir:  configDir,
		ProjectDir: projectDir,
		GOOS:       runtime.GOOS,
	}, nil
}

// StateDir returns the directory holding receipts and per-plugin data.
func StateDir(env adapter.Env) string { return filepath.Join(env.HomeDir, StateDirName) }

// CacheDir returns the directory holding fetched source packages. It sits
// beside the receipts rather than inside any client's directory, so a client
// reinstall cannot destroy it and it can be cleared independently.
func CacheDir(env adapter.Env) string { return filepath.Join(StateDir(env), "cache") }

// PluginDataDir returns the persistent directory backing ${PLUGIN_DATA} for a
// plugin. The specification requires it to survive updates, so it lives in our
// state directory rather than inside any client's install location.
func PluginDataDir(env adapter.Env, pluginName string) string {
	return filepath.Join(StateDir(env), "data", pluginName)
}

// Adapters returns every adapter, in a stable order.
func Adapters(env adapter.Env) []adapter.Adapter {
	dataDir := func(name string) string { return PluginDataDir(env, name) }
	return []adapter.Adapter{
		claudecode.New(),
		cursor.New(dataDir),
		vscode.New(dataDir),
		codex.New(dataDir),
		gemini.New(dataDir),
	}
}

// ByID returns the adapter for a client identifier.
func ByID(env adapter.Env, id string) (adapter.Adapter, bool) {
	for _, a := range Adapters(env) {
		if a.Client().ID == id {
			return a, true
		}
	}
	return nil, false
}

// ClientIDs returns every known client identifier.
func ClientIDs(env adapter.Env) []string {
	var out []string
	for _, a := range Adapters(env) {
		out = append(out, a.Client().ID)
	}
	sort.Strings(out)
	return out
}

// Detect returns every client installation found on this machine.
func Detect(env adapter.Env) []adapter.Installation {
	var out []adapter.Installation
	for _, a := range Adapters(env) {
		out = append(out, a.Detect(env)...)
	}
	return out
}

// Selection narrows which installations an operation applies to.
type Selection struct {
	// Clients restricts to these client IDs. Empty means every detected one.
	Clients []string
	// Scope restricts to one scope. Empty means every scope.
	Scope adapter.Scope
}

func (s Selection) matches(inst adapter.Installation) bool {
	if s.Scope != "" && inst.Scope != s.Scope {
		return false
	}
	if len(s.Clients) == 0 {
		return true
	}
	for _, id := range s.Clients {
		if strings.EqualFold(id, inst.Client.ID) {
			return true
		}
	}
	return false
}

// PlanInstall computes what installing a plugin would do to every selected
// client, without touching disk.
func PlanInstall(env adapter.Env, p *ir.Plugin, src *safepath.Root, sel Selection, opts adapter.PlanOptions) ([]*adapter.Plan, error) {
	var plans []*adapter.Plan

	for _, a := range Adapters(env) {
		for _, inst := range a.Detect(env) {
			if !sel.matches(inst) {
				continue
			}
			plan, err := a.Plan(inst, p, src, opts)
			if err != nil {
				return nil, fmt.Errorf("%s (%s): %w", inst.Client.Name, inst.Scope, err)
			}
			plans = append(plans, plan)
		}
	}

	if len(plans) == 0 {
		return nil, fmt.Errorf("no matching agent clients found on this machine (looked for %s)",
			strings.Join(ClientIDs(env), ", "))
	}
	return plans, nil
}

// keyRemover removes exactly the configuration keys a receipt recorded.
type keyRemover interface {
	PlanRemoveKeys(inst adapter.Installation, pluginName string, keys [][]string) (*adapter.Plan, error)
}

// sectionRemover removes exactly the managed-block sections a receipt recorded.
type sectionRemover interface {
	PlanRemoveSections(inst adapter.Installation, pluginName string, sections []string) (*adapter.Plan, error)
}

// PlanRemove computes what removing a plugin would do.
//
// Removal is driven entirely by receipts. Deleting whatever currently matches
// our naming convention would eventually take out an entry the user wrote by
// hand that happened to look like ours; if it is not in the receipt, we did not
// write it, so we leave it alone.
func PlanRemove(env adapter.Env, store *receipt.Store, pluginName string, sel Selection) ([]*adapter.Plan, error) {
	entries := store.ForPlugin(pluginName)
	if len(entries) == 0 {
		return nil, fmt.Errorf("%q is not recorded as installed by agentbridge", pluginName)
	}

	var plans []*adapter.Plan
	for _, e := range entries {
		a, ok := ByID(env, e.Client)
		if !ok {
			return nil, fmt.Errorf("receipt names unknown client %q", e.Client)
		}

		inst := adapter.Installation{
			Client:     a.Client(),
			Scope:      adapter.Scope(e.Scope),
			ConfigPath: e.ConfigPath,
			PackageDir: filepath.Dir(e.PackageDir),
		}
		if !sel.matches(inst) {
			continue
		}

		var (
			plan *adapter.Plan
			err  error
		)
		switch {
		case len(e.ConfigKeys) > 0:
			r, ok := a.(keyRemover)
			if !ok {
				return nil, fmt.Errorf("%s cannot remove configuration keys", e.Client)
			}
			plan, err = r.PlanRemoveKeys(inst, pluginName, e.ConfigKeys)
		case len(e.BlockSections) > 0:
			r, ok := a.(sectionRemover)
			if !ok {
				return nil, fmt.Errorf("%s cannot remove managed block sections", e.Client)
			}
			plan, err = r.PlanRemoveSections(inst, pluginName, e.BlockSections)
		default:
			plan, err = a.PlanRemove(inst, pluginName)
		}
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", e.Client, e.Scope, err)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// Provenance records where a plugin came from, for the receipt.
type Provenance struct {
	// Source is the pinned reference: a branch or tag already replaced by the
	// commit it resolved to.
	Source string
	// TreeDigest is the content address of the installed package.
	TreeDigest string
	// Managed names the manifest scope that declared this plugin, empty for an
	// ad-hoc install.
	Managed string
}

// ApplyInstall executes plans and records receipts.
//
// The receipt is written after the plan succeeds. If the process dies between
// the two, the next install re-records it, whereas recording first would leave
// a receipt claiming ownership of something that was never written.
func ApplyInstall(env adapter.Env, store *receipt.Store, p *ir.Plugin, plans []*adapter.Plan, prov Provenance) error {
	digest, err := p.Digest()
	if err != nil {
		return err
	}

	// Spec 9.1: PLUGIN_DATA must exist and be writable before a plugin
	// subprocess starts. The target client will not create it, so we do.
	if err := adapter.EnsurePluginData(PluginDataDir(env, p.Name)); err != nil {
		return fmt.Errorf("creating plugin data directory: %w", err)
	}

	for _, plan := range plans {
		if err := adapter.Apply(plan); err != nil {
			return fmt.Errorf("%s (%s): %w", plan.Installation.Client.Name, plan.Installation.Scope, err)
		}
		store.Put(receipt.Entry{
			Plugin:        p.Name,
			Client:        plan.Installation.Client.ID,
			Scope:         string(plan.Installation.Scope),
			Digest:        digest,
			Source:        prov.Source,
			TreeDigest:    prov.TreeDigest,
			Managed:       prov.Managed,
			ConfigPath:    plan.Installation.ConfigPath,
			ConfigKeys:    plan.ConfigKeys,
			BlockSections: plan.BlockSections,
			PackageDir:    plan.PackageDir,
		})
	}
	return store.Save()
}

// ApplyRemove executes removal plans and clears their receipts.
func ApplyRemove(store *receipt.Store, pluginName string, plans []*adapter.Plan) error {
	for _, plan := range plans {
		if err := adapter.Apply(plan); err != nil {
			return fmt.Errorf("%s (%s): %w", plan.Installation.Client.Name, plan.Installation.Scope, err)
		}
		store.Delete(pluginName, plan.Installation.Client.ID, string(plan.Installation.Scope))
	}
	return store.Save()
}
