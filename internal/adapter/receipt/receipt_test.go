package receipt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
)

// The store is a whole-file document, so an instance loaded before someone
// else's write would erase receipts it never saw — and the plugins they
// described would become unremovable with nothing to say why.
func TestSaveRefusesToClobberAConcurrentWrite(t *testing.T) {
	dir := t.TempDir()

	first, err := receipt.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first.Put(receipt.Entry{Plugin: "a", Client: "cursor", Scope: "user"})
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	// Two instances, both now stale relative to each other.
	stale, err := receipt.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	other, err := receipt.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	other.Put(receipt.Entry{Plugin: "b", Client: "cursor", Scope: "user"})
	if err := other.Save(); err != nil {
		t.Fatal(err)
	}

	stale.Put(receipt.Entry{Plugin: "c", Client: "cursor", Scope: "user"})
	err = stale.Save()
	if err == nil {
		t.Fatal("a stale instance overwrote a newer file")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Errorf("error should say what happened: %v", err)
	}

	// The newer write survives.
	reloaded, err := receipt.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.ForPlugin("b")) == 0 {
		t.Error("the newer receipt was lost")
	}
}

// Repeated saves from one instance are the normal case and must keep working.
func TestRepeatedSavesFromOneInstance(t *testing.T) {
	dir := t.TempDir()

	store, err := receipt.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		store.Put(receipt.Entry{Plugin: name, Client: "cursor", Scope: "user"})
		if err := store.Save(); err != nil {
			t.Fatalf("save after %s: %v", name, err)
		}
	}
	if len(store.All()) != 3 {
		t.Errorf("entries = %d", len(store.All()))
	}
}

func TestOpenRejectsCorruptStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, receipt.FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Silently resetting would orphan every installed plugin, leaving files in
	// clients' configs that nothing knows how to remove.
	if _, err := receipt.Open(dir); err == nil {
		t.Error("a corrupt store should be an error, not a silent reset")
	}
}
