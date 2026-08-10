package conformance

import (
	"fmt"
	"strings"
)

// RecordTemplate renders a results file with every case unmeasured, ready for a
// human to fill in.
//
// Measuring a client is already tedious — install a plugin, restart, look, note
// it down, repeat eighteen times. Asking someone to also hand-author YAML with
// the right case ids is how a corpus goes unmeasured. The template turns the job
// into editing one word per case.
//
// Every outcome starts as `unmeasured` rather than `pass`, and that default is
// deliberate: a half-finished run left as-is reports honestly instead of
// claiming eighteen passes nobody observed.
func RecordTemplate(dir, target string) (string, error) {
	cases, err := LoadCases(dir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# A conformance run against one client.\n")
	b.WriteString("#\n")
	b.WriteString("# Fill in `outcome` for each case: pass | fail | unmeasured.\n")
	b.WriteString("# Leave anything you did not personally observe as `unmeasured` — an\n")
	b.WriteString("# unmeasured case is a useful, honest datum; a guessed one poisons the table.\n")
	b.WriteString("#\n")
	b.WriteString("# A failure is a bug report, not a verdict. The likeliest explanations, in\n")
	b.WriteString("# order: the corpus is wrong, the installation was wrong, the client is wrong.\n")
	b.WriteString("#\n")
	b.WriteString("# See conformance/PROTOCOL.md for how to install a case into each client.\n\n")

	fmt.Fprintf(&b, "target: %s\n", target)
	b.WriteString("version: \"\"          # the client version you tested — a result without one cannot be retired\n")
	b.WriteString("platform: \"\"         # e.g. macOS 15 / arm64\n")
	b.WriteString("tested_by: \"\"\n")
	b.WriteString("tested_on: \"\"        # YYYY-MM-DD\n")
	b.WriteString("method: |\n")
	b.WriteString("  # How you installed each plugin, so someone else can reproduce it.\n\n")
	b.WriteString("results:\n")

	for _, c := range cases {
		fmt.Fprintf(&b, "  # §%s %s — %s\n", c.Section, c.Requirement, c.Title)
		for _, line := range strings.Split(strings.TrimSpace(c.Observe), "\n") {
			fmt.Fprintf(&b, "  #   %s\n", line)
		}
		fmt.Fprintf(&b, "  - id: %s\n", c.ID)
		b.WriteString("    outcome: unmeasured\n")
		b.WriteString("    notes: \"\"\n\n")
	}

	return b.String(), nil
}
