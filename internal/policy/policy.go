// Package policy enforces org rules about what may be installed.
//
// The rest of this tool answers "what would happen if I installed this?".
// Policy answers "am I allowed to?", and the difference matters to a different
// person: a platform or security lead who is not at the keyboard when the
// install runs and needs the answer to hold without them.
//
// Three properties shape the design.
//
// A policy is a file in a repository, not a service. There is no account to
// create, nothing to log in to, and no network call on the enforcement path —
// so it works in an air-gapped build, and a team can adopt it in an afternoon
// without procurement. The trade is that it binds the machines that run this
// binary against that file, and nothing else; it is a guardrail, not a
// sandbox, and it is documented as one.
//
// Policies restrict and never grant. Every policy found is evaluated and any
// violation blocks, so adding a second policy can only ever narrow what is
// allowed. That rules out the failure where a user-level file quietly
// re-permits what a project forbade, which is the whole reason to have a
// project-level one.
//
// It refuses to guess. An unreadable or malformed policy is an error rather
// than an empty policy, because a rule that silently stops applying is worse
// than no rule: the enforcement disappears and the file stays in the
// repository, still looking like protection.
package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/ir"
	"gopkg.in/yaml.v3"
)

// FileName is the policy file, alongside agentbridge.yaml.
const FileName = "agentbridge.policy.yaml"

// LocalPolicy says what to do with a plugin installed from a directory on this
// machine rather than from a pinned remote.
type LocalPolicy string

const (
	// LocalAllow permits local directories regardless of the source rules. The
	// default, because forbidding it stops a plugin author testing their own
	// work, and an org that has not said otherwise did not mean to.
	LocalAllow LocalPolicy = "allow"
	// LocalDeny forbids them, which is what closes the obvious hole in an
	// allow-list: copy the package to /tmp and install from there.
	LocalDeny LocalPolicy = "deny"
)

// Policy is one policy file.
type Policy struct {
	Version int `yaml:"version" json:"version"`

	// The json tags matter as much as the yaml ones. Without them this
	// marshals as Sources.Allow while the file it describes says
	// sources.allow, so `agentbridge policy --json` would disagree with the
	// document it is reporting on and with every other --json in this tool.
	Sources struct {
		// Allow is a list of source patterns. An empty list allows any remote;
		// a non-empty one allows only what matches.
		Allow []string `yaml:"allow" json:"allow,omitempty"`
		// Deny is checked first and wins, so a broad allow can carry a
		// specific exception without being rewritten.
		Deny  []string    `yaml:"deny" json:"deny,omitempty"`
		Local LocalPolicy `yaml:"local" json:"local"`
	} `yaml:"sources" json:"sources"`

	Capabilities struct {
		// Deny names capabilities a plugin may not have: exec, network,
		// filesystem, secrets. These are inferred from the package rather than
		// declared by it, so this is a ceiling on what a plugin can reach,
		// checked against evidence rather than against a promise.
		Deny []string `yaml:"deny" json:"deny,omitempty"`
	} `yaml:"capabilities" json:"capabilities"`

	Plugins struct {
		// Deny names plugins by name, for the specific thing an org has
		// decided against.
		Deny []string `yaml:"deny" json:"deny,omitempty"`
	} `yaml:"plugins" json:"plugins"`

	// Path is where this was read from. Carried so a refusal can name the file
	// that caused it — a rule the reader cannot locate is a rule they cannot
	// change.
	Path string `yaml:"-" json:"path"`
}

// Subject is what a policy is asked about.
//
// Deliberately not a source.Ref: policy has no business knowing how a
// reference is fetched, and keeping the dependency out means the rules can be
// evaluated against anything that can describe itself this way.
type Subject struct {
	Name string
	// Source is the normalized reference: the URL for a remote, without tag or
	// digest, and the directory for a local one.
	Source       string
	Local        bool
	Capabilities ir.Capabilities
}

// Violation is one rule a subject broke.
type Violation struct {
	// Policy is the file that carried the rule.
	Policy string `json:"policy"`
	// Rule is the dotted path of the setting, so it can be found by searching.
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: %s (%s)", v.Rule, v.Detail, v.Policy)
}

// Load reads one policy file. A missing file is not an error and yields nil,
// because most repositories have no policy and that is not a problem to report.
func Load(path string) (*Policy, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// A file with no documents in it — empty, or nothing but comments —
	// decodes as io.EOF, which reaches the user as the bare word "EOF". That
	// is the state somebody is in the moment they create the file and before
	// they type anything into it, so it is worth a sentence rather than a
	// error code.
	if strings.TrimSpace(stripComments(string(raw))) == "" {
		return nil, fmt.Errorf("%s: the policy file is empty, so no rule is being enforced; delete it, or give it at least `version: 1`", path)
	}

	var p Policy
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	// Unknown fields are an error here, unlike in a plugin manifest. §5.2 asks
	// clients to ignore what they do not understand so a package stays
	// portable across versions; a policy has the opposite requirement, because
	// a misspelled rule that is ignored reads exactly like a rule that is being
	// enforced.
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if p.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported policy version %d; this build understands version 1", path, p.Version)
	}
	if p.Sources.Local == "" {
		p.Sources.Local = LocalAllow
	}
	if p.Sources.Local != LocalAllow && p.Sources.Local != LocalDeny {
		return nil, fmt.Errorf("%s: sources.local is %q; expected %q or %q", path, p.Sources.Local, LocalAllow, LocalDeny)
	}
	for _, c := range p.Capabilities.Deny {
		if !knownCapability(c) {
			return nil, fmt.Errorf("%s: capabilities.deny names %q, which is not a capability this tool infers (%s)",
				path, c, strings.Join(capabilityNames(), ", "))
		}
	}
	p.Path = path
	return &p, nil
}

// Discover finds every policy that applies, nearest first.
//
// The walk goes up from the working directory so a policy applies from
// anywhere inside a repository rather than only from the top of it — otherwise
// `cd services/api && agentbridge install …` escapes it.
//
// It deliberately does NOT stop at a .git boundary, which is where
// lockfile.FindProjectRoot stops. The two are asking different questions and
// the asymmetry is the point. Finding a project means finding *this* project,
// and beyond a repository boundary you are in someone else's. Enforcing a
// policy means not missing one, and the two failure modes are not comparable:
// applying a policy from a directory above the repository is visible — the
// refusal names the file, and `agentbridge policy` lists every file in force —
// whereas failing to apply one is silent, and silence in a security control is
// the failure that matters. A monorepo with a nested repository inside it
// would otherwise let that subtree slip out from under the org's rules without
// anyone seeing it happen.
//
// The cost is that a stray policy file high in a home directory affects
// everything beneath it. That is why the listing exists.
func Discover(workDir, userDir string) ([]*Policy, error) {
	var out []*Policy
	seen := map[string]bool{}

	dir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	for {
		path := filepath.Join(dir, FileName)
		if !seen[path] {
			seen[path] = true
			p, err := Load(path)
			if err != nil {
				return nil, err
			}
			if p != nil {
				out = append(out, p)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	if userDir != "" {
		path := filepath.Join(userDir, FileName)
		if !seen[path] {
			p, err := Load(path)
			if err != nil {
				return nil, err
			}
			if p != nil {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// Check evaluates every policy and returns everything the subject broke.
//
// All violations are returned rather than the first, because a person fixing
// this wants the whole list: discovering a second refusal after satisfying the
// first is how a five-minute change becomes an afternoon.
func Check(policies []*Policy, s Subject) []Violation {
	var out []Violation
	for _, p := range policies {
		out = append(out, p.check(s)...)
	}
	return out
}

func (p *Policy) check(s Subject) []Violation {
	var out []Violation
	add := func(rule, format string, args ...any) {
		out = append(out, Violation{Policy: p.Path, Rule: rule, Detail: fmt.Sprintf(format, args...)})
	}

	for _, name := range p.Plugins.Deny {
		if name == s.Name {
			add("plugins.deny", "%s is denied by name", s.Name)
		}
	}

	if s.Local {
		if p.Sources.Local == LocalDeny {
			add("sources.local", "installing from a local directory is not allowed; %s is one", s.Source)
		}
	} else {
		for _, pattern := range p.Sources.Deny {
			if match(pattern, s.Source) {
				add("sources.deny", "%s matches the denied pattern %s", s.Source, pattern)
			}
		}
		if len(p.Sources.Allow) > 0 && !anyMatch(p.Sources.Allow, s.Source) {
			add("sources.allow", "%s matches none of the allowed patterns: %s",
				s.Source, strings.Join(p.Sources.Allow, ", "))
		}
	}

	for _, name := range p.Capabilities.Deny {
		if hasCapability(s.Capabilities, name) {
			add("capabilities.deny", "%s has the %s capability, which is denied; run `agentbridge inspect` for the evidence",
				s.Name, name)
		}
	}
	return out
}

func anyMatch(patterns []string, s string) bool {
	for _, p := range patterns {
		if match(p, s) {
			return true
		}
	}
	return false
}

// match reports whether a source matches a pattern.
//
// `*` matches within one path segment and `**` crosses them, which is the
// convention every developer already knows from .gitignore and CI path
// filters. Getting this wrong is dangerous in one direction only: a pattern
// that matches more than the author meant silently widens an allow-list, so
// `*` deliberately stops at a separator and reaching further has to be asked
// for.
func match(pattern, s string) bool {
	// A trailing /** is the common case and reads as "this prefix and anything
	// under it"; without this it would not match the prefix itself.
	if strings.HasSuffix(pattern, "/**") && s == strings.TrimSuffix(pattern, "/**") {
		return true
	}
	return matchParts(splitPattern(pattern), s)
}

func splitPattern(p string) []string {
	var parts []string
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '*' {
			b.WriteByte(p[i])
			continue
		}
		if b.Len() > 0 {
			parts = append(parts, b.String())
			b.Reset()
		}
		if i+1 < len(p) && p[i+1] == '*' {
			parts = append(parts, "**")
			i++
			continue
		}
		parts = append(parts, "*")
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func matchParts(parts []string, s string) bool {
	if len(parts) == 0 {
		return s == ""
	}
	switch parts[0] {
	case "*", "**":
		crossesSeparator := parts[0] == "**"
		for i := 0; i <= len(s); i++ {
			if !crossesSeparator && i > 0 && s[i-1] == '/' {
				break
			}
			if matchParts(parts[1:], s[i:]) {
				return true
			}
		}
		return false
	default:
		if !strings.HasPrefix(s, parts[0]) {
			return false
		}
		return matchParts(parts[1:], s[len(parts[0]):])
	}
}

func hasCapability(c ir.Capabilities, name string) bool {
	switch ir.Capability(name) {
	case ir.CapExec:
		return c.Exec
	case ir.CapNetwork:
		return c.Network
	case ir.CapFilesystem:
		return c.Filesystem
	case ir.CapSecrets:
		return c.Secrets
	}
	return false
}

// stripComments removes whole-line YAML comments, only well enough to tell an
// empty document from one carrying rules. Nothing downstream parses with this.
func stripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func knownCapability(name string) bool {
	for _, c := range capabilityNames() {
		if c == name {
			return true
		}
	}
	return false
}

func capabilityNames() []string {
	names := []string{string(ir.CapExec), string(ir.CapNetwork), string(ir.CapFilesystem), string(ir.CapSecrets)}
	sort.Strings(names)
	return names
}
