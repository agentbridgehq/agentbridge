package conformance

import (
	"encoding/json"
	"path/filepath"
)

// Index renders the corpus as a machine-readable manifest.
//
// The corpus is only worth what its reach is, and requiring the agentbridge
// binary to consume it would shrink that to people who already use this tool —
// which is the least interesting audience for a compatibility suite. A JSON
// manifest lets anyone write a runner in any language against plain plugin
// directories, with no dependency on us at all.
//
// That is also the form in which it can be offered upstream: the Agent Plugins
// technical charter lists "managing reference implementations and test suites"
// among the TSC's responsibilities, and a suite that only one vendor's tool can
// run is not one a standards body can adopt.
func Index(dir string) ([]byte, error) {
	cases, err := LoadCases(dir)
	if err != nil {
		return nil, err
	}

	type indexCase struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Section     string   `json:"section"`
		Requirement string   `json:"requirement"`
		Plugin      string   `json:"plugin"`
		Expect      expectJS `json:"expect"`
		Observe     string   `json:"observe"`
	}

	out := struct {
		Spec        string      `json:"spec"`
		Description string      `json:"description"`
		License     string      `json:"license"`
		Cases       []indexCase `json:"cases"`
	}{
		Spec: "1.0.0",
		Description: "Conformance cases for Agent Plugins 1.0.0. Each `plugin` path is an " +
			"ordinary plugin package; `expect` states what a conformant client must do with it, " +
			"and `observe` says what to look for in a client that cannot be driven programmatically.",
		License: "Apache-2.0",
	}

	for _, c := range cases {
		out.Cases = append(out.Cases, indexCase{
			ID:          c.ID,
			Title:       c.Title,
			Section:     c.Section,
			Requirement: c.Requirement,
			Plugin:      filepath.ToSlash(filepath.Join("cases", filepath.Base(c.Dir), "plugin")),
			Expect: expectJS{
				Loads:      c.Expect.Loads,
				Skills:     optional(c.Expect.hasSkills, c.Expect.Skills),
				MCPServers: optional(c.Expect.hasMCP, c.Expect.MCPServers),
			},
			Observe: c.Observe,
		})
	}

	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// expectJS omits counts a case did not specify, rather than publishing a zero
// that a runner would read as "expect none".
type expectJS struct {
	Loads      bool `json:"loads"`
	Skills     *int `json:"skills,omitempty"`
	MCPServers *int `json:"mcpServers,omitempty"`
}

func optional(present bool, v int) *int {
	if !present {
		return nil
	}
	return &v
}
