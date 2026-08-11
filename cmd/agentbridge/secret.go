package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/agentbridge/agentbridge/internal/adapter"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	"github.com/agentbridge/agentbridge/internal/configedit"
	"github.com/agentbridge/agentbridge/internal/secrets"
	"golang.org/x/term"
)

// ---------------------------------------------------------------- secret

func secretCmd(args []string) error {
	if len(args) == 0 {
		secretUsage()
		return nil
	}
	switch args[0] {
	case "set":
		return secretSet(args[1:])
	case "list", "ls":
		return secretList(args[1:])
	case "rm", "remove", "delete":
		return secretRemove(args[1:])
	case "scan":
		return secretScan(args[1:])
	default:
		secretUsage()
		return fmt.Errorf("unknown secret command %q", args[0])
	}
}

func secretUsage() {
	fmt.Fprint(os.Stderr, `agentbridge secret - keep credentials out of the files this tool writes

  agentbridge secret set <name>        Store a secret (prompts, or reads stdin)
  agentbridge secret list              List stored secret names
  agentbridge secret rm <name>         Remove a secret
  agentbridge secret scan              Find credentials sitting in client configs

Reference a stored secret from a plugin's mcp.json:

  "env": { "API_TOKEN": "${secret:acme/api-token}" }

The reference is resolved when the server starts, not when it is installed, so
the value never reaches a configuration file. Note it is not portable: the
specification requires a conformant client to leave unrecognized placeholder
text literal, so this syntax only works through agentbridge.
`)
}

func secretSet(args []string) error {
	fs := flag.NewFlagSet("secret set", flag.ContinueOnError)
	fromStdin := fs.Bool("stdin", false, "read the value from standard input")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("secret set takes exactly one name")
	}
	name := positional[0]
	if err := secrets.ValidateName(name); err != nil {
		return err
	}

	value, err := readSecretValue(*fromStdin)
	if err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("refusing to store an empty value")
	}

	store := secrets.Open()
	if err := store.Set(name, value); err != nil {
		return err
	}
	fmt.Printf("Stored %s in the %s (%s).\n", name, store.Describe(), secrets.Mask(value))
	fmt.Printf("Reference it from a plugin as ${secret:%s}\n", name)
	return nil
}

// readSecretValue takes a value without it appearing in shell history or in
// the process table, which is why there is no --value flag.
func readSecretValue(fromStdin bool) (string, error) {
	if fromStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fmt.Fprint(os.Stderr, "Value (not echoed): ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func secretList(args []string) error {
	fs := flag.NewFlagSet("secret list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	store := secrets.Open()
	names, err := store.List()
	if err != nil {
		return err
	}
	if *asJSON {
		// Names only, never values — a list is something people paste into
		// issues.
		return emitJSON(map[string]any{"store": store.Describe(), "names": names})
	}
	if len(names) == 0 {
		fmt.Printf("No secrets stored in the %s.\n", store.Describe())
		return nil
	}
	// Values are never printed, not even masked: a list is something people
	// paste into issues.
	for _, n := range names {
		fmt.Println(n)
	}
	return nil
}

func secretRemove(args []string) error {
	fs := flag.NewFlagSet("secret rm", flag.ContinueOnError)
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("secret rm takes exactly one name")
	}

	store := secrets.Open()
	if err := store.Delete(positional[0]); err != nil {
		return err
	}
	fmt.Printf("Removed %s.\n", positional[0])
	return nil
}

// ---------------------------------------------------------------- run

// runServer is the launcher that makes secret references work.
//
// A client's configuration records which secrets a server needs, never their
// values. At start, this resolves each reference from the credential store,
// adds it to the environment, and replaces itself with the real command. The
// credential therefore exists only in the process environment of the server
// that needs it, and never in a file that gets committed, backed up, or shared
// on a screen.
//
// It is deliberately the only part of this tool that sits in a running agent's
// path, and it gets there only because someone chose to use a secret reference.
func runServer(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var refs stringList
	fs.Var(&refs, "secret", "ENV=secret-name, repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	command := fs.Args()
	if len(command) == 0 {
		return fmt.Errorf("run needs a command after --")
	}

	store := secrets.Open()
	env := os.Environ()

	for _, spec := range refs {
		key, name, ok := strings.Cut(spec, "=")
		if !ok || key == "" || name == "" {
			return fmt.Errorf("invalid --secret %q: expected ENV=secret-name", spec)
		}
		value, err := store.Get(name)
		if err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				return fmt.Errorf("secret %q is not stored; run `agentbridge secret set %s`", name, name)
			}
			return err
		}
		env = append(env, key+"="+value)
	}

	path, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("%s: %w", command[0], err)
	}

	if runtime.GOOS == "windows" {
		// Windows has no exec, so the process is spawned and its exit status
		// propagated. Signals reach the child through the console group.
		cmd := exec.Command(path, command[1:]...)
		cmd.Env = env
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.ExitCode())
			}
			return err
		}
		return nil
	}

	// Replacing this process rather than wrapping it keeps the process tree
	// the client expects, so signals and stdio behave exactly as they would
	// without a launcher in the way.
	return syscall.Exec(path, command, env)
}

// stringList collects a repeatable flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// ---------------------------------------------------------------- scan

// secretScan reports credentials already sitting in client configuration files
// and offers to move them into the credential store.
//
// This exists because the interesting case is not a fresh install — it is the
// machine that has been accumulating MCP servers by hand for months. Detection
// is read-only and migration is opt-in: rewriting someone's configuration
// because a heuristic fired would be exactly the kind of surprise this project
// is meant to avoid.
func secretScan(args []string) error {
	fs := flag.NewFlagSet("secret scan", flag.ContinueOnError)
	migrate := fs.Bool("migrate", false, "move findings into the credential store and reference them")
	if err := fs.Parse(args); err != nil {
		return err
	}

	env, err := currentEnv()
	if err != nil {
		return err
	}

	found := 0
	migrated := 0
	for _, inst := range adapterreg.Detect(env) {
		if inst.ConfigPath == "" {
			continue
		}
		findings, err := scanConfig(inst.ConfigPath)
		if err != nil {
			fmt.Printf("  ?? %-14s %v\n", inst.Client.ID, err)
			continue
		}
		if len(findings) == 0 {
			continue
		}

		fmt.Printf("\n%s  %s\n", inst.Client.ID, inst.ConfigPath)
		for _, f := range findings {
			fmt.Printf("  %-6s %s.%s — %s\n", f.Confidence, f.Server, f.Key, f.Reason)
			found++

			if !*migrate {
				continue
			}
			name := f.Server + "/" + f.Suggested
			if err := migrateSecret(inst.ConfigPath, f, name); err != nil {
				fmt.Printf("         migration failed: %v\n", err)
				continue
			}
			fmt.Printf("         moved to ${secret:%s}\n", name)
			migrated++
		}
	}

	switch {
	case found == 0:
		fmt.Println("No credentials found in the client configurations on this machine.")
	case *migrate:
		fmt.Printf("\n%d finding(s), %d migrated. Restart your agent clients to pick up the change.\n", found, migrated)
	default:
		fmt.Printf("\n%d finding(s). Re-run with --migrate to move them into the %s.\n",
			found, secrets.Open().Describe())
		fmt.Println("Nothing was changed.")
	}
	return nil
}

// configFinding is a suspected credential located in a client's config.
type configFinding struct {
	secrets.Finding
	// Server is the config key of the MCP server the value belongs to.
	Server string
	// keyPath addresses the value inside the document, so migration can
	// replace exactly it.
	keyPath []string
}

// scanConfig reads one client configuration and reports credentials in it.
//
// Only JSON configurations are scanned. Codex's TOML is written into a block
// this tool owns, so anything credential-shaped there came through an install
// and was already reported at that point.
func scanConfig(path string) ([]configFinding, error) {
	if !strings.HasSuffix(path, ".json") {
		return nil, nil
	}
	doc, err := configedit.LoadJSON(path)
	if err != nil {
		return nil, err
	}
	if !doc.Existed() {
		return nil, nil
	}

	var out []configFinding
	// Both container spellings, since VS Code is the odd one out.
	for _, container := range [][]string{{"mcpServers"}, {"servers"}} {
		names, err := doc.Keys(container)
		if err != nil {
			return nil, err
		}
		sort.Strings(names)

		for _, server := range names {
			envPath := append(append([]string(nil), container...), server, "env")
			envKeys, err := doc.Keys(envPath)
			if err != nil {
				return nil, err
			}
			sort.Strings(envKeys)

			for _, key := range envKeys {
				value, err := doc.StringAt(append(append([]string(nil), envPath...), key))
				if err != nil || value == "" {
					continue
				}
				if f, ok := secrets.Detect(key, value); ok {
					out = append(out, configFinding{
						Finding: f,
						Server:  server,
						keyPath: append(append([]string(nil), envPath...), key),
					})
				}
			}
		}
	}
	return out, nil
}

// migrateSecret moves one value into the credential store and replaces it with
// a reference.
//
// The value is stored first and only then removed from the file. The reverse
// order risks losing a credential that exists nowhere else, and a credential a
// user cannot recover is a worse outcome than one that is stored twice.
func migrateSecret(path string, f configFinding, name string) error {
	doc, err := configedit.LoadJSON(path)
	if err != nil {
		return err
	}
	value, err := doc.StringAt(f.keyPath)
	if err != nil {
		return err
	}

	store := secrets.Open()
	if existing, err := store.Get(name); err == nil && existing != value {
		return fmt.Errorf("secret %q already holds a different value; migrate by hand", name)
	}
	if err := store.Set(name, value); err != nil {
		return err
	}

	if err := doc.Set(f.keyPath, secrets.Ref{Name: name}.String()); err != nil {
		return err
	}
	after, err := doc.Bytes()
	if err != nil {
		return err
	}
	return adapter.Apply(&adapter.Plan{Ops: []adapter.Op{{
		Kind:   adapter.OpWriteFile,
		Path:   path,
		Before: doc.Original(),
		After:  after,
	}}})
}
