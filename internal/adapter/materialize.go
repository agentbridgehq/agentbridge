package adapter

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/ir"
)

// secretEnvPattern mirrors the one in internal/capability. Duplicated
// deliberately: this one exists to decide whether writing a value into a
// client's config file needs a warning, and the two should be free to diverge
// without one silently changing the other's behavior.
var secretEnvPattern = regexp.MustCompile(`(?i)(^|_)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|APIKEY|API_KEY|ACCESS_KEY|PRIVATE_KEY|CLIENT_SECRET|AUTH)($|_)`)

// Materialize turns a server's portable form into something a non-conformant
// client can actually launch correctly.
//
// The Agent Plugins runtime contract (§9.1, §9.2, §7.2.1) is a set of promises
// a *conformant* client makes to a plugin. Every target we write configuration
// for makes none of them, so this function has to keep those promises on the
// client's behalf. Each one is a silent failure if skipped — the config
// validates, the client starts, and the server misbehaves or dies:
//
//   - §9.2 expansion. ${PLUGIN_ROOT} and ${PLUGIN_DATA} are expanded in args,
//     env values and cwd. Nowhere else: the spec forbids expansion in command,
//     url, header names and header values, so those are passed through
//     untouched.
//   - §7.2.1 command resolution. A `./`-relative command is resolved against
//     the plugin root, because the target client resolves relative paths
//     against its own working directory, not the plugin's. A bare name is left
//     alone for the platform's executable search.
//   - §7.2.1 default working directory. An omitted cwd means the plugin root.
//     A client that does not know the plugin exists will use its own cwd
//     instead, so the default is made explicit.
//   - §9.1 subprocess environment. PLUGIN_ROOT and PLUGIN_DATA are injected,
//     since the spec requires them to be present for every plugin subprocess
//     and the target client will not supply them. They are set last, which is
//     also the precedence the spec requires.
func Materialize(s ir.MCPServer, pluginRoot, pluginData string) ir.MCPServer {
	out := s

	expand := func(v string) string {
		v = strings.ReplaceAll(v, ir.PlaceholderPluginRoot, pluginRoot)
		v = strings.ReplaceAll(v, ir.PlaceholderPluginData, pluginData)
		return v
	}

	if s.Transport != ir.TransportStdio {
		// §7.2.1: no expansion in url, header names, or header values. Remote
		// entries are copied through unchanged.
		out.Headers = copyMap(s.Headers)
		return out
	}

	// §7.2.1: command takes no placeholder expansion. A plugin-relative path
	// is *resolved*, which is a different operation.
	if isRelativeCommand(out.Command) {
		rel := strings.TrimPrefix(strings.TrimPrefix(out.Command, "./"), ".\\")
		out.Command = filepath.Join(pluginRoot, filepath.FromSlash(rel))
	}

	if len(s.Args) > 0 {
		out.Args = make([]string, len(s.Args))
		for i, a := range s.Args {
			out.Args[i] = expand(a)
		}
	}

	out.Env = make(map[string]string, len(s.Env)+2)
	for k, v := range s.Env {
		out.Env[k] = expand(v)
	}
	// §9.1: "The client MUST then set PLUGIN_ROOT and PLUGIN_DATA ..., replacing
	// any entries with equivalent names." Set after the configured env so the
	// precedence matches.
	if pluginRoot != "" {
		out.Env["PLUGIN_ROOT"] = pluginRoot
	}
	if pluginData != "" {
		out.Env["PLUGIN_DATA"] = pluginData
	}

	switch {
	case out.Cwd == "":
		// §7.2.1: "When cwd is omitted, clients MUST use the plugin root as the
		// subprocess working directory."
		out.Cwd = pluginRoot
	case strings.HasPrefix(out.Cwd, ir.PlaceholderPluginRoot), strings.HasPrefix(out.Cwd, ir.PlaceholderPluginData):
		out.Cwd = filepath.FromSlash(expand(out.Cwd))
	case strings.HasPrefix(out.Cwd, "./"):
		out.Cwd = filepath.Join(pluginRoot, filepath.FromSlash(strings.TrimPrefix(out.Cwd, "./")))
	}

	return out
}

func isRelativeCommand(cmd string) bool {
	return strings.HasPrefix(cmd, "./") || strings.HasPrefix(cmd, ".\\")
}

func copyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// EnsurePluginData creates the persistent data directory a plugin's subprocess
// is promised.
//
// §9.1 requires the client to create PLUGIN_DATA before launching a plugin
// subprocess, make it writable, and preserve it across updates. Since the
// target client does not know the directory exists, creating it is our job —
// and a server that assumes it can write there would otherwise fail on first
// run.
func EnsurePluginData(dir string) error {
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// ReleasePluginData disposes of a plugin's data directory at uninstall, and
// reports whether anything was kept.
//
// §9.1 requires PLUGIN_DATA to be preserved across *updates*, and says nothing
// about removal — which leaves the decision to us, and both possible answers
// are wrong in one direction:
//
//   - Deleting it always would throw away a server's accumulated state on an
//     uninstall the user may be doing to reinstall a minute later. That data is
//     theirs, and this tool did not write it.
//   - Keeping it always leaves an empty directory behind on every uninstall of
//     every plugin that never ran a server, which is litter — and litter left
//     by a tool whose documented promise is to remove "exactly what was
//     installed, and nothing else".
//
// So: an empty directory is removed, because there is nothing to preserve and
// what remains is only our own bookkeeping. A directory with contents is kept
// and *reported*, so the choice to delete real data stays with the person whose
// data it is. Silence is the one option not available, since it is the one that
// makes the promise above untrue.
func ReleasePluginData(dir string) (kept string, err error) {
	if dir == "" {
		return "", nil
	}

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(entries) > 0 {
		return dir, nil
	}

	// os.Remove rather than RemoveAll: the directory was empty a moment ago,
	// and if something has appeared since then it is a plugin's data arriving
	// under a concurrent process, not ours to delete.
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return dir, nil
	}
	return "", nil
}

// NoteSecretsWritten records a loss for each env value that lands in a client
// config as plaintext.
//
// The spec agrees this is wrong: §9.2 states that "configured env values are
// visible package data, not a portable secret mechanism" and that plugins MUST
// NOT embed credentials in env. These files are also routinely committed —
// .vscode/mcp.json is documented as something to share with a team — so a token
// in one is a token in the repository. M5 replaces these with keychain-backed
// references; until then the honest move is to say so every time.
func NoteSecretsWritten(f *Fidelity, serverName string, env map[string]string, configPath string) {
	for k, v := range env {
		if v == "" || !secretEnvPattern.MatchString(k) {
			continue
		}
		if k == "PLUGIN_ROOT" || k == "PLUGIN_DATA" {
			continue
		}
		f.AddLoss(LossSecretInPlaintext, serverName,
			"env %s is written as plaintext into %s; the specification is explicit that env is visible package data and not a secret mechanism (§9.2)",
			k, configPath)
	}
}
