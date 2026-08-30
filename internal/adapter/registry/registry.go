// Package registry assembles the adapters and orchestrates install and
// removal across every client on a machine.
//
// It is separate from package adapter so that the adapter contract has no
// dependency on the concrete clients, which would be an import cycle.
package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/adapter"
	"github.com/agentbridgehq/agentbridge/internal/adapter/clients/claudecode"
	"github.com/agentbridgehq/agentbridge/internal/adapter/clients/codex"
	"github.com/agentbridgehq/agentbridge/internal/adapter/clients/cursor"
	"github.com/agentbridgehq/agentbridge/internal/adapter/clients/gemini"
	"github.com/agentbridgehq/agentbridge/internal/adapter/clients/opencode"
	"github.com/agentbridgehq/agentbridge/internal/adapter/clients/vscode"
	"github.com/agentbridgehq/agentbridge/internal/adapter/receipt"
	"github.com/agentbridgehq/agentbridge/internal/configedit"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
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
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	return adapter.Env{
		HomeDir:    home,
		ConfigDir:  configDir,
		ConfigHome: configHome,
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
		vscode.New(dataDir, env.HomeDir),
		codex.New(dataDir),
		gemini.New(dataDir),
		opencode.New(dataDir),
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
	PlanRemoveKeys(inst adapter.Installation, pluginName string, keys, created [][]string) (*adapter.Plan, error)
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
			plan, err = r.PlanRemoveKeys(inst, pluginName, e.ConfigKeys, e.CreatedContainers)
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
		if err := addAuxRemoval(plan, e); err != nil {
			return nil, fmt.Errorf("%s (%s): %w", e.Client, e.Scope, err)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// addAuxRemoval takes back the keys an install wrote into a second
// configuration file.
//
// It lives here rather than in an adapter because it is not client-specific:
// the receipt says which JSON file and which key paths, and removal deletes
// exactly those. That is the same contract as the primary config, and the
// reason removal can be exact rather than pattern-matched.
func addAuxRemoval(plan *adapter.Plan, e receipt.Entry) error {
	if e.AuxConfigPath == "" || len(e.AuxConfigKeys) == 0 {
		return nil
	}
	doc, err := configedit.LoadJSON(e.AuxConfigPath)
	if err != nil {
		return err
	}
	if !doc.Existed() {
		return nil
	}
	for _, k := range e.AuxConfigKeys {
		if err := doc.Delete(k); err != nil {
			return err
		}
	}
	// A container we brought into existence goes with them, but only while it
	// is still empty: a user who has since added a location of their own keeps
	// it, and everything they wrote around it.
	for _, c := range e.AuxCreatedContainers {
		if children, err := doc.Keys(c); err == nil && len(children) == 0 {
			if err := doc.Delete(c); err != nil {
				return err
			}
		}
	}
	after, err := doc.Bytes()
	if err != nil {
		return err
	}
	plan.Ops = append(plan.Ops, adapter.Op{
		Kind:   adapter.OpWriteFile,
		Path:   e.AuxConfigPath,
		Before: doc.Original(),
		After:  after,
		Note:   "remove the registered skills location",
	})
	return nil
}

// inheritedContainers returns the containers an earlier install of ours
// recorded creating in the same file.
func inheritedContainers(store *receipt.Store, plan *adapter.Plan) [][]string {
	if plan.Installation.ConfigPath == "" || len(plan.ConfigKeys) == 0 {
		return nil
	}
	for _, e := range store.All() {
		if e.ConfigPath != plan.Installation.ConfigPath || len(e.CreatedContainers) == 0 {
			continue
		}
		return e.CreatedContainers
	}
	return nil
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
	// Identity names the upstream with the revision removed.
	Identity string
}

// ErrNameConflict is returned when a different plugin already occupies a name.
var ErrNameConflict = errors.New("plugin name already in use by a different source")

// CheckNameConflict refuses to install over a different plugin with the same
// name.
//
// Exported so a caller can fail before printing a report for an install that
// cannot proceed. ApplyInstall checks it again regardless: this is a data
// integrity rule, not a presentation one, and every path that writes must
// enforce it.
//
// Nothing in Agent Plugins assigns names: §5.5 constrains the string and no
// authority prevents two unrelated plugins from claiming the same one (threat
// T4 in docs/05). Because configuration keys are namespaced by plugin name,
// installing a second plugin under an existing name overwrites the first's
// receipt — and a receipt is the only record of what to remove. The entries the
// first plugin wrote are then orphaned in a client's configuration with nothing
// left that knows they exist.
//
// Refusing is the honest response: two different plugins claiming one name is a
// conflict only the user can resolve.
func CheckNameConflict(store *receipt.Store, name, identity string) error {
	if identity == "" {
		return nil
	}
	for _, e := range store.ForPlugin(name) {
		if e.SourceIdentity == "" || e.SourceIdentity == identity {
			continue
		}
		return fmt.Errorf("%w: %q is already installed from %s.\n"+
			"Two different plugins cannot share a name, because configuration entries are keyed by it "+
			"and removal would orphan one of them. Run `agentbridge remove %s` first if you mean to replace it",
			ErrNameConflict, name, e.SourceIdentity, name)
	}
	return nil
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

	if err := CheckNameConflict(store, p.Name, prov.Identity); err != nil {
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
		// A container this install found already present may still be one we
		// created, for an earlier plugin. Only the first install into an empty
		// config records having created the container; by the time that plugin
		// is removed the container holds the others, so nothing is reclaimed —
		// and by the time it is empty, the receipt that knew we made it is
		// gone. The empty object then outlives every plugin that used it.
		//
		// Inheriting the record keeps the knowledge alive for as long as any
		// of ours is in there, so whichever plugin leaves last takes it with
		// them.
		created := plan.CreatedContainers
		if len(created) == 0 {
			created = inheritedContainers(store, plan)
		}

		store.Put(receipt.Entry{
			Plugin:               p.Name,
			Client:               plan.Installation.Client.ID,
			Scope:                string(plan.Installation.Scope),
			Digest:               digest,
			Source:               prov.Source,
			TreeDigest:           prov.TreeDigest,
			SourceIdentity:       prov.Identity,
			Managed:              prov.Managed,
			ConfigPath:           plan.Installation.ConfigPath,
			ConfigKeys:           plan.ConfigKeys,
			CreatedContainers:    created,
			BlockSections:        plan.BlockSections,
			PackageDir:           plan.PackageDir,
			AuxConfigPath:        plan.AuxConfigPath,
			AuxConfigKeys:        plan.AuxConfigKeys,
			AuxCreatedContainers: plan.AuxCreatedContainers,
		})
	}
	return store.Save()
}

// ApplyRemove executes removal plans, clears their receipts, and disposes of the
// plugin's data directory.
//
// It returns the data directory if one was kept, so the caller can say so. An
// uninstall that silently leaves something behind contradicts the promise the
// receipt design exists to make.
func ApplyRemove(env adapter.Env, store *receipt.Store, pluginName string, plans []*adapter.Plan) (keptData string, err error) {
	for _, plan := range plans {
		if err := adapter.Apply(plan); err != nil {
			return "", fmt.Errorf("%s (%s): %w", plan.Installation.Client.Name, plan.Installation.Scope, err)
		}
		store.Delete(pluginName, plan.Installation.Client.ID, string(plan.Installation.Scope))
	}

	// After the clients, and only once no receipt still claims this plugin.
	// Removing from one client while another still has it installed would take
	// the surviving install's data with it.
	if len(store.ForPlugin(pluginName)) == 0 {
		kept, err := adapter.ReleasePluginData(PluginDataDir(env, pluginName))
		if err != nil {
			return "", err
		}
		keptData = kept
	}
	return keptData, store.Save()
}
