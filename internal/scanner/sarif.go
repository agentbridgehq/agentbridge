package scanner

import (
	"encoding/json"
	"sort"
)

// SARIF output.
//
// Chosen because it removes an integration argument rather than because it is
// pleasant: GitHub code scanning, GitLab, and every enterprise security
// dashboard already ingest it. A finding that appears where a security team
// already looks gets acted on; one that requires them to run a new tool does
// not.
//
// The schema is SARIF 2.1.0, hand-built rather than pulled from a library. The
// subset needed here is small, and a dependency in a security tool's output
// path is a dependency in its threat model.

const (
	sarifVersion = "2.1.0"
	sarifSchema  = "https://json.schemastore.org/sarif-2.1.0.json"
)

// SARIF renders a report in the interchange format.
func (r *Report) SARIF(toolVersion string) ([]byte, error) {
	rules := usedRules(r)

	type message struct {
		Text string `json:"text"`
	}
	type artifactLocation struct {
		URI string `json:"uri"`
	}
	type region struct {
		StartLine int `json:"startLine,omitempty"`
	}
	type physicalLocation struct {
		ArtifactLocation artifactLocation `json:"artifactLocation"`
		Region           *region          `json:"region,omitempty"`
	}
	type location struct {
		PhysicalLocation physicalLocation `json:"physicalLocation"`
	}
	type result struct {
		RuleID    string     `json:"ruleId"`
		Level     string     `json:"level"`
		Message   message    `json:"message"`
		Locations []location `json:"locations,omitempty"`
	}
	type ruleDescription struct {
		Text string `json:"text"`
	}
	type ruleProperties struct {
		Tags             []string `json:"tags,omitempty"`
		SecuritySeverity string   `json:"security-severity,omitempty"`
	}
	type reportingDescriptor struct {
		ID               string          `json:"id"`
		Name             string          `json:"name"`
		ShortDescription ruleDescription `json:"shortDescription"`
		FullDescription  ruleDescription `json:"fullDescription"`
		Help             ruleDescription `json:"help"`
		Properties       ruleProperties  `json:"properties"`
	}
	type driver struct {
		Name           string                `json:"name"`
		Version        string                `json:"version"`
		InformationURI string                `json:"informationUri"`
		Rules          []reportingDescriptor `json:"rules"`
	}
	type tool struct {
		Driver driver `json:"driver"`
	}
	type run struct {
		Tool    tool     `json:"tool"`
		Results []result `json:"results"`
	}
	type document struct {
		Schema  string `json:"$schema"`
		Version string `json:"version"`
		Runs    []run  `json:"runs"`
	}

	descriptors := make([]reportingDescriptor, 0, len(rules))
	for _, rule := range rules {
		descriptors = append(descriptors, reportingDescriptor{
			ID:               rule.ID,
			Name:             rule.Title,
			ShortDescription: ruleDescription{Text: rule.Title},
			FullDescription:  ruleDescription{Text: rule.Rationale},
			Help:             ruleDescription{Text: rule.Rationale + "\n\n" + rule.Remedy},
			Properties: ruleProperties{
				Tags:             []string{"security", "agent-plugins", "prompt-injection"},
				SecuritySeverity: securityScore(rule.Severity),
			},
		})
	}

	results := make([]result, 0, len(r.Findings))
	for _, f := range r.Findings {
		res := result{
			RuleID:  f.RuleID,
			Level:   sarifLevel(f.Severity),
			Message: message{Text: f.Message},
		}
		if f.File != "" {
			loc := location{PhysicalLocation: physicalLocation{
				ArtifactLocation: artifactLocation{URI: f.File},
			}}
			if f.Line > 0 {
				loc.PhysicalLocation.Region = &region{StartLine: f.Line}
			}
			res.Locations = []location{loc}
		}
		results = append(results, res)
	}

	doc := document{
		Schema:  sarifSchema,
		Version: sarifVersion,
		Runs: []run{{
			Tool: tool{Driver: driver{
				Name:           "agentbridge",
				Version:        toolVersion,
				InformationURI: "https://github.com/agentbridgehq/agentbridge",
				Rules:          descriptors,
			}},
			Results: results,
		}},
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// usedRules returns the documentation for rules this report actually triggered.
//
// Emitting the whole catalogue would put rules in a dashboard that nothing
// matched, which reads as noise the first time and as coverage the second.
func usedRules(r *Report) []Rule {
	seen := map[string]bool{}
	var out []Rule
	for _, f := range r.Findings {
		if seen[f.RuleID] {
			continue
		}
		seen[f.RuleID] = true
		if rule, ok := Lookup(f.RuleID); ok {
			out = append(out, rule)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sarifLevel(s Severity) string {
	switch s {
	case High:
		return "error"
	case Medium:
		return "warning"
	case Low:
		return "note"
	}
	return "note"
}

// securityScore maps severity onto the CVSS-like scale GitHub uses to decide
// what blocks a pull request.
func securityScore(s Severity) string {
	switch s {
	case High:
		return "8.0"
	case Medium:
		return "5.0"
	case Low:
		return "3.0"
	}
	return "0.0"
}
