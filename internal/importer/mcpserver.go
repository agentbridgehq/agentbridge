package importer

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/diag"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// Semantic checks on MCP server entries that JSON Schema cannot express.
//
// The Agent Plugins schemas cover shape, and the specification is explicit that
// its text governs where the two disagree (§7.2.1). Several of the actual
// requirements — executable-token resolution, transport security, working
// directory containment, header validity — are semantic, and a client that
// validates only against the schema will happily launch a server the
// specification forbids.
//
// Section references are to Agent Plugins Specification v1.0.0.

// CheckServerURL enforces the remote endpoint rules in §7.2.1.
//
// The url "MUST be an absolute HTTP or HTTPS URL and MUST NOT contain user
// information or a fragment. Non-loopback endpoints MUST use HTTPS." Loopback
// is exempted by the spec itself because local development servers
// legitimately speak plain HTTP.
//
// It returns false when the entry must be skipped under §7.2.2.
func CheckServerURL(name, rawURL string, ds *diag.Diagnostics) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"server was skipped: url is not a valid URL: %v", err)
		return false
	}
	if !u.IsAbs() {
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"server was skipped: url %q is not absolute", rawURL)
		return false
	}
	// §7.2.1: credentials in the URL are forbidden outright, not merely
	// discouraged — they would be package data visible to anyone with the
	// plugin.
	if u.User != nil {
		ds.AddComponent(diag.Error, diag.CodeMCPInvalidURLForm, "mcp.json", name,
			"server was skipped: url must not contain user information")
		return false
	}
	if u.Fragment != "" || strings.Contains(rawURL, "#") {
		ds.AddComponent(diag.Error, diag.CodeMCPInvalidURLForm, "mcp.json", name,
			"server was skipped: url must not contain a fragment")
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
			"server was skipped: url %q uses http to a non-loopback host; §7.2.1 requires https", rawURL)
		return false
	default:
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"server was skipped: url %q uses unsupported scheme %q", rawURL, u.Scheme)
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

// CheckHeaders enforces the header rules in §7.2.1.
//
// Names and values must be valid HTTP header fields, and because header names
// are case-insensitive, two entries differing only in case are a genuine
// ambiguity rather than a style question — the spec makes such an entry
// invalid.
func CheckHeaders(name string, headers map[string]string, ds *diag.Diagnostics) bool {
	seen := make(map[string]string, len(headers))

	for _, k := range SortedKeys(headers) {
		if !validHeaderName(k) {
			ds.AddComponent(diag.Error, diag.CodeMCPInvalidHeader, "mcp.json", name,
				"server was skipped: %q is not a valid HTTP header name", k)
			return false
		}
		if !validHeaderValue(headers[k]) {
			ds.AddComponent(diag.Error, diag.CodeMCPInvalidHeader, "mcp.json", name,
				"server was skipped: header %s has a value containing characters not allowed in an HTTP header", k)
			return false
		}
		lower := strings.ToLower(k)
		if first, dup := seen[lower]; dup {
			ds.AddComponent(diag.Error, diag.CodeMCPDuplicateHeader, "mcp.json", name,
				"server was skipped: headers %q and %q differ only in case, and header names are case-insensitive", first, k)
			return false
		}
		seen[lower] = k
	}
	return true
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	// http.CanonicalHeaderKey leaves invalid names untouched, which makes it a
	// usable validity probe without hand-rolling the token grammar.
	return http.CanonicalHeaderKey(name) != "" && !strings.ContainsAny(name, " \t\r\n:")
}

func validHeaderValue(value string) bool {
	return !strings.ContainsAny(value, "\r\n\x00")
}

// CheckStdioCommand enforces the executable-token rules in §7.2.1.
//
// The command "MUST be either a bare executable name or a plugin-relative path
// beginning with `./`". Anything else — an absolute path, a `../` escape, or a
// bare relative path like `bin/server` — is invalid; the spec's own example
// calls `../bin/server` out explicitly. A plugin-relative command must also
// stay inside the root, which is the difference between "runs a binary this
// plugin ships" and "runs anything on the machine".
func CheckStdioCommand(root *safepath.Root, name, command string, ds *diag.Diagnostics) bool {
	if command == "" {
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"server was skipped: stdio server has an empty command")
		return false
	}

	if isPluginRelative(command) {
		if _, err := root.Resolve(command); err != nil {
			ds.AddComponent(diag.Error, diag.CodePathEscape, "mcp.json", name,
				"server was skipped: command %q does not resolve inside the plugin root: %v", command, err)
			return false
		}
		return true
	}

	// A bare executable name is resolved by the platform's search rules. It is
	// a name, not a path, so any separator means the author wrote something
	// the specification does not allow.
	if strings.ContainsAny(command, `/\`) {
		ds.AddComponent(diag.Error, diag.CodeMCPInvalidCommand, "mcp.json", name,
			"server was skipped: command %q must be either a bare executable name or a plugin-relative path beginning with \"./\"", command)
		return false
	}
	if strings.ContainsAny(command, " \t") {
		// §7.2.1 requires a single executable token, so embedded whitespace
		// means the author expected shell parsing that will not happen.
		ds.AddComponent(diag.Error, diag.CodeMCPInvalidCommand, "mcp.json", name,
			"server was skipped: command %q contains whitespace but is resolved as a single executable token, not a shell string; move arguments into args", command)
		return false
	}
	// §9.2: placeholders are never expanded in command, so one appearing here
	// would be launched literally.
	if strings.Contains(command, "${") {
		ds.AddComponent(diag.Error, diag.CodeMCPInvalidCommand, "mcp.json", name,
			"server was skipped: command %q contains a placeholder, and §9.2 forbids expansion in command; use a plugin-relative \"./\" path instead", command)
		return false
	}
	return true
}

// CheckCwd enforces the working-directory rules in §7.2.1.
//
// Three forms are legal and nothing else: a `./`-relative path, exactly
// ${PLUGIN_ROOT} or a path beginning ${PLUGIN_ROOT}/, and exactly
// ${PLUGIN_DATA} or a path beginning ${PLUGIN_DATA}/. A ${PLUGIN_ROOT}-rooted
// value must stay inside the plugin root; a ${PLUGIN_DATA}-rooted one must stay
// inside the plugin data directory. The spec is precise that a bare
// ${PLUGIN_DATA}suffix is not one of the accepted forms, which is why the
// prefix test checks for the separator too.
func CheckCwd(root *safepath.Root, name, cwd string, ds *diag.Diagnostics) bool {
	if cwd == "" {
		// §7.2.1: an omitted cwd means the plugin root.
		return true
	}

	switch {
	case rootedAt(cwd, ir.PlaceholderPluginData):
		// The data directory is client-managed and outside the package, so
		// there is no plugin root to contain it to — but an escape out of the
		// data directory itself is still invalid.
		rel := strings.TrimPrefix(strings.TrimPrefix(cwd, ir.PlaceholderPluginData), "/")
		if rel != "" && escapesLexically(rel) {
			ds.AddComponent(diag.Error, diag.CodeMCPCwdUncontained, "mcp.json", name,
				"server was skipped: cwd %q escapes the plugin data directory", cwd)
			return false
		}
		return true

	case rootedAt(cwd, ir.PlaceholderPluginRoot):
		rel := strings.TrimPrefix(strings.TrimPrefix(cwd, ir.PlaceholderPluginRoot), "/")
		if rel == "" {
			return true
		}
		if _, err := root.Resolve(rel); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPCwdUncontained, "mcp.json", name,
				"server was skipped: cwd %q does not resolve inside the plugin root: %v", cwd, err)
			return false
		}
		return true

	case isPluginRelative(cwd):
		if _, err := root.Resolve(cwd); err != nil {
			ds.AddComponent(diag.Error, diag.CodeMCPCwdUncontained, "mcp.json", name,
				"server was skipped: cwd %q does not resolve inside the plugin root: %v", cwd, err)
			return false
		}
		return true

	default:
		ds.AddComponent(diag.Error, diag.CodeMCPServerInvalid, "mcp.json", name,
			"server was skipped: cwd %q must be %q-relative, or rooted at %s or %s",
			cwd, "./", ir.PlaceholderPluginRoot, ir.PlaceholderPluginData)
		return false
	}
}

// rootedAt reports whether a value is exactly the placeholder or the
// placeholder followed by a separator, which is the form §7.2.1 defines.
func rootedAt(value, placeholder string) bool {
	return value == placeholder || strings.HasPrefix(value, placeholder+"/")
}

func escapesLexically(rel string) bool {
	for _, seg := range strings.Split(strings.ReplaceAll(rel, `\`, "/"), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// CheckReservedEnv enforces §9.2: an MCP server's env "MUST NOT contain entries
// named PLUGIN_ROOT or PLUGIN_DATA. Such an entry makes that server
// configuration invalid." The client supplies both variables itself, so a
// manifest setting them is trying to override values it does not own.
//
// It returns false when the server must be skipped.
func CheckReservedEnv(name string, env map[string]string, ds *diag.Diagnostics) bool {
	for _, k := range SortedKeys(env) {
		if k == "PLUGIN_ROOT" || k == "PLUGIN_DATA" {
			ds.AddComponent(diag.Error, diag.CodeMCPReservedEnv, "mcp.json", name,
				"server was skipped: env %s is reserved and supplied by the client (§9.2)", k)
			return false
		}
	}
	return true
}

// StripReservedEnv removes reserved environment names without rejecting the
// server, for dialects whose own rules permit them. The portability problem is
// still reported, because the entry cannot survive a trip through the portable
// format.
func StripReservedEnv(name string, env map[string]string, ds *diag.Diagnostics) {
	for _, k := range SortedKeys(env) {
		if k == "PLUGIN_ROOT" || k == "PLUGIN_DATA" {
			ds.AddComponent(diag.Warning, diag.CodeMCPReservedEnv, "mcp.json", name,
				"env %s is reserved in Agent Plugins §9.2 and was dropped; a conformant client supplies it", k)
			delete(env, k)
		}
	}
}

func isPluginRelative(p string) bool {
	return strings.HasPrefix(p, "./") || strings.HasPrefix(p, ".\\")
}
