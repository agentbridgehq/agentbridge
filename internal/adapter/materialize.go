package adapter

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agentbridge/agentbridge/internal/ir"
)

// secretEnvPattern mirrors the one in internal/capability. Duplicated
// deliberately: this one exists to decide whether writing a value into a
// client's config file needs a warning, and the two should be free to diverge
// without one silently changing the other's behavior.
var secretEnvPattern = regexp.MustCompile(`(?i)(^|_)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|APIKEY|API_KEY|ACCESS_KEY|PRIVATE_KEY|CLIENT_SECRET|AUTH)($|_)`)

// Materialize resolves a server's portable form into something a client can
// actually launch.
//
// The Agent Plugins placeholders are a contract between a plugin and a
// *conformant* client. No client we write config for expands ${PLUGIN_ROOT} or
// ${PLUGIN_DATA} on our behalf, and a plugin-relative "./bin/server" is
// meaningless in a config file that lives somewhere else entirely. Both must
// therefore be resolved to absolute paths at write time.
//
// Getting this wrong is a silent failure: the config validates, the client
// starts, and the server dies with "no such file". It is the mirror image of
// the placeholder problem found importing from Claude Code.
func Materialize(s ir.MCPServer, pluginRoot, pluginData string) ir.MCPServer {
	out := s

	expand := func(v string) string {
		v = strings.ReplaceAll(v, ir.PlaceholderPluginRoot, pluginRoot)
		v = strings.ReplaceAll(v, ir.PlaceholderPluginData, pluginData)
		return v
	}

	if strings.HasPrefix(out.Command, "./") || strings.HasPrefix(out.Command, ".\\") {
		out.Command = filepath.Join(pluginRoot, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(out.Command, "./"), ".\\")))
	} else {
		// A bare command is resolved by the platform's executable search and
		// must be left alone.
		out.Command = expand(out.Command)
	}

	if len(s.Args) > 0 {
		out.Args = make([]string, len(s.Args))
		for i, a := range s.Args {
			out.Args[i] = expand(a)
		}
	}
	if len(s.Env) > 0 {
		out.Env = make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			out.Env[k] = expand(v)
		}
	}
	if len(s.Headers) > 0 {
		out.Headers = make(map[string]string, len(s.Headers))
		for k, v := range s.Headers {
			out.Headers[k] = expand(v)
		}
	}

	switch {
	case out.Cwd == "":
	case strings.HasPrefix(out.Cwd, ir.PlaceholderPluginRoot), strings.HasPrefix(out.Cwd, ir.PlaceholderPluginData):
		out.Cwd = filepath.FromSlash(expand(out.Cwd))
	case strings.HasPrefix(out.Cwd, "./"):
		out.Cwd = filepath.Join(pluginRoot, filepath.FromSlash(strings.TrimPrefix(out.Cwd, "./")))
	}

	return out
}

// NoteSecretsWritten records a loss for each env value that lands in a client
// config as plaintext.
//
// This is not pedantry. These files are routinely committed — VS Code's
// .vscode/mcp.json is documented as something to check in and share with a
// team — so a token in one is a token in the repository. M5 replaces these
// with keychain-backed references; until then the honest move is to say so
// every time.
func NoteSecretsWritten(f *Fidelity, serverName string, env map[string]string, configPath string) {
	for k, v := range env {
		if v == "" || !secretEnvPattern.MatchString(k) {
			continue
		}
		f.AddLoss(LossSecretInPlaintext, serverName,
			"env %s is written as plaintext into %s; move it to a secret reference once M5 lands, and keep that file out of version control until then",
			k, configPath)
	}
}
