package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	"github.com/agentbridge/agentbridge/internal/importer/registry"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
	"github.com/agentbridge/agentbridge/internal/scanner"
	"github.com/agentbridge/agentbridge/internal/source"
)

// scanCmd inspects a plugin's instruction text.
//
// The thing this looks for exists in no other scanner: a SKILL.md is
// natural-language text loaded into a model's context that directs an agent
// holding tools. SCA reads dependency manifests, SAST reads source, EDR watches
// processes, an MCP gateway sees tool calls — a sentence telling an agent to
// read ~/.aws/credentials before answering database questions passes all of
// them.
func scanCmd(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	sarifPath := fs.String("sarif", "", "write SARIF 2.1.0 to this path, for code scanning dashboards")
	minSeverity := fs.String("min-severity", "info", "report findings at this severity or above: high, medium, low, info")
	failOn := fs.String("fail-on", "high", "exit non-zero when a finding reaches this severity, or `never`")
	rulesFlag := fs.Bool("rules", false, "print the rule catalogue and exit")
	offline := fs.Bool("offline", false, "never access the network; only pinned, cached references resolve")

	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if *rulesFlag {
		return printRules(*asJSON)
	}
	if len(positional) != 1 {
		return fmt.Errorf("scan takes exactly one plugin reference")
	}

	minimum, err := scanner.ParseSeverity(*minSeverity)
	if err != nil {
		return err
	}

	// A reference rather than a directory, because the question this answers —
	// "what is in the thing I am about to install?" — is asked before the thing
	// is on disk.
	env, err := currentEnv()
	if err != nil {
		return err
	}
	resolved, err := source.ResolveString(context.Background(), positional[0], source.Options{
		Cache:   source.NewCache(adapterreg.CacheDir(env)),
		Offline: *offline,
	})
	if err != nil {
		return err
	}

	result, err := registry.Open(resolved.Dir)
	if err != nil {
		return err
	}
	root, err := safepath.NewRoot(resolved.Dir)
	if err != nil {
		return err
	}

	report, err := scanner.Scan(root, result.Plugin)
	if err != nil {
		return err
	}
	report.Findings = report.AtLeast(minimum)

	if *sarifPath != "" {
		raw, err := report.SARIF(version)
		if err != nil {
			return err
		}
		if err := os.WriteFile(*sarifPath, raw, 0o644); err != nil {
			return err
		}
	}

	if *asJSON {
		if err := emitJSON(report); err != nil {
			return err
		}
	} else {
		printScan(report, *sarifPath)
	}

	if strings.EqualFold(*failOn, "never") {
		return nil
	}
	threshold, err := scanner.ParseSeverity(*failOn)
	if err != nil {
		return err
	}
	if n := len(report.AtLeast(threshold)); n > 0 {
		return fmt.Errorf("%d finding(s) at %s or above", n, threshold)
	}
	return nil
}

// scanGate runs the scanner as part of an install.
//
// The decision to make this blocking rather than advisory: a warning printed
// above a successful install is read after the plugin is already in the config,
// which is the wrong side of the action. So a High finding stops the install and
// says how to proceed anyway — the same shape as --allow-plaintext-secrets, and
// for the same reason. Anything below High prints a single line, because a
// scanner that pages someone over a Medium is one that gets bypassed by habit.
func scanGate(root *safepath.Root, p *ir.Plugin, allow, quiet bool) error {
	report, err := scanner.Scan(root, p)
	if err != nil {
		// A scan that cannot run must not block an install: this is an advisory
		// layer, and failing closed on a read error would make it the most
		// fragile part of the tool.
		if !quiet {
			fmt.Fprintf(os.Stderr, "note: content scan did not run: %v\n", err)
		}
		return nil
	}

	high := report.AtLeast(scanner.High)
	if len(high) == 0 {
		if n := len(report.AtLeast(scanner.Low)); n > 0 && !quiet {
			fmt.Printf("  %d content finding(s) below the blocking threshold — `agentbridge scan` for detail\n\n", n)
		}
		return nil
	}

	if allow {
		if !quiet {
			fmt.Printf("  %d high-severity content finding(s), installed anyway (--allow-flagged-content)\n\n", len(high))
		}
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d high-severity content finding(s) in %s:\n", len(high), p.Name)
	for _, f := range high {
		fmt.Fprintf(&b, "\n  %s  %s\n    %s\n", location(f), f.Title, f.Message)
		if f.Excerpt != "" {
			fmt.Fprintf(&b, "    > %s\n", f.Excerpt)
		}
	}
	fmt.Fprintf(&b, "\nThis text would be loaded into an agent's context. Read it with\n")
	fmt.Fprintf(&b, "`agentbridge scan <ref>`, then install with --allow-flagged-content if it is fine.")
	return errors.New(b.String())
}

func printScan(r *scanner.Report, sarifPath string) {
	fmt.Printf("%s\n\n", r.Plugin)

	if len(r.Findings) == 0 {
		fmt.Printf("  nothing found in %d file(s)\n\n", r.Scanned)
		fmt.Printf("  This is a heuristic scan of instruction text, not a proof of safety.\n")
		fmt.Printf("  It cannot tell you whether a plugin does something harmful in a way\n")
		fmt.Printf("  it does not describe.\n")
		return
	}

	marker := map[scanner.Severity]string{
		scanner.High: "HIGH", scanner.Medium: "MED ", scanner.Low: "LOW ", scanner.Info: "note",
	}

	for _, f := range r.Findings {
		fmt.Printf("  %s  %-34s %s\n", marker[f.Severity], location(f), f.Title)
		fmt.Printf("        %s\n", f.Message)
		if f.Excerpt != "" {
			fmt.Printf("        > %s\n", f.Excerpt)
		}
		if rule, ok := scanner.Lookup(f.RuleID); ok && f.Severity.AtLeast(scanner.Medium) {
			fmt.Printf("        → %s\n", rule.Remedy)
		}
		fmt.Println()
	}

	fmt.Printf("  %d high, %d medium, %d low, %d note",
		r.Count(scanner.High), r.Count(scanner.Medium), r.Count(scanner.Low), r.Count(scanner.Info))
	if sarifPath != "" {
		fmt.Printf(" · SARIF written to %s", sarifPath)
	}
	fmt.Println()

	// The limits belong next to the results, not in documentation nobody opens
	// while looking at a finding.
	fmt.Printf("\n  Findings are evidence, not verdicts: every rule here can be triggered by\n")
	fmt.Printf("  legitimate content. Read the excerpt before concluding anything, and run\n")
	fmt.Printf("  `agentbridge scan --rules` for what each rule means.\n")
}

// location renders where a finding is, omitting a line number for findings
// about a file as a whole rather than printing a misleading `:0`.
func location(f scanner.Finding) string {
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	return f.File
}

func printRules(asJSON bool) error {
	rules := scanner.Catalog()
	if asJSON {
		return emitJSON(rules)
	}

	for _, r := range rules {
		fmt.Printf("%-32s %s\n", r.ID, strings.ToUpper(string(r.Severity)))
		fmt.Printf("  %s\n", r.Title)
		fmt.Printf("  %s\n", wrap(r.Rationale, 76, "  "))
		fmt.Printf("  → %s\n\n", wrap(r.Remedy, 76, "    "))
	}
	return nil
}

// wrap folds text to a width, so a rationale reads as prose in a terminal
// rather than as one long line.
func wrap(s string, width int, indent string) string {
	var out strings.Builder
	col := 0
	for i, word := range strings.Fields(s) {
		if col > 0 && col+len(word)+1 > width {
			out.WriteString("\n" + indent)
			col = len(indent)
		} else if i > 0 {
			out.WriteString(" ")
			col++
		}
		out.WriteString(word)
		col += len(word)
	}
	return out.String()
}
