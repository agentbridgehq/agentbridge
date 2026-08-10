package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/secrets"
)

// PlanOptions carry install-time policy into the adapters.
type PlanOptions struct {
	// AllowPlaintextSecrets permits writing a credential-looking literal into
	// a client configuration file. Off by default, because these files are
	// routinely committed — .vscode/mcp.json is documented as something to
	// share with a team — and the specification itself says env values are
	// visible package data (§9.2).
	AllowPlaintextSecrets bool
	// Launcher is the absolute path to this binary. A server whose environment
	// contains secret references is launched through it so the values are read
	// from the credential store at spawn time and never written to disk.
	// Empty disables the launcher, and such servers are then skipped.
	Launcher string
	// Secrets resolves references at plan time, only to check they exist. The
	// values are never read into a plan.
	Secrets secrets.Store
}

// Stable loss codes for secret handling.
const (
	LossSecretPlaintextRefused = "client.secret_plaintext_refused"
	LossSecretMissing          = "client.secret_not_stored"
	LossSecretNoLauncher       = "client.secret_launcher_unavailable"
	LossSecretPartialRef       = "client.secret_reference_embedded"
)

// SecretNote records a non-lossy secret decision, so the fidelity report can
// say a server was launched indirectly rather than leaving it unexplained.
type SecretNote struct {
	Server string
	Detail string
}

// PrepareSecrets applies secret policy to one server, returning the server to
// install and whether it may be installed at all.
//
// Three outcomes, and the middle one is the interesting one:
//
//   - No credentials involved: the server passes through untouched.
//   - Secret *references*: the server is rewritten to launch through this
//     binary, which resolves the references from the credential store and execs
//     the real command. The value reaches the process environment and never the
//     filesystem.
//   - Credential *literals*: refused, unless explicitly allowed. Writing one
//     puts a live credential in a file that is frequently committed.
//
// The launcher is not imposed on anyone. It appears only for a server whose
// configuration already uses secret references, which is a deliberate act by
// whoever wrote it — so the default install still writes a plain configuration
// with no runtime dependency on this tool, as docs/03 principle 3 requires.
func PrepareSecrets(s ir.MCPServer, opts PlanOptions, f *Fidelity, configPath string) (ir.MCPServer, []SecretNote, bool) {
	var notes []SecretNote

	// A reference embedded in a larger string cannot be resolved, and must not
	// be written out: §9.2 requires a conformant client to leave unrecognized
	// placeholder-like text literal, so the server would receive the
	// placeholder itself.
	for _, k := range sortedMapKeys(s.Env) {
		v := s.Env[k]
		if secrets.ContainsRef(v) && !secrets.IsRef(v) {
			f.AddLoss(LossSecretPartialRef, s.Name,
				"env %s embeds a secret reference inside a larger value, which cannot be resolved; use the reference as the whole value", k)
			return s, nil, false
		}
	}

	refs := secrets.RefsIn(s.Env)

	// Literals that look like credentials.
	for _, finding := range secrets.DetectAll(s.Env) {
		if _, isRef := refs[finding.Key]; isRef {
			continue
		}
		if !opts.AllowPlaintextSecrets {
			f.AddLoss(LossSecretPlaintextRefused, s.Name,
				"env %s was not written: %s. Store it with `agentbridge secret set %s`, set the value to ${secret:%s} in the plugin, "+
					"or re-run with --allow-plaintext-secrets to write it to %s as-is",
				finding.Key, finding.Reason, finding.Suggested, finding.Suggested, configPath)
			return s, nil, false
		}
		f.AddLoss(LossSecretInPlaintext, s.Name,
			"env %s is written as plaintext into %s; the specification is explicit that env is visible package data and not a secret mechanism (§9.2)",
			finding.Key, configPath)
	}

	if len(refs) == 0 {
		return s, nil, true
	}

	// Every referenced secret must already exist. Discovering at launch that a
	// credential was never stored produces a server that fails silently inside
	// a client, which is the failure mode hardest to diagnose.
	if opts.Secrets != nil {
		for _, k := range sortedRefKeys(refs) {
			if _, err := opts.Secrets.Get(refs[k].Name); err != nil {
				f.AddLoss(LossSecretMissing, s.Name,
					"env %s references secret %q, which is not stored; run `agentbridge secret set %s`",
					k, refs[k].Name, refs[k].Name)
				return s, nil, false
			}
		}
	}

	if opts.Launcher == "" {
		f.AddLoss(LossSecretNoLauncher, s.Name,
			"env references a stored secret, but no launcher is available to inject it")
		return s, nil, false
	}

	out := s
	out.Env = map[string]string{}
	var secretArgs []string
	for _, k := range sortedMapKeys(s.Env) {
		if ref, ok := refs[k]; ok {
			secretArgs = append(secretArgs, "--secret", k+"="+ref.Name)
			continue
		}
		out.Env[k] = s.Env[k]
	}

	args := append([]string{"run"}, secretArgs...)
	args = append(args, "--", s.Command)
	args = append(args, s.Args...)

	out.Command = opts.Launcher
	out.Args = args

	notes = append(notes, SecretNote{
		Server: s.Name,
		Detail: fmt.Sprintf("launched via agentbridge so %d secret(s) are read from the credential store at start, not written to disk", len(refs)),
	})
	return out, notes, true
}

// LauncherPath returns the absolute path of the running binary, for servers
// that need secrets injected.
//
// A failure here is not fatal to an install: it only means secret references
// cannot be used, which is reported per server rather than aborting everything.
func LauncherPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	resolved, err := os.Readlink(exe)
	if err == nil && resolved != "" {
		// A symlinked binary is fine to record, but the target is more stable
		// across upgrades that replace the link.
		if abs := resolveRelative(exe, resolved); abs != "" {
			exe = abs
		}
	}
	return exe
}

func resolveRelative(link, target string) string {
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(filepath.Dir(link), target)
}

func sortStrings(s []string) { sort.Strings(s) }

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortedRefKeys(m map[string]secrets.Ref) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}
