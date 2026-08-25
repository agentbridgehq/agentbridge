// Package scanner inspects a plugin for content that would steer an agent
// somewhere its user did not intend.
//
// A `SKILL.md` is natural-language text loaded into a model's context that
// directs the agent's behaviour. It is code for a probabilistic interpreter
// holding tools — and no mainstream security product inspects it. SCA reads
// dependency manifests, SAST reads source, EDR watches processes, and an MCP
// gateway sees tool calls. A sentence telling an agent to read
// `~/.aws/credentials` before answering database questions passes all of them.
//
// What this is and is not:
//
//   - It is a **local, offline heuristic scanner by default**. No network, no
//     model, no account — that is what makes it usable in the environments
//     where it matters most, and internal/privacy enforces it.
//   - There is an **optional model pass** (see classify.go) which is off unless
//     a caller supplies a classifier, sends nothing anywhere until then, and
//     talks only to an endpoint the user configured. It can only ever *add*
//     findings; it cannot clear one the rules produced.
//   - It is **not** a verdict. Every rule here can be triggered by legitimate
//     content — a security plugin discussing prompt injection will match the
//     prompt-injection rules. Findings are evidence for a person, not grounds
//     for a machine to block something silently.
//
// The design constraint that shapes everything: **false positives are the real
// failure mode.** A scanner that fires on ordinary plugins gets muted within a
// week, and a muted scanner is worse than none because it produces the
// appearance of coverage. So rules are calibrated to be quiet, severity is
// assigned by how hard the pattern is to reach innocently, and anything
// speculative is Info rather than High.
package scanner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// Severity grades a finding.
type Severity string

const (
	// High means the content is very unlikely to be innocent and would change
	// what an agent does. Installing without reading it is a mistake.
	High Severity = "high"
	// Medium means suspicious and worth a human deciding.
	Medium Severity = "medium"
	// Low means notable but commonly benign.
	Low Severity = "low"
	// Info records something worth knowing that is not itself a problem.
	Info Severity = "info"
)

// rank orders severities for sorting and thresholds.
func (s Severity) rank() int {
	switch s {
	case High:
		return 3
	case Medium:
		return 2
	case Low:
		return 1
	}
	return 0
}

// AtLeast reports whether s is at least as severe as min.
func (s Severity) AtLeast(min Severity) bool { return s.rank() >= min.rank() }

// ParseSeverity reads a severity name.
func ParseSeverity(s string) (Severity, error) {
	switch Severity(strings.ToLower(s)) {
	case High:
		return High, nil
	case Medium:
		return Medium, nil
	case Low:
		return Low, nil
	case Info:
		return Info, nil
	}
	return "", fmt.Errorf("unknown severity %q: use high, medium, low or info", s)
}

// Source records what produced a finding.
type Source string

const (
	// SourceRule is a deterministic match. Re-running produces it again.
	SourceRule Source = "rule"
	// SourceModel is a model's judgement. It is evidence of a different kind:
	// it reaches phrasing no pattern anticipated, and it is not reproducible.
	// A reader weighing a finding needs to know which of the two they have.
	SourceModel Source = "model"
)

// Finding is one thing worth a person's attention.
type Finding struct {
	RuleID   string   `json:"ruleId"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	// Message states what was found, in terms of what it would do.
	Message string `json:"message"`
	// File is plugin-relative; Line is 1-indexed. Both are zero for a finding
	// about the plugin as a whole.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// Excerpt is the matching text, truncated and stripped of control
	// characters so that printing a finding cannot itself smuggle an escape
	// sequence into a terminal.
	Excerpt string `json:"excerpt,omitempty"`
	// Source is "rule" for a pattern match and "model" for a classifier
	// judgement. Empty means rule, so every finding written before the
	// classifier existed reads correctly.
	Source Source `json:"source,omitempty"`
}

// FromModel reports whether a model produced this finding.
func (f Finding) FromModel() bool { return f.Source == SourceModel }

// Report is the outcome of scanning one plugin.
type Report struct {
	Plugin   string    `json:"plugin"`
	Findings []Finding `json:"findings"`
	// Scanned counts the files examined, so an empty report can be told apart
	// from a scan that found nothing to read.
	Scanned int `json:"filesScanned"`
	// Classifier names the model pass, when one ran. Empty means the report is
	// entirely deterministic.
	Classifier string `json:"classifier,omitempty"`
	// ClassifierErrors records files the model pass could not judge. Recorded
	// for the same reason as Unread: a classifier that failed on the one file
	// carrying the injection must not be read as having cleared it.
	ClassifierErrors []string `json:"classifierErrors,omitempty"`
	// Unread lists files that belong to a skill and could not be opened.
	//
	// Recorded rather than skipped, because "no findings" and "no findings in
	// the parts I could read" are different claims and only one of them is
	// true. This is the same rule the adapters follow for components they
	// cannot carry: never drop anything in silence. It is also the shape an
	// attacker would reach for — a scanner that quietly ignores one
	// unreadable file is a scanner you disable by making a file unreadable.
	Unread []string `json:"unreadFiles,omitempty"`
}

// Complete reports whether every file belonging to a skill was examined. A
// report that is not complete cannot support "nothing was found".
func (r *Report) Complete() bool { return len(r.Unread) == 0 }

// Count returns the number of findings at a severity.
func (r *Report) Count(s Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == s {
			n++
		}
	}
	return n
}

// Worst returns the highest severity present.
func (r *Report) Worst() Severity {
	worst := Info
	for _, f := range r.Findings {
		if f.Severity.rank() > worst.rank() {
			worst = f.Severity
		}
	}
	return worst
}

// AtLeast returns the findings at or above a severity.
func (r *Report) AtLeast(min Severity) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity.AtLeast(min) {
			out = append(out, f)
		}
	}
	return out
}

// Scan examines a plugin's instruction text and server configuration.
//
// Instruction text is everything a client may load into a model's context:
// SKILL.md and the reference material beside it. Scripts are examined too, but
// under different rules — they are code the agent may run rather than text it
// reads, so the interesting question shifts from "what is this telling the
// model" to "what would this do".
//
// It takes no classifier and cannot be given one. Everything in the CLI calls
// this, so the offline, deterministic scan is what happens unless a caller goes
// out of its way to ask for more — a default expressed in the type signature
// rather than in a flag some later edit could invert.
func Scan(root *safepath.Root, p *ir.Plugin) (*Report, error) {
	return ScanWith(context.Background(), root, p, nil)
}

// ScanWith runs the local rules and, when given a classifier, a model pass over
// the same instruction text.
//
// The model pass is strictly additive. It appends findings; it cannot remove or
// downgrade one the rules produced. That is what makes it safe to feed
// attacker-chosen text to a model and act on the answer: the worst a successful
// injection achieves is that the model says nothing, which loses its opinion
// and gains the attacker no authority over the deterministic findings.
func ScanWith(ctx context.Context, root *safepath.Root, p *ir.Plugin, classifier Classifier) (*Report, error) {
	report := &Report{Plugin: p.Name}
	if classifier != nil {
		report.Classifier = classifier.Describe()
	}

	for _, skill := range p.Skills {
		files, err := skillFiles(root, skill)
		if err != nil {
			return nil, err
		}
		for _, rel := range files {
			abs, err := root.Resolve(rel)
			if err != nil {
				report.Unread = append(report.Unread, rel)
				continue
			}
			raw, err := os.ReadFile(abs)
			if err != nil {
				report.Unread = append(report.Unread, rel)
				continue
			}
			report.Scanned++

			content := string(raw)
			if isScript(rel) {
				report.Findings = append(report.Findings, scanScript(rel, content)...)
				continue
			}
			report.Findings = append(report.Findings, scanInstructions(rel, content)...)

			// Instruction text only. A bundled script is code, and asking a
			// model whether shell is hostile is a different question with a
			// much worse false-positive rate.
			if classifier == nil {
				continue
			}
			found, err := classifier.Classify(ctx, rel, content)
			if err != nil {
				report.ClassifierErrors = append(report.ClassifierErrors,
					fmt.Sprintf("%s: %v", rel, err))
				continue
			}
			report.Findings = append(report.Findings, found...)
		}
	}

	report.Findings = append(report.Findings, scanServers(p)...)

	sortFindings(report.Findings)
	sort.Strings(report.Unread)
	return report, nil
}

// skillFiles lists the files belonging to one skill.
//
// The Agent Skills specification defines scripts/, references/ and assets/
// beside SKILL.md. References are the ones that matter most here: they are
// loaded into context like the skill body, but a reviewer reading a plugin
// tends to open SKILL.md and stop.
func skillFiles(root *safepath.Root, skill ir.Skill) ([]string, error) {
	files := []string{skill.Entrypoint}
	if skill.Dir == "" {
		return files, nil
	}

	for _, sub := range []string{"references", "scripts"} {
		rel := path.Join(skill.Dir, sub)
		abs, err := root.Resolve(rel)
		if err != nil {
			continue
		}
		err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			r, rerr := filepath.Rel(root.Path(), p)
			if rerr != nil {
				return nil
			}
			files = append(files, filepath.ToSlash(r))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func isScript(rel string) bool {
	return strings.Contains(rel, "/scripts/")
}

// sortFindings orders by severity then location, so the most serious thing is
// the first thing read.
func sortFindings(f []Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Severity != f[j].Severity {
			return f[i].Severity.rank() > f[j].Severity.rank()
		}
		if f[i].File != f[j].File {
			return f[i].File < f[j].File
		}
		return f[i].Line < f[j].Line
	})
}
