package registry_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/adapter/receipt"
	"github.com/agentbridgehq/agentbridge/internal/adapter/registry"
)

// Regression: two plugins claiming the same name silently orphaned one of them.
//
// Configuration entries are keyed by plugin name and a receipt is the only
// record of what to remove, so installing a second plugin under an existing
// name overwrote the first's receipt. `remove` then cleaned only the second,
// leaving the first's entries in the client's configuration permanently, with
// nothing left that knew they existed.
//
// Nothing in Agent Plugins prevents the collision: §5.5 constrains the name
// string and no authority allocates it (threat T4 in docs/05).
func TestSecondPluginCannotTakeAnInstalledName(t *testing.T) {
	env := fakeMachine(t, "cursor")
	plugin, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{
		Identity: "https://example.com/first",
	}); err != nil {
		t.Fatal(err)
	}

	err = registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{
		Identity: "https://example.com/second",
	})
	if err == nil {
		t.Fatal("a different source took an installed plugin name")
	}
	if !errors.Is(err, registry.ErrNameConflict) {
		t.Errorf("error = %v, want ErrNameConflict", err)
	}
	// The message must name the incumbent and say how to proceed, since only
	// the user can resolve this.
	if !strings.Contains(err.Error(), "example.com/first") || !strings.Contains(err.Error(), "agentbridge remove") {
		t.Errorf("unhelpful message: %v", err)
	}
}

// Upgrading is the common case and must not be mistaken for a collision: the
// pin changes with every version, the identity does not.
func TestUpgradingTheSameSourceIsAllowed(t *testing.T) {
	env := fakeMachine(t, "cursor")
	plugin, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}

	for _, pin := range []string{"https://example.com/repo@aaa", "https://example.com/repo@bbb"} {
		if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{
			Source: pin, Identity: "https://example.com/repo",
		}); err != nil {
			t.Fatalf("installing %s: %v", pin, err)
		}
	}
}

// Removing the incumbent frees the name.
func TestNameIsFreedByRemoval(t *testing.T) {
	env := fakeMachine(t, "cursor")
	plugin, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{
		Identity: "https://example.com/first",
	}); err != nil {
		t.Fatal(err)
	}

	removePlans, err := registry.PlanRemove(env, store, plugin.Name, registry.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ApplyRemove(env, store, plugin.Name, removePlans); err != nil {
		t.Fatal(err)
	}

	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{
		Identity: "https://example.com/second",
	}); err != nil {
		t.Errorf("the name should be free after removal: %v", err)
	}

	// And nothing the first install wrote is left behind.
	raw, err := os.ReadFile(filepath.Join(env.HomeDir, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "deploy-tools.bundled") != 1 {
		t.Errorf("orphaned or duplicated entries:\n%s", raw)
	}
}

// An install with no identity recorded — an older receipt, or a path that does
// not supply one — must not be treated as a conflict. Blocking on missing data
// would turn an upgrade into an error for anyone with a receipt written by a
// previous version.
func TestMissingIdentityDoesNotBlock(t *testing.T) {
	env := fakeMachine(t, "cursor")
	plugin, src := loadFixture(t, ccFixture)

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := registry.PlanInstall(env, plugin, src, registry.Selection{}, allowPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ApplyInstall(env, store, plugin, plans, registry.Provenance{
		Identity: "https://example.com/anything",
	}); err != nil {
		t.Errorf("an unidentified incumbent should not block: %v", err)
	}
}
