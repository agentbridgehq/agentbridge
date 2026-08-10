// Package validate checks a plugin against Agent Plugins Specification v1.0.0
// from the author's side.
//
// This is the mirror of the importer. The importer is deliberately forgiving,
// because the specification requires a client to load what it can and report
// the rest — a user should not lose a working plugin over one bad server entry.
// An author wants the opposite: every deviation, including the ones a client is
// obliged to tolerate, so they can fix them before publishing.
//
// Three classes of finding, and the distinction matters because the spec draws
// it explicitly:
//
//   - **Violations** — a MUST. The plugin is not conformant.
//   - **Advisories** — a SHOULD, or a MUST that binds the *plugin author*
//     rather than the client. §9.2 forbids secrets in `env`; a client cannot
//     enforce that, so nothing else in this codebase will ever tell an author
//     they have done it.
//   - **Notes** — things a conformant client will tolerate but report, such as
//     an unknown top-level field. §5.4 is emphatic that a client MUST NOT
//     reject a plugin for a non-SemVer version or a non-SPDX licence, so those
//     are advisories here and never violations.
package validate

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/agentbridge/agentbridge/internal/diag"
	"github.com/agentbridge/agentbridge/internal/importer"
	"github.com/agentbridge/agentbridge/internal/importer/agentplugins"
	"github.com/agentbridge/agentbridge/internal/importer/registry"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
	"github.com/agentbridge/agentbridge/internal/schema"
)

// Severity of a finding.
type Severity string

const (
	// Violation is a MUST that was broken. Not conformant.
	Violation Severity = "violation"
	// Advisory is a SHOULD, or a MUST that binds the author rather than a
	// client and so cannot be enforced at load time.
	Advisory Severity = "advisory"
	// Note is tolerated by a conformant client but worth knowing.
	Note Severity = "note"
)

// Finding is one result.
type Finding struct {
	Severity Severity `json:"severity"`
	// Section cites the specification clause, so an author can check the claim
	// rather than take our word for it.
	Section string `json:"section"`
	Message string `json:"message"`
	Where   string `json:"where,omitempty"`
}

// Report is the outcome of validating one plugin.
type Report struct {
	Dir      string    `json:"dir"`
	Dialect  string    `json:"dialect"`
	Name     string    `json:"name,omitempty"`
	Findings []Finding `json:"findings"`
	// Loaded is false when the plugin was rejected outright.
	Loaded bool `json:"loaded"`
}

// Conformant reports whether the plugin has no violations.
func (r *Report) Conformant() bool {
	return r.Loaded && r.Count(Violation) == 0
}

// Portable reports whether the plugin could be published as an Agent Plugins
// package as it stands. For a plugin already in the portable format this is the
// same question as conformance; for another dialect it is the more useful one,
// since the manifest rules do not bind a plugin that is not claiming to follow
// them.
func (r *Report) Portable() bool {
	return r.Conformant() && r.Count(Advisory) == 0
}

// ForeignDialect reports whether the plugin is in a dialect other than Agent
// Plugins.
func (r *Report) ForeignDialect() bool {
	return r.Loaded && r.Dialect != string(ir.DialectAgentPlugins)
}

// Count returns the number of findings at a severity.
func (r *Report) Count(sev Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

func (r *Report) add(sev Severity, section, where, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{
		Severity: sev,
		Section:  section,
		Message:  fmt.Sprintf(format, args...),
		Where:    where,
	})
}

var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// Run validates the plugin at dir.
func Run(dir string) (*Report, error) {
	report := &Report{Dir: dir}

	result, err := registry.Open(dir)
	if err != nil {
		report.add(Violation, "§5", agentplugins.ManifestPath, "plugin was rejected: %v", err)
		return report, nil
	}
	report.Loaded = true
	report.Dialect = string(result.Plugin.Origin.Dialect)
	report.Name = result.Plugin.Name

	// A plugin in another dialect can be checked for what it would need in
	// order to become portable, but the manifest rules do not apply to it yet.
	if result.Plugin.Origin.Dialect != ir.DialectAgentPlugins {
		report.add(Note, "§5.1",
			"", "this is a %s plugin, not an Agent Plugins package; it has no root plugin.json, so only portability of its components is checked",
			result.Plugin.Origin.Dialect)
	} else {
		checkStrictManifest(dir, report)
	}

	carryDiagnostics(result.Diagnostics, report)
	checkAuthorObligations(result, report)
	return report, nil
}

// checkStrictManifest applies the closed-schema rules an author cares about but
// a client is required to tolerate.
func checkStrictManifest(dir string, report *Report) {
	root, err := safepath.NewRoot(dir)
	if err != nil {
		return
	}
	_, decoded, err := importer.ReadJSON(root, agentplugins.ManifestPath)
	if err != nil {
		return
	}

	// The loader relaxes two constraints because §5.2 requires clients to
	// tolerate them. An author should still hear about both.
	if err := schema.ValidatePluginManifestStrict(decoded); err != nil {
		report.add(Advisory, "§5.2", agentplugins.ManifestPath,
			"manifest does not satisfy the closed schema: %v", err)
	}
}

// carryDiagnostics translates loader diagnostics into findings.
//
// Severity is deliberately re-mapped rather than copied: an Error diagnostic
// means a client had to skip a component, which for an author is a conformance
// violation, not a mere warning.
func carryDiagnostics(ds diag.Diagnostics, report *Report) {
	for _, d := range ds {
		// checkAuthorObligations reports this one with the specification
		// citation an author needs, so carrying it here too would say the same
		// thing twice in different words.
		if d.Code == diag.CodeSecretLiteralInEnv {
			continue
		}
		section, sev := classify(d)
		where := strings.TrimSpace(d.Path + " " + d.Component)
		report.add(sev, section, where, "%s", d.Message)
	}
}

func classify(d diag.Diagnostic) (string, Severity) {
	switch d.Code {
	case diag.CodeManifestUnknownField:
		// §5.2: clients report and ignore these, so the plugin still loads —
		// but an unknown top-level field is a schema violation and almost
		// always a typo.
		return "§5.2", Advisory
	case diag.CodeManifestBadExtensions:
		return "§8.1", Advisory
	case diag.CodeManifestInvalidName:
		return "§5.5", Violation
	case diag.CodeVersionMismatch, diag.CodeUnsupportedSpecVer:
		return "§10.1", Violation
	case diag.CodeMCPInvalidCommand:
		return "§7.2.1", Violation
	case diag.CodeMCPInsecureURL, diag.CodeMCPInvalidURLForm:
		return "§7.2.1", Violation
	case diag.CodeMCPInvalidHeader, diag.CodeMCPDuplicateHeader:
		return "§7.2.1", Violation
	case diag.CodeMCPReservedEnv:
		return "§9.2", Violation
	case diag.CodeMCPCwdUncontained, diag.CodePathEscape:
		return "§4.1", Violation
	case diag.CodeSkillNotRegularFile, diag.CodeSkillsNotDirectory:
		return "§6.2", Violation
	case diag.CodeMCPNotRegularFile:
		return "§6.2", Violation
	case diag.CodeSkillInvalidFrontmat:
		return "§7.1", Violation
	case diag.CodeSkillNoName, diag.CodeSkillMissingFile, diag.CodeSkillDuplicate:
		return "§7.1", Advisory
	case diag.CodeMCPServerInvalid:
		return "§7.2.1", Violation
	case diag.CodeComponentUnsupport:
		// Not a specification violation: the component simply has no portable
		// equivalent. The author needs to know before publishing, but the
		// plugin is not breaking a rule by having it.
		return "§7", Advisory
	case diag.CodeSkillFlatCommand:
		return "§7.1", Advisory
	}

	if d.Severity == diag.Error {
		return "§11.3", Violation
	}
	return "", Note
}

// checkAuthorObligations covers requirements that bind the plugin author and
// which therefore no client will ever report.
func checkAuthorObligations(result *importer.Result, report *Report) {
	p := result.Plugin

	// §5.4: SemVer and SPDX are RECOMMENDED, and a client MUST NOT reject a
	// plugin for either. Advisory only, and never a violation.
	if p.Version == "" {
		report.add(Advisory, "§5.4", agentplugins.ManifestPath,
			"no version; clients use it for update checks and cache freshness")
	} else if !semverPattern.MatchString(p.Version) {
		report.add(Advisory, "§10.2", agentplugins.ManifestPath,
			"version %q is not Semantic Versioning, which the specification recommends", p.Version)
	}
	if p.License == "" {
		report.add(Advisory, "§5.4", agentplugins.ManifestPath,
			"no license; an SPDX identifier is recommended")
	}
	if p.Description == "" {
		report.add(Advisory, "§5.4", agentplugins.ManifestPath, "no description")
	}

	for _, s := range p.MCPServers {
		// §9.2 and §7.2.1 both state, normatively, that these are visible
		// package data and MUST NOT carry secrets. A client cannot enforce
		// this, so an author will not hear it anywhere else.
		for _, k := range importer.SortedKeys(s.Env) {
			if looksSecret(k) && s.Env[k] != "" && !strings.Contains(s.Env[k], "${") {
				report.add(Advisory, "§9.2", "mcp.json "+s.Name,
					"env %s looks like a credential; env values are visible package data and the specification forbids embedding secrets in them", k)
			}
		}
		for _, k := range importer.SortedKeys(s.Headers) {
			if strings.EqualFold(k, "authorization") || looksSecret(k) {
				report.add(Advisory, "§7.2.1", "mcp.json "+s.Name,
					"header %s looks like a credential; header values are visible package data and the specification forbids embedding secrets in them", k)
			}
		}
		// §7.2.1: a plugin bundling an executable must use a plugin-relative
		// command. A bare name that also exists in the package is very likely
		// an author mistake.
		if s.Transport == ir.TransportStdio && !strings.HasPrefix(s.Command, "./") {
			if _, err := os.Stat(p.Origin.Root + "/" + s.Command); err == nil {
				report.add(Advisory, "§7.2.1", "mcp.json "+s.Name,
					"command %q is resolved by the platform executable search, but a file of that name ships in the package; use \"./%s\" for deterministic execution",
					s.Command, s.Command)
			}
		}
	}

	if len(p.Skills) == 0 && len(p.MCPServers) == 0 {
		report.add(Advisory, "§7", "", "plugin declares no skills and no MCP servers")
	}
}

func looksSecret(name string) bool {
	upper := strings.ToUpper(name)
	for _, needle := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "APIKEY", "API_KEY", "ACCESS_KEY", "PRIVATE_KEY"} {
		if strings.Contains(upper, needle) {
			return true
		}
	}
	return false
}

// JSON renders the report for machine consumers.
func (r *Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
