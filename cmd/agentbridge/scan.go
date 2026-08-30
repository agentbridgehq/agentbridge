package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	adapterreg "github.com/agentbridgehq/agentbridge/internal/adapter/registry"
	"github.com/agentbridgehq/agentbridge/internal/discover"
	"github.com/agentbridgehq/agentbridge/internal/importer/registry"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
	"github.com/agentbridgehq/agentbridge/internal/scanner"
	"github.com/agentbridgehq/agentbridge/internal/secrets"
	"github.com/agentbridgehq/agentbridge/internal/source"
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
	var classify classifierFlags
	registerClassifierFlags(fs, &classify)

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

	// Every plugin in the tree, not just one. Pointing at a plugin and pointing
	// at a repository that contains several is the same request, and a team
	// scanning in CI should not have to enumerate their own directories — a
	// hand-written list is a list that stops covering plugins added later.
	found, err := discover.Plugins(resolved.Dir)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("%s: no plugin found here or in any directory beneath it", positional[0])
	}

	model, err := classify.build(*offline)
	if err != nil {
		return err
	}

	reports := make([]*scanner.Report, 0, len(found))
	for _, p := range found {
		root, err := safepath.NewRoot(p.Dir)
		if err != nil {
			return err
		}
		result, err := registry.Open(p.Dir)
		if err != nil {
			return fmt.Errorf("%s: %w", p.Rel, err)
		}
		report, err := scanner.ScanWith(context.Background(), root, result.Plugin, model)
		if err != nil {
			return err
		}
		report.Findings = report.AtLeast(minimum)
		// Located as the repository sees them, so a dashboard annotates the
		// right file when two plugins both have skills/deploy/SKILL.md.
		reports = append(reports, report.WithPrefix(p.Rel))
	}

	if *sarifPath != "" {
		raw, err := scanner.CombinedSARIF(version, reports...)
		if err != nil {
			return err
		}
		if err := os.WriteFile(*sarifPath, raw, 0o644); err != nil {
			return err
		}
	}

	if *asJSON {
		// A single plugin stays a bare report, so every existing consumer keeps
		// working; a tree becomes a list, which is the only honest shape for
		// more than one.
		if len(reports) == 1 {
			if err := emitJSON(reports[0]); err != nil {
				return err
			}
		} else if err := emitJSON(map[string]any{
			"root": positional[0], "plugins": len(reports), "reports": reports,
		}); err != nil {
			return err
		}
	} else {
		printScanAll(reports, *sarifPath)
	}

	if strings.EqualFold(*failOn, "never") {
		return nil
	}
	threshold, err := scanner.ParseSeverity(*failOn)
	if err != nil {
		return err
	}
	var over int
	for _, r := range reports {
		over += len(r.AtLeast(threshold))
	}
	if over > 0 {
		return fmt.Errorf("%d finding(s) at %s or above", over, threshold)
	}
	return nil
}

// printScanAll renders one or many plugin reports.
//
// A single plugin prints exactly as it always did. Several print a per-plugin
// section and one total, because the number a reviewer acts on is the total
// and the number they investigate with is the section.
func printScanAll(reports []*scanner.Report, sarifPath string) {
	if len(reports) == 1 {
		printScan(reports[0], sarifPath)
		return
	}

	var high, medium, low, info, clean int
	for _, r := range reports {
		if len(r.Findings) == 0 {
			clean++
			continue
		}
		printScan(r, "")
		fmt.Println()
		high += r.Count(scanner.High)
		medium += r.Count(scanner.Medium)
		low += r.Count(scanner.Low)
		info += r.Count(scanner.Info)
	}

	fmt.Printf("%d plugin(s) scanned, %d with nothing to report\n", len(reports), clean)
	fmt.Printf("  %d high, %d medium, %d low, %d note", high, medium, low, info)
	if sarifPath != "" {
		fmt.Printf(" · SARIF written to %s", sarifPath)
	}
	fmt.Println()
}

// classifierFlags are the model-pass options, shared by scan, install and sync.
//
// Flags plus environment rather than a field in agentbridge.yaml, deliberately:
// the manifest is committed and shared by a team, while "which endpoint may see
// our plugin text" is a per-machine, per-policy decision. One developer pointing
// at a model on their laptop and another at a corporate gateway is the normal
// case, and a committed file would make it a merge conflict.
type classifierFlags struct {
	enabled  bool
	endpoint string
	model    string
	canBlock bool
}

// defaultClassifierModel is a default for the *model name* only. There is
// deliberately no default endpoint: the model is a parameter, the destination is
// a decision.
const defaultClassifierModel = "claude-sonnet-5"

func registerClassifierFlags(fs *flag.FlagSet, c *classifierFlags) {
	fs.BoolVar(&c.enabled, "classify", false,
		"also ask a model about the instruction text; sends it to the endpoint you configure")
	fs.StringVar(&c.endpoint, "classifier-endpoint", os.Getenv("AGENTBRIDGE_CLASSIFIER_ENDPOINT"),
		"URL of an Anthropic-compatible API, which may be a model on this machine")
	fs.StringVar(&c.model, "classifier-model", envOr("AGENTBRIDGE_CLASSIFIER_MODEL", defaultClassifierModel),
		"model to ask")
	fs.BoolVar(&c.canBlock, "classify-can-block", false,
		"let a model finding reach high severity and stop an install")
}

// classifierSecretName is where `agentbridge secret set` puts the API key, so
// the key lives in the OS credential store like every other credential this
// tool handles rather than in a config file or a shell profile.
const classifierSecretName = "classifier-key"

// build returns a classifier, or nil when the model pass is off.
//
// Refusing rather than silently skipping is the whole contract here in both
// directions: asking for --classify with no endpoint is an error, and asking
// for it with --offline is an error. A security tool that quietly does less
// than it was told to is the thing this project keeps finding and fixing.
func (c classifierFlags) build(offline bool) (scanner.Classifier, error) {
	if !c.enabled {
		return nil, nil
	}
	if offline {
		return nil, fmt.Errorf("--classify needs the network and --offline forbids it; " +
			"drop one of them rather than letting the scan quietly do less than you asked")
	}

	key, err := classifierKey()
	if err != nil {
		return nil, err
	}
	return scanner.NewAPIClassifier(scanner.Config{
		Endpoint: c.endpoint,
		Model:    c.model,
		APIKey:   key,
		CanBlock: c.canBlock,
	})
}

// classifierKey reads the API key from the credential store, then the
// environment.
//
// The store first, because that is where this tool tells everyone else to put
// credentials and it would be poor form to exempt its own. The environment
// second, because CI has no keychain.
func classifierKey() (string, error) {
	if key, err := secrets.Open().Get(classifierSecretName); err == nil && key != "" {
		return key, nil
	}
	if key := os.Getenv("AGENTBRIDGE_CLASSIFIER_KEY"); key != "" {
		return key, nil
	}
	// Not an error: a model on localhost usually needs no key at all.
	return "", nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// refusal is what a --json consumer receives when the gate stops an install.
//
// It carries the findings themselves rather than a message about them, because
// the thing a CI script wants is the list it must decide about, and re-running
// the scan to obtain it would be running the tool twice to learn one answer.
type refusal struct {
	Plugin   string            `json:"plugin"`
	Refused  bool              `json:"refused"`
	Reason   string            `json:"reason"`
	Findings []scanner.Finding `json:"findings"`
	Remedy   string            `json:"remedy"`
}

// scanGate runs the scanner as part of an install.
//
// The decision to make this blocking rather than advisory: a warning printed
// above a successful install is read after the plugin is already in the config,
// which is the wrong side of the action. So a High finding stops the install and
// says how to proceed anyway — the same shape as --allow-plaintext-secrets, and
// for the same reason. Anything below High prints a single line, because a
// scanner that pages someone over a Medium is one that gets bypassed by habit.
// The `quiet` argument is the --json flag: in that mode nothing may be written
// to stdout except the JSON document, so a refusal is reported by the caller
// instead. `scan` and `validate` both emit their findings as JSON *and* exit
// non-zero, and an install refused on exactly those findings must not be the
// one command that hands a script an empty pipe.
func scanGate(root *safepath.Root, p *ir.Plugin, model scanner.Classifier, allow, quiet bool) error {
	report, err := scanner.ScanWith(context.Background(), root, p, model)
	if err != nil {
		// A scan that cannot run must not block an install: this is an advisory
		// layer, and failing closed on a read error would make it the most
		// fragile part of the tool.
		if !quiet {
			fmt.Fprintf(os.Stderr, "note: content scan did not run: %v\n", err)
		}
		return nil
	}

	// A gap in coverage is worth a line even when the scan is otherwise clean:
	// it is the difference between "this looks fine" and "the parts I could
	// read look fine", and only the reader can judge which matters.
	if !report.Complete() && !quiet {
		fmt.Fprintf(os.Stderr, "note: %d file(s) could not be read, so the content scan is incomplete: %s\n",
			len(report.Unread), strings.Join(report.Unread, ", "))
	}
	// Same rule for the model pass: a classifier that failed on the one file
	// carrying the injection has not cleared it.
	if len(report.ClassifierErrors) > 0 && !quiet {
		fmt.Fprintf(os.Stderr, "note: the model pass failed on %d file(s): %s\n",
			len(report.ClassifierErrors), strings.Join(report.ClassifierErrors, "; "))
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

	if quiet {
		if jsonErr := emitJSON(refusal{
			Plugin:   p.Name,
			Refused:  true,
			Reason:   "high-severity content findings",
			Findings: high,
			Remedy:   "read them with `agentbridge scan <ref>`, then pass --allow-flagged-content to accept",
		}); jsonErr != nil {
			return jsonErr
		}
		return fmt.Errorf("%d high-severity content finding(s) in %s", len(high), p.Name)
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
		printUnread(r)
		printClassifier(r)
		fmt.Printf("  This is a heuristic scan of instruction text, not a proof of safety.\n")
		fmt.Printf("  It cannot tell you whether a plugin does something harmful in a way\n")
		fmt.Printf("  it does not describe.\n")
		return
	}

	marker := map[scanner.Severity]string{
		scanner.High: "HIGH", scanner.Medium: "MED ", scanner.Low: "LOW ", scanner.Info: "note",
	}

	for _, f := range r.Findings {
		// A model finding is a different kind of evidence from a pattern match:
		// it reaches phrasing no rule anticipated, and it is not reproducible.
		// A reader deciding what to do needs to know which they are holding.
		kind := ""
		if f.FromModel() {
			kind = "  [model]"
		}
		fmt.Printf("  %s  %-34s %s%s\n", marker[f.Severity], location(f), f.Title, kind)
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

	fmt.Println()
	printUnread(r)
	printClassifier(r)

	// The limits belong next to the results, not in documentation nobody opens
	// while looking at a finding.
	fmt.Printf("  Findings are evidence, not verdicts: every rule here can be triggered by\n")
	fmt.Printf("  legitimate content. Read the excerpt before concluding anything, and run\n")
	fmt.Printf("  `agentbridge scan --rules` for what each rule means.\n")
}

// printUnread reports files the scan could not open.
//
// "Nothing found" and "nothing found in the parts I could read" are different
// claims, and printing the first while meaning the second is how a scanner
// comes to be trusted for coverage it does not have.
func printUnread(r *scanner.Report) {
	if r.Complete() {
		return
	}
	fmt.Printf("  ! %d file(s) could not be read, so this scan is incomplete:\n", len(r.Unread))
	for _, f := range r.Unread {
		fmt.Printf("      %s\n", f)
	}
	fmt.Println()
}

// printClassifier reports the model pass: that it ran, and anywhere it did not.
//
// A classifier that failed on the one file carrying the injection has not
// cleared it, so its failures are as much a part of the result as its findings.
func printClassifier(r *scanner.Report) {
	if r.Classifier == "" {
		return
	}
	fmt.Printf("  model pass: %s\n", r.Classifier)
	for _, e := range r.ClassifierErrors {
		fmt.Printf("  ! the model could not judge %s\n", e)
	}
	fmt.Println()
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
