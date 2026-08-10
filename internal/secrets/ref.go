// Package secrets keeps credentials out of the files this tool writes.
//
// The specification is unusually direct about the problem and offers no
// solution. §9.2: "Configured `env` values are visible package data, not a
// portable secret mechanism. Plugins MUST NOT embed credentials or other
// secrets in `env`." §7.2.1 says the same of `headers`, forbids user
// information in a `url`, and states plainly that "Agent Plugins v1 defines no
// OAuth configuration or portable credential-reference fields."
//
// Read together: **there is no conformant way to give an MCP server a
// credential in v1.0.0.** Every plugin that needs one is either violating the
// specification or relying on client-specific behavior. The gap is
// acknowledged upstream — `FUTURE_CONSIDERATIONS.md` lists secret handling as
// possible future work — so the design here is deliberately shaped to converge
// with whatever lands rather than to compete with it: a reference syntax that
// is ours alone, resolved before anything reaches a client, and never written
// into a portable artifact.
//
// # Why references cannot be portable
//
// §9.2 also requires that "unrecognized placeholder-like text MUST remain
// literal" and that clients perform no expansion beyond the two defined
// placeholders. A `${secret:...}` written into an mcp.json would therefore be
// sent to the server verbatim, as the eleven-character string it is. That is
// not a limitation to work around; it is the reason a reference must be
// resolved by us at install or launch time and must never appear in a file a
// conformant client reads.
package secrets

import (
	"fmt"
	"regexp"
	"strings"
)

// Prefix introduces a secret reference.
const Prefix = "${secret:"

// refPattern matches a whole-value secret reference.
//
// Only whole values are references. A value like "Bearer ${secret:token}" is
// deliberately *not* matched: partial interpolation would mean the surrounding
// text has to be reconstructed wherever the secret is used, and every place
// that handles it becomes a place that can accidentally log the assembled
// result. Whole-value references keep the secret an opaque unit.
var refPattern = regexp.MustCompile(`^\$\{secret:([A-Za-z0-9][A-Za-z0-9._/-]*)\}$`)

// nameOK matches a permitted secret name.
var nameOK = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// Ref is a reference to a stored secret.
type Ref struct {
	// Name identifies the secret, conventionally scoped like
	// "acme.db/api-token". The slash is a convention, not a hierarchy: stores
	// treat the whole string as one key.
	Name string
}

// String renders the reference as it appears in configuration.
func (r Ref) String() string { return Prefix + r.Name + "}" }

// ParseRef reads a whole-value secret reference.
func ParseRef(value string) (Ref, bool) {
	m := refPattern.FindStringSubmatch(value)
	if m == nil {
		return Ref{}, false
	}
	return Ref{Name: m[1]}, true
}

// IsRef reports whether a value is a secret reference.
func IsRef(value string) bool {
	_, ok := ParseRef(value)
	return ok
}

// ContainsRef reports whether a value mentions a reference anywhere, including
// in a form this package will not resolve.
//
// Used to catch the near-miss — a reference embedded in a larger string — and
// tell the author why it will not work, rather than writing it out verbatim and
// leaking a placeholder to a server.
func ContainsRef(value string) bool { return strings.Contains(value, Prefix) }

// ValidateName checks a secret name.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("secret name must not be empty")
	}
	if !nameOK.MatchString(name) {
		return fmt.Errorf("invalid secret name %q: use letters, digits, and %q, starting with a letter or digit",
			name, "._/-")
	}
	return nil
}

// RefsIn returns every environment entry whose value is a secret reference,
// keyed by environment variable name.
func RefsIn(env map[string]string) map[string]Ref {
	out := map[string]Ref{}
	for k, v := range env {
		if ref, ok := ParseRef(v); ok {
			out[k] = ref
		}
	}
	return out
}
