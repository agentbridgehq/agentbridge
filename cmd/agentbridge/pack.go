package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/pack"
	"github.com/agentbridgehq/agentbridge/internal/validate"
)

// packCmd writes the vendor manifests a package needs to install natively.
//
// This is the only command aimed at the person writing a plugin rather than the
// person installing one, and that changes what "correct" means. Its output is
// committed, so it must be idempotent and readable in a diff; it runs in the
// author's CI, so it must have a mode that reports without writing; and the
// package it produces has to work on a machine where agentbridge is not
// installed, because the point is to make a conformant package load in clients
// that do not read the specification's manifest.
func packCmd(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	dryRun := fs.Bool("dry-run", false, "show what would be written, write nothing")
	check := fs.Bool("check", false, "write nothing; exit non-zero if anything is missing or stale")
	clientList := fs.String("client", "", "restrict to these client ids, comma separated")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("pack takes exactly one plugin directory")
	}
	dir := positional[0]

	// Refusing to pack an invalid package is the whole discipline of this
	// command. The manifests below are derived from the specification manifest,
	// so packing a broken package would carry the breakage into three more
	// files and make it look sanctioned. Validation first also means the author
	// hears about a problem while they can still fix it in one place.
	report, err := validate.Run(dir)
	if err != nil {
		return err
	}
	if report.Count(validate.Violation) > 0 {
		printReport(report)
		return fmt.Errorf("refusing to pack: this is not a conformant Agent Plugins package yet")
	}

	result, err := open(dir, "")
	if err != nil {
		return err
	}

	var clients []string
	if strings.TrimSpace(*clientList) != "" {
		clients = strings.Split(*clientList, ",")
	}

	files, gaps, err := pack.Build(result.Plugin, clients)
	if err != nil {
		return err
	}
	changes, err := pack.Plan(dir, files)
	if err != nil {
		return err
	}

	out := pack.Result{Plugin: result.Plugin.Name, Changes: changes, Gaps: gaps}

	if !*dryRun && !*check {
		if err := pack.Apply(dir, changes); err != nil {
			return err
		}
	}

	if *asJSON {
		if err := emitJSON(out); err != nil {
			return err
		}
	} else {
		printPack(out, *dryRun, *check)
	}

	// --check is for CI, where the useful signal is "somebody edited the
	// manifest and did not re-run pack", and the useful behaviour is a failed
	// job rather than a diff nobody reads.
	if *check && out.Written() {
		return fmt.Errorf("vendor manifests are out of date; run `agentbridge pack %s`", dir)
	}
	return nil
}

func printPack(r pack.Result, dryRun, check bool) {
	verb := "wrote"
	switch {
	case check:
		verb = "checked"
	case dryRun:
		verb = "would write"
	}

	fmt.Printf("%s — vendor manifests\n\n", r.Plugin)

	byClient := map[string][]pack.Change{}
	var order []string
	for _, c := range r.Changes {
		if _, seen := byClient[c.Client]; !seen {
			order = append(order, c.Client)
		}
		byClient[c.Client] = append(byClient[c.Client], c)
	}

	for _, client := range order {
		fmt.Printf("  %s\n", client)
		for _, c := range byClient[client] {
			mark := "="
			if c.State != pack.Unchanged {
				mark = "+"
			}
			fmt.Printf("    %s %-32s %-9s %s\n", mark, c.Path, c.State, c.Why)
			// Naming what survived is the point of merging rather than
			// replacing. An author whose logo is still there should be able to
			// see that without diffing the file.
			if len(c.Kept) > 0 {
				fmt.Printf("    %s %-32s %-9s kept: %s\n", " ", "", "", strings.Join(c.Kept, ", "))
			}
		}
		fmt.Println()
	}

	if len(r.Gaps) > 0 {
		fmt.Println("  not settled by packing:")
		for _, g := range r.Gaps {
			fmt.Printf("    %s: %s\n", g.Client, g.Detail)
		}
		fmt.Println()
	}

	changed := 0
	for _, c := range r.Changes {
		if c.State != pack.Unchanged {
			changed++
		}
	}
	if changed == 0 {
		fmt.Printf("  all %d file(s) already up to date.\n", len(r.Changes))
		return
	}
	if check {
		fmt.Printf("  %d of %d file(s) are missing or stale; run `agentbridge pack` to refresh them.\n", changed, len(r.Changes))
		return
	}
	fmt.Printf("  %s %d of %d file(s). Commit them: they are what makes this package\n", verb, changed, len(r.Changes))
	fmt.Printf("  install on a machine that does not have agentbridge.\n")
}
