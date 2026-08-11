package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/agentbridge/agentbridge/internal/conformance"
)

// conformanceCmd runs the canonical corpus.
//
// Shipped in the binary rather than kept as an internal test because the corpus
// is only worth what its reach is. A client vendor should be able to check their
// own implementation against the same cases we check ours against, without
// adopting anything else here — that is what makes a compatibility matrix
// something the ecosystem cites rather than something one vendor asserts.
func conformanceCmd(args []string) error {
	fs := flag.NewFlagSet("conformance", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	list := fs.Bool("list", false, "print the corpus as a manual checklist")
	record := fs.String("record", "", "print a results template for a client you are testing by hand")
	dir := fs.String("corpus", "conformance/cases", "path to the case corpus")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	if *record != "" {
		out, err := conformance.RecordTemplate(*dir, *record)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}
	if *list {
		return listCases(*dir, *asJSON)
	}

	report, err := conformance.RunSelf(*dir)
	if err != nil {
		return err
	}

	if *asJSON {
		return emitJSON(report)
	}
	printConformance(report)

	if report.Count(conformance.Fail) > 0 {
		return fmt.Errorf("%d case(s) failed", report.Count(conformance.Fail))
	}
	return nil
}

// listCases prints the corpus for a human testing a client this runner cannot
// drive.
func listCases(dir string, asJSON bool) error {
	cases, err := conformance.LoadCases(dir)
	if err != nil {
		return err
	}
	if asJSON {
		return emitJSON(cases)
	}

	fmt.Printf("%d conformance cases.\n", len(cases))
	fmt.Printf("Point a client at each plugin directory and record what you observe.\n")
	fmt.Printf("See conformance/README.md for how to contribute results.\n\n")

	for _, c := range cases {
		fmt.Printf("── %s  §%s %s\n", c.ID, c.Section, c.Requirement)
		fmt.Printf("   %s\n", c.Title)
		fmt.Printf("   plugin: %s/plugin\n", c.Dir)
		for _, line := range strings.Split(strings.TrimSpace(c.Observe), "\n") {
			fmt.Printf("   > %s\n", line)
		}
		fmt.Println()
	}
	return nil
}

func printConformance(r *conformance.Report) {
	marker := map[conformance.Outcome]string{
		conformance.Pass: "ok", conformance.Fail: "xx", conformance.Unmeasured: "??",
	}

	fmt.Printf("Agent Plugins 1.0.0 conformance — %s\n\n", r.Target)
	for _, res := range r.Results {
		fmt.Printf("  %s §%-7s %-42s %s\n",
			marker[res.Outcome], res.Case.Section, res.Case.ID, res.Case.Title)
		if res.Outcome == conformance.Fail {
			fmt.Printf("       %s\n", res.Detail)
		}
	}

	fmt.Printf("\n  %d passed, %d failed, %d unmeasured\n",
		r.Count(conformance.Pass), r.Count(conformance.Fail), r.Count(conformance.Unmeasured))

	if r.Conformant() {
		fmt.Printf("  Every case in the corpus behaves as the specification requires.\n")
	}
	fmt.Fprintf(os.Stderr, "\nThis measures one loader. For another client, run with --list and\n")
	fmt.Fprintf(os.Stderr, "check each case by hand: no automated result is inferred for software\n")
	fmt.Fprintf(os.Stderr, "this runner cannot drive.\n")
}
