// Package capability infers what access a plugin can obtain.
//
// Nothing in the Agent Plugins format requires a plugin to declare what it
// does, so this is inference from evidence, and it is deliberately
// conservative: over-reporting a capability costs a user one glance at the
// evidence, while under-reporting it means a policy decision was made on a
// false premise.
//
// Scope note: this is not the scanner. Skill analysis here is limited to
// unambiguous references to credential locations, which is enough to populate
// the `secrets` capability honestly. The real prompt-injection classifier —
// obfuscation, instruction-override phrasing, exfiltration patterns, LLM
// classification — is Phase 2 work described in docs/05-security-and-trust.md.
// Nothing here should be mistaken for a security verdict.
package capability

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/agentbridge/agentbridge/internal/diag"
	"github.com/agentbridge/agentbridge/internal/ir"
)

// secretEnvPattern matches environment variable names that conventionally hold
// credentials. Matching the name, not the value, is what makes this useful on
// a manifest whose values are placeholders.
var secretEnvPattern = regexp.MustCompile(`(?i)(^|_)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|APIKEY|API_KEY|ACCESS_KEY|PRIVATE_KEY|CLIENT_SECRET|AUTH)($|_)`)

// credentialPaths are locations whose appearance in skill instructions means
// the skill is steering the agent toward credentials. These are literal,
// low-ambiguity strings; anything requiring judgement belongs in the scanner.
var credentialPaths = []string{
	"~/.aws/credentials",
	".aws/credentials",
	"~/.ssh/id_rsa",
	".ssh/id_rsa",
	"~/.ssh/id_ed25519",
	"~/.netrc",
	"~/.npmrc",
	"~/.docker/config.json",
	"~/.kube/config",
	"~/.config/gcloud",
	"id_rsa",
	".env.local",
	".env.production",
}

// Infer computes the capability set for a plugin from its MCP servers, and
// from skill bodies supplied by the caller.
//
// skillBodies is keyed by skill name. It is passed separately because skill
// bodies are intentionally absent from the IR: inference runs at import time
// while the content is in hand.
func Infer(p *ir.Plugin, skillBodies map[string]string) diag.Diagnostics {
	var ds diag.Diagnostics
	caps := ir.Capabilities{}

	for _, s := range p.MCPServers {
		inferServer(&caps, &ds, s)
	}
	for _, s := range p.Skills {
		body, ok := skillBodies[s.Name]
		if !ok {
			continue
		}
		inferSkill(&caps, &ds, s.Name, body)
	}

	p.Capabilities = caps
	return ds
}

func inferServer(caps *ir.Capabilities, ds *diag.Diagnostics, s ir.MCPServer) {
	switch s.Transport {
	case ir.TransportStdio:
		// A stdio server is a process launched on the user's machine with the
		// user's privileges. There is no weaker reading of this.
		caps.Set(ir.CapExec, s.Name,
			fmt.Sprintf("stdio server runs %q on this machine", s.Command))
		caps.Set(ir.CapFilesystem, s.Name,
			"a local process inherits the user's filesystem access")
	case ir.TransportStreamableHTTP, ir.TransportSSE:
		host := s.URL
		if u, err := url.Parse(s.URL); err == nil && u.Host != "" {
			host = u.Host
		}
		caps.Set(ir.CapNetwork, s.Name,
			fmt.Sprintf("%s server connects to %s", s.Transport, host))
	}

	for name := range s.Env {
		if !secretEnvPattern.MatchString(name) {
			continue
		}
		caps.Set(ir.CapSecrets, s.Name,
			fmt.Sprintf("env %s appears to carry a credential", name))
		// The value being a literal rather than a placeholder is the part that
		// matters: it means a secret is sitting in a file that will be
		// committed. M5 replaces these with secret references.
		if v := s.Env[name]; v != "" && !isPlaceholder(v) {
			ds.AddComponent(diag.Warning, diag.CodeSecretLiteralInEnv, "mcp.json", s.Name,
				"env %s holds a literal value; secrets in a manifest end up in version control", name)
		}
	}

	for name := range s.Headers {
		if strings.EqualFold(name, "authorization") || secretEnvPattern.MatchString(name) {
			caps.Set(ir.CapSecrets, s.Name,
				fmt.Sprintf("header %s carries a credential", name))
		}
	}
}

func inferSkill(caps *ir.Capabilities, ds *diag.Diagnostics, name, body string) {
	lower := strings.ToLower(body)

	// The patterns overlap by design — "~/.aws/credentials" contains
	// ".aws/credentials" — so a single mention would otherwise be reported
	// several times. Longest match wins: report the most specific form and
	// suppress the substrings it already covers.
	var reported []string
	for _, path := range credentialPathsByLength() {
		if !strings.Contains(lower, strings.ToLower(path)) {
			continue
		}
		if coveredBy(reported, path) {
			continue
		}
		reported = append(reported, path)

		caps.Set(ir.CapSecrets, name,
			fmt.Sprintf("skill instructions reference %s", path))
		ds.AddComponent(diag.Warning, diag.CodeSkillReadsCredsHint, "", name,
			"skill instructions reference %s; a skill is instruction text loaded into an agent with tool access, so review what it directs the agent to do with it", path)
	}
}

// credentialPathsByLength orders the patterns longest-first so the most
// specific match is the one reported.
func credentialPathsByLength() []string {
	out := append([]string(nil), credentialPaths...)
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func coveredBy(reported []string, candidate string) bool {
	for _, r := range reported {
		if strings.Contains(strings.ToLower(r), strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func isPlaceholder(v string) bool {
	return strings.Contains(v, ir.PlaceholderPluginRoot) ||
		strings.Contains(v, ir.PlaceholderPluginData) ||
		(strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}"))
}
