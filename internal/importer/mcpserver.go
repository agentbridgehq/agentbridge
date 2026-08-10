package importer

import (
	"net"
	"net/url"
	"strings"

	"github.com/agentbridge/agentbridge/internal/diag"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
)

// Semantic checks on MCP server entries that JSON Schema cannot express.
//
// The Agent Plugins schemas cover shape. Several of the specification's actual
// requirements are semantic — transport security, executable resolution,
// working-directory containment — and a client that validates only against the
// schema will happily launch a server the specification forbids.

// CheckServerURL enforces the transport security rule: a non-loopback endpoint
// must use HTTPS.
//
// It returns false when the server must be rejected. Loopback is exempt
// because local development servers legitimately speak plain HTTP, and
// refusing them would push authors toward disabling the check entirely.
func CheckServerURL(name, rawURL string, ds *diag.Diagnostics) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"server url is not a valid URL: %v", err)
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return true
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return true
		}
		ds.AddComponent(diag.Error, diag.CodeMCPInsecureURL, "mcp.json", name,
			"server url %q uses http to a non-loopback host; the specification requires https", rawURL)
		return false
	case "":
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"server url %q has no scheme; an absolute http or https URL is required", rawURL)
		return false
	default:
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"server url %q uses unsupported scheme %q", rawURL, u.Scheme)
		return false
	}
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// CheckStdioCommand validates a stdio server's command against the
// specification's resolution rule: a command is either a bare executable name
// resolved by the platform's search rules, or a `./`-relative path resolved
// against the plugin root.
//
// A plugin-relative command must stay inside the root; that is the same
// containment rule as everything else, and it is the difference between
// "runs a binary this plugin ships" and "runs anything on the machine."
func CheckStdioCommand(root *safepath.Root, name, command string, ds *diag.Diagnostics) bool {
	if command == "" {
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"stdio server has an empty command")
		return false
	}

	if !isPluginRelative(command) {
		// A bare token, resolved by the platform's executable search rules.
		// Whether it exists is a runtime question, not a load-time one: the
		// binary may be installed later, or by the plugin itself.
		if strings.ContainsAny(command, " \t") {
			// The specification requires commands resolve as single executable
			// tokens, so embedded whitespace means the author expected shell
			// parsing that will not happen.
			ds.AddComponent(diag.Warning, diag.CodeMCPServerInvalid, "mcp.json", name,
				"command %q contains whitespace but is resolved as a single executable token, not a shell string; move arguments into args", command)
		}
		return true
	}

	if _, err := root.Resolve(command); err != nil {
		ds.AddComponent(diag.Error, diag.CodePathEscape, "mcp.json", name,
			"command %q does not resolve inside the plugin root: %v", command, err)
		return false
	}
	return true
}

// CheckCwd validates a working directory against the containment rule.
//
// Three forms are legal: `./`-relative, `${PLUGIN_ROOT}`-rooted, and
// `${PLUGIN_DATA}`-rooted. Only the first two are checked for containment —
// PLUGIN_DATA is by definition a directory outside the package, so there is
// nothing to contain it to. It is validated for shape only.
func CheckCwd(root *safepath.Root, name, cwd string, ds *diag.Diagnostics) bool {
	if cwd == "" {
		return true
	}

	switch {
	case strings.HasPrefix(cwd, ir.PlaceholderPluginData):
		return true

	case strings.HasPrefix(cwd, ir.PlaceholderPluginRoot):
		rel := strings.TrimPrefix(cwd, ir.PlaceholderPluginRoot)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return true
		}
		if _, err := root.Resolve(rel); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPCwdUncontained, "mcp.json", name,
				"cwd %q does not resolve inside the plugin root: %v", cwd, err)
			return false
		}
		return true

	case isPluginRelative(cwd):
		if _, err := root.Resolve(cwd); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPCwdUncontained, "mcp.json", name,
				"cwd %q does not resolve inside the plugin root: %v", cwd, err)
			return false
		}
		return true

	default:
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"cwd %q must start with \"./\", %q, or %q", cwd, ir.PlaceholderPluginRoot, ir.PlaceholderPluginData)
		return false
	}
}

// CheckReservedEnv reports environment variable names the specification
// reserves. The client supplies PLUGIN_ROOT and PLUGIN_DATA; a manifest that
// sets them is trying to override values the client controls.
func CheckReservedEnv(name string, env map[string]string, ds *diag.Diagnostics) {
	for _, k := range SortedKeys(env) {
		if k == "PLUGIN_ROOT" || k == "PLUGIN_DATA" {
			ds.AddComponent(diag.Warning, diag.CodeMCPReservedEnv, "mcp.json", name,
				"env %s is reserved and is supplied by the client; the manifest value was dropped", k)
			delete(env, k)
		}
	}
}

func isPluginRelative(p string) bool {
	return strings.HasPrefix(p, "./") || strings.HasPrefix(p, ".\\")
}
