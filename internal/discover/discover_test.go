package discover_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/discover"
)

func writePlugin(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "skills", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",` +
		`"name":"` + name + `","version":"1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: s\ndescription: d\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "skills", "s", "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rels(found []discover.Found) []string {
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.Rel)
	}
	return out
}

// The case this package exists for: a repository holding several plugins,
// which is how a company keeps its internal ones.
func TestFindsEveryPluginInARepository(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, filepath.Join(root, "plugins", "deploy"), "acme.deploy")
	writePlugin(t, filepath.Join(root, "plugins", "review"), "acme.review")
	writePlugin(t, filepath.Join(root, "tools", "agent", "notes"), "acme.notes")

	// Ordinary repository content that is not a plugin.
	if err := os.MkdirAll(filepath.Join(root, "src", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	found, err := discover.Plugins(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"plugins/deploy", "plugins/review", "tools/agent/notes"}
	got := rels(found)
	if len(got) != len(want) {
		t.Fatalf("found %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("found[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Pointing at a plugin and pointing at a repository containing one are the
// same operation from the caller's side.
func TestAPluginItselfIsTheOnlyResult(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "acme.single")

	found, err := discover.Plugins(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Rel != "." {
		t.Fatalf("found %v, want exactly the root", rels(found))
	}
}

// A plugin's own subdirectories are its components. Recursing into them would
// report references/ and scripts/ as packages of their own.
func TestDoesNotDescendIntoAPlugin(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, filepath.Join(root, "p"), "acme.p")
	// A nested directory that would itself look like a plugin.
	writePlugin(t, filepath.Join(root, "p", "skills", "s", "references"), "acme.nested")

	found, err := discover.Plugins(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Rel != "p" {
		t.Errorf("found %v, want only the outer plugin", rels(found))
	}
}

// A vendored copy of somebody else's plugin is not this repository's problem,
// and reporting findings from one is how a scan becomes noise people mute.
func TestSkipsDependencyAndBuildDirectories(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, filepath.Join(root, "plugins", "ours"), "acme.ours")
	for _, skipped := range []string{"node_modules", "vendor", "dist", ".git"} {
		writePlugin(t, filepath.Join(root, skipped, "theirs"), "other.theirs")
	}

	found, err := discover.Plugins(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Rel != "plugins/ours" {
		t.Errorf("found %v, want only plugins/ours", rels(found))
	}
}

func TestEmptyTreeFindsNothing(t *testing.T) {
	found, err := discover.Plugins(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("found %v in an empty tree", rels(found))
	}
}

// Results are ordered so a CI log and a SARIF upload read the same way on
// every run, whatever order the filesystem hands them back.
func TestResultsAreOrdered(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"zebra", "alpha", "middle"} {
		writePlugin(t, filepath.Join(root, "plugins", name), "acme."+name)
	}

	found, err := discover.Plugins(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"plugins/alpha", "plugins/middle", "plugins/zebra"}
	for i, w := range want {
		if found[i].Rel != w {
			t.Errorf("found[%d] = %q, want %q", i, found[i].Rel, w)
		}
	}
}
