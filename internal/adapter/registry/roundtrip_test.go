package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/adapter/receipt"
	"github.com/agentbridgehq/agentbridge/internal/adapter/registry"
)

// Installing a plugin and then removing it must leave every configuration file
// exactly as it was found, byte for byte.
//
// README and docs/getting-started invite the reader to try precisely this and
// diff the result, so it is a promise the project makes in writing. It was not
// kept: on a real machine the removal left "mcpServers": {} reflowed across two
// lines, added a trailing newline to a file that had none, and left behind an
// empty "mcp" object in a config that never had that key. Nothing was broken
// and no server survived — which is why only a byte comparison finds it.
//
// The cases below are the actual starting states of the files on a developer's
// machine, including the ones with no trailing newline and no container key,
// because those are the two the obvious implementation gets wrong.
func TestInstallThenRemoveRestoresConfigsExactly(t *testing.T) {
	cases := []struct {
		client string
		rel    string
		before string
	}{
		{
			client: "cursor",
			rel:    ".cursor/mcp.json",
			before: "{\n  \"mcpServers\": {}\n}\n",
		},
		{
			client: "cursor",
			rel:    ".cursor/mcp.json",
			before: "{\n  // kept by hand, do not lose this\n  \"mcpServers\": {\n    \"mine\": { \"command\": \"my-server\" }\n  }\n}\n",
		},
		{
			client: "vscode",
			rel:    "Library/Application Support/Code/User/mcp.json",
			before: "{\n  \"servers\": {}\n}\n",
		},
		{
			// No "mcp" key at all, and no trailing newline: exactly what
			// opencode writes when a user first runs it.
			client: "opencode",
			rel:    ".config/opencode/opencode.json",
			before: "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}",
		},
		{
			client: "opencode",
			rel:    ".config/opencode/opencode.json",
			before: "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"model\": \"anthropic/claude-sonnet-4-6\"\n}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.client+" "+tc.before[:min(24, len(tc.before))], func(t *testing.T) {
			env := fakeMachine(t, tc.client)
			path := filepath.Join(env.HomeDir, filepath.FromSlash(tc.rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}

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

			installed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(installed) == tc.before {
				t.Fatal("install changed nothing, so the removal proves nothing")
			}

			removePlans, err := registry.PlanRemove(env, store, plugin.Name, registry.Selection{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := registry.ApplyRemove(env, store, plugin.Name, removePlans); err != nil {
				t.Fatal(err)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tc.before {
				t.Errorf("config not restored\n--- before ---\n%q\n--- after ---\n%q", tc.before, string(after))
			}
		})
	}
}

// A config the install created from nothing must not be left holding the shape
// of what was removed.
//
// Reclaiming the container empties the document itself, and the same leftover
// whitespace applies one level up: the file was written as "{\n}" rather than
// "{}". Small, but it is a file this tool created, so nothing in it was chosen
// by the user and every byte of it is ours to get right.
func TestRemoveCollapsesAConfigWeCreated(t *testing.T) {
	for _, client := range []string{"cursor", "opencode"} {
		t.Run(client, func(t *testing.T) {
			env := fakeMachine(t, client)
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

			var configPath string
			for _, p := range plans {
				if p.Installation.ConfigPath != "" {
					configPath = p.Installation.ConfigPath
				}
			}
			if configPath == "" {
				t.Fatal("no config path in the install plans")
			}
			if _, err := os.Stat(configPath); err != nil {
				t.Fatalf("install did not create the config: %v", err)
			}

			removePlans, err := registry.PlanRemove(env, store, plugin.Name, registry.Selection{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := registry.ApplyRemove(env, store, plugin.Name, removePlans); err != nil {
				t.Fatal(err)
			}

			after, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(after); got != "{}\n" {
				t.Errorf("config we created reads %q after removal, want %q", got, "{}\n")
			}
		})
	}
}

// A plugin with no servers for a client must not bring that client's config
// into existence.
//
// Installing a skills-only plugin created ~/.cursor/mcp.json holding "{}" —
// a file the user never had, for a plugin that had nothing to put in it. The
// dry run announced it as "create config with managed server entries", which
// was untrue in the one place a user is looking specifically to see what will
// be written.
//
// It also cost the plan its honesty: a client that should have reported "=="
// (nothing to do) reported "!!" instead, because there was now an op.
func TestSkillsOnlyPluginDoesNotCreateAnEmptyConfig(t *testing.T) {
	env := fakeMachine(t, "cursor", "claude-code")

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills", "only"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",` +
		`"name":"acme.skillsonly","version":"1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: only\ndescription: A skill and nothing else\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "skills", "only", "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}

	plugin, src := loadFixture(t, dir)
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

	config := filepath.Join(env.HomeDir, ".cursor", "mcp.json")
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		got, _ := os.ReadFile(config)
		t.Errorf("install created %s holding %q; a plugin with no servers should leave it absent",
			config, string(got))
	}

	// Scoped to the config, not the whole plan. Cursor installs a package now,
	// so a skills-only plugin legitimately changes something — just not this
	// file. The claim under test is that no config is conjured for a client
	// with no servers to put in one.
	for _, p := range plans {
		for _, op := range p.Ops {
			if op.Path == p.Installation.ConfigPath && op.Path != "" {
				t.Errorf("%s plans a write to its config, but there are no servers: %s",
					p.Installation.Client.ID, op.Note)
			}
		}
	}
}

// The last plugin out of a shared container takes the container with it.
//
// Only the first install into an empty config records having created the
// container. By the time that plugin is removed the container holds the others,
// so nothing is reclaimed — and by the time it is empty, the receipt that knew
// we created it has been deleted. The empty object then outlives every plugin
// that ever used it, which is how a real machine ended up with "mcp": {} after
// eleven plugins had come and gone.
func TestLastPluginOutReclaimsASharedContainer(t *testing.T) {
	env := fakeMachine(t, "opencode")
	config := filepath.Join(env.HomeDir, ".config", "opencode", "opencode.json")
	before := "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}"
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := receipt.Open(registry.StateDir(env))
	if err != nil {
		t.Fatal(err)
	}
	base, src := loadFixture(t, ccFixture)

	names := []string{"acme.first", "acme.second"}
	for _, name := range names {
		p := *base
		p.Name = name
		plans, err := registry.PlanInstall(env, &p, src, registry.Selection{}, allowPlaintext)
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.ApplyInstall(env, store, &p, plans, registry.Provenance{}); err != nil {
			t.Fatal(err)
		}
	}

	for _, name := range names {
		plans, err := registry.PlanRemove(env, store, name, registry.Selection{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := registry.ApplyRemove(env, store, name, plans); err != nil {
			t.Fatal(err)
		}
	}

	after, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Errorf("a shared container outlived both plugins\n before %q\n after  %q", before, string(after))
	}
}
