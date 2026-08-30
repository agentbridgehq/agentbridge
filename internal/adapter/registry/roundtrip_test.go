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
