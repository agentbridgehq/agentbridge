// Package conformance runs the canonical test corpus against a plugin loader.
//
// The corpus in conformance/cases is the point, not this runner. The
// specification permits a conformant client to support neither component type
// (§11.1), leaves several requirements unexpressible in JSON Schema, and in one
// case — a non-object `extensions` field — makes a client that follows the
// published schema literally *non-conformant*. Whether a given client actually
// behaves as specified is therefore an empirical question that nobody is
// currently answering.
//
// Each case is an ordinary plugin directory plus a statement of what a
// conformant client must do with it. That shape matters: the same corpus can be
// run automatically here and pointed by hand at any client on earth, which is
// what makes it useful to people who have no interest in this tool.
//
// Automated verification here proves our own loader. Third-party clients are
// checked by a human following each case's `observe` note and recording what
// they saw — see conformance/README.md. Nothing in this package pretends
// otherwise: a result nobody observed is reported as unmeasured, never as a
// pass.
package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/agentbridgehq/agentbridge/internal/importer/registry"
)

// Case is one conformance test.
type Case struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
	// Section cites the specification clause the case tests.
	Section string `yaml:"section" json:"section"`
	// Requirement is the conformance keyword: MUST, SHOULD, MAY.
	Requirement string `yaml:"requirement" json:"requirement"`
	Expect      Expect `yaml:"expect" json:"expect"`
	// Observe tells a human what to look for in a client that this runner
	// cannot inspect.
	Observe string `yaml:"observe" json:"observe"`

	// Dir is the case directory, filled in on load.
	Dir string `yaml:"-" json:"-"`
}

// Expect is what a conformant client must do with the case.
type Expect struct {
	// Loads is whether the plugin should load at all. A false value is the
	// strongest assertion in the corpus: the plugin must be rejected and none
	// of its components made available.
	Loads bool `yaml:"loads" json:"loads"`
	// Skills and MCPServers are the component counts a client should end up
	// with. Negative means unspecified.
	Skills     int `yaml:"skills" json:"skills"`
	MCPServers int `yaml:"mcpServers" json:"mcpServers"`
	// Diagnostics are reason codes that must appear. These are ours, so they
	// only constrain this implementation; a human checking another client uses
	// the Observe note instead.
	Diagnostics []string `yaml:"diagnostics" json:"diagnostics,omitempty"`

	hasSkills bool
	hasMCP    bool
}

// UnmarshalYAML records which counts were actually specified, so an omitted
// count is not silently read as zero.
func (e *Expect) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Loads       bool     `yaml:"loads"`
		Skills      *int     `yaml:"skills"`
		MCPServers  *int     `yaml:"mcpServers"`
		Diagnostics []string `yaml:"diagnostics"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	e.Loads = raw.Loads
	e.Diagnostics = raw.Diagnostics
	if raw.Skills != nil {
		e.Skills, e.hasSkills = *raw.Skills, true
	}
	if raw.MCPServers != nil {
		e.MCPServers, e.hasMCP = *raw.MCPServers, true
	}
	return nil
}

// Outcome grades a case.
type Outcome string

const (
	// Pass means the loader did what the specification requires.
	Pass Outcome = "pass"
	// Fail means it did not.
	Fail Outcome = "fail"
	// Unmeasured means nobody has run this case against the target. It is
	// never inferred from anything; a result that was not observed is not a
	// result.
	Unmeasured Outcome = "unmeasured"
)

// Result is one case run against one target.
type Result struct {
	Case    Case    `json:"case"`
	Outcome Outcome `json:"outcome"`
	// Detail explains a failure, or what was observed.
	Detail string `json:"detail,omitempty"`
}

// Report is a full run.
type Report struct {
	// Target names what was tested.
	Target  string   `json:"target"`
	Results []Result `json:"results"`
}

// Count returns how many results have an outcome.
func (r *Report) Count(o Outcome) int {
	n := 0
	for _, res := range r.Results {
		if res.Outcome == o {
			n++
		}
	}
	return n
}

// Conformant reports whether nothing failed and nothing is unmeasured.
func (r *Report) Conformant() bool {
	return r.Count(Fail) == 0 && r.Count(Unmeasured) == 0
}

// LoadCases reads the corpus.
func LoadCases(dir string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading the conformance corpus at %s: %w", dir, err)
	}

	var cases []Case
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "case.yaml")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var c Case
		if err := yaml.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		c.Dir = filepath.Join(dir, e.Name())
		cases = append(cases, c)
	}

	sort.Slice(cases, func(i, j int) bool { return cases[i].Dir < cases[j].Dir })
	return cases, nil
}

// RunSelf runs the corpus against this implementation's loader.
func RunSelf(dir string) (*Report, error) {
	cases, err := LoadCases(dir)
	if err != nil {
		return nil, err
	}

	report := &Report{Target: "agentbridge"}
	for _, c := range cases {
		report.Results = append(report.Results, runOne(c))
	}
	return report, nil
}

func runOne(c Case) Result {
	pluginDir := filepath.Join(c.Dir, "plugin")

	result, err := registry.Open(pluginDir)
	if !c.Expect.Loads {
		if err == nil {
			return Result{Case: c, Outcome: Fail,
				Detail: "the plugin loaded, but the specification requires it to be rejected"}
		}
		return Result{Case: c, Outcome: Pass, Detail: "rejected: " + firstLine(err.Error())}
	}
	if err != nil {
		return Result{Case: c, Outcome: Fail, Detail: "the plugin was rejected: " + firstLine(err.Error())}
	}

	var problems []string

	if c.Expect.hasSkills && len(result.Plugin.Skills) != c.Expect.Skills {
		problems = append(problems, fmt.Sprintf("skills: got %d, want %d",
			len(result.Plugin.Skills), c.Expect.Skills))
	}
	if c.Expect.hasMCP && len(result.Plugin.MCPServers) != c.Expect.MCPServers {
		problems = append(problems, fmt.Sprintf("mcp servers: got %d, want %d",
			len(result.Plugin.MCPServers), c.Expect.MCPServers))
	}

	present := map[string]bool{}
	for _, d := range result.Diagnostics {
		present[d.Code] = true
	}
	for _, want := range c.Expect.Diagnostics {
		if !present[want] {
			problems = append(problems, fmt.Sprintf("expected diagnostic %s, got %v",
				want, result.Diagnostics.Codes()))
		}
	}

	if len(problems) > 0 {
		return Result{Case: c, Outcome: Fail, Detail: strings.Join(problems, "; ")}
	}
	return Result{Case: c, Outcome: Pass}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Blank builds a report of unmeasured results, for a client nobody has tested
// yet.
//
// This exists so an untested client is represented explicitly rather than
// omitted. A blank row in a compatibility matrix invites the reader to assume
// something; a row that says "nobody has checked" does not.
func Blank(dir, target string) (*Report, error) {
	cases, err := LoadCases(dir)
	if err != nil {
		return nil, err
	}
	report := &Report{Target: target}
	for _, c := range cases {
		report.Results = append(report.Results, Result{Case: c, Outcome: Unmeasured})
	}
	return report, nil
}
