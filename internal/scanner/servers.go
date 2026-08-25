package scanner

import (
	"fmt"
	"net/url"
	"sort"

	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/secrets"
)

// Server-side rules.
//
// Deliberately few. Most of what could be checked here — transport security,
// command form, working-directory containment, reserved environment names — is
// already enforced at load time by the importer, which rejects the server
// outright rather than reporting it. Repeating those checks would produce
// findings for configurations that can never reach a client.
//
// What is left are the two things a *valid* server configuration can still tell
// a reviewer: it carries a credential in plain sight, or it sends whatever the
// agent gives it to somebody else's machine.
func scanServers(p *ir.Plugin) []Finding {
	var out []Finding

	servers := append([]ir.MCPServer(nil), p.MCPServers...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

	for _, s := range servers {
		for _, f := range secrets.DetectAll(s.Env) {
			// A secret reference is the correct way to do this and must not be
			// reported as though it were the problem.
			if secrets.IsRef(s.Env[f.Key]) {
				continue
			}
			out = append(out, Finding{
				RuleID:   RuleServerSecretLiteral,
				Severity: severityForCredential(f.Confidence),
				Title:    catalog[RuleServerSecretLiteral].Title,
				Message: fmt.Sprintf("server %q has env %s in plain text: %s",
					s.Name, f.Key, f.Reason),
				File:    "mcp.json",
				Excerpt: secrets.Mask(s.Env[f.Key]),
			})
		}

		if s.Transport == ir.TransportStdio {
			continue
		}
		host := s.URL
		if u, err := url.Parse(s.URL); err == nil && u.Host != "" {
			host = u.Host
		}
		out = append(out, Finding{
			RuleID:   RuleServerRemoteEgress,
			Severity: catalog[RuleServerRemoteEgress].Severity,
			Title:    catalog[RuleServerRemoteEgress].Title,
			Message: fmt.Sprintf("server %q connects to %s, so anything the agent passes to its tools leaves this machine",
				s.Name, host),
			File:    "mcp.json",
			Excerpt: host,
		})
	}

	return out
}

// severityForCredential maps detection confidence onto severity.
//
// A value that identifies itself as an issuer's token — an `sk-` or `ghp_`
// prefix — is a live credential in a file people commit, and worth stopping
// for. A variable merely *named* like a credential holding something short is
// far more often a placeholder, and grading that as High would train people to
// ignore the ones that matter.
func severityForCredential(c secrets.Confidence) Severity {
	switch c {
	case secrets.High:
		return High
	case secrets.Medium:
		return Medium
	default:
		return Low
	}
}
