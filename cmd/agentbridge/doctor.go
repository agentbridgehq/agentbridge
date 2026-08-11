package main

import (
	"flag"
	"fmt"

	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	"github.com/agentbridge/agentbridge/internal/doctor"
	"github.com/agentbridge/agentbridge/internal/secrets"
)

// doctorCmd answers "I installed it — why is nothing happening?".
//
// The specification permits a conformant client to support neither skills nor
// MCP servers, component locations are fixed so a plugin either lands or
// silently does not, and every client spells its configuration differently. The
// question is therefore inevitable, and nothing else in the ecosystem answers
// it. Every check here reports the next action rather than a status.
func doctorCmd(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	client := fs.String("client", "", "restrict to one client id")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return fmt.Errorf("doctor takes at most one plugin name")
	}
	plugin := ""
	if len(positional) == 1 {
		plugin = positional[0]
	}

	env, err := currentEnv()
	if err != nil {
		return err
	}
	store, err := receipt.Open(adapterreg.StateDir(env))
	if err != nil {
		return err
	}

	opts := doctor.Options{Env: env, Plugin: plugin, Client: *client}
	report := doctor.Run(store, opts)
	doctor.CheckSecretReferences(report, store, secrets.Open(), opts)

	if *asJSON {
		return emitJSON(report)
	}
	printDoctor(report)

	if !report.Healthy() {
		return fmt.Errorf("%d problem(s) found", report.Count(doctor.Fail))
	}
	return nil
}

func printDoctor(r *doctor.Report) {
	if len(r.Checks) == 0 {
		fmt.Println("Nothing to report.")
		return
	}

	marker := map[doctor.Status]string{
		doctor.Fail: "xx", doctor.Warn: "!!", doctor.OK: "ok", doctor.Info: "--",
	}
	// Failures first: someone running this has a problem and should not have to
	// scroll past the things that are fine.
	for _, status := range []doctor.Status{doctor.Fail, doctor.Warn, doctor.Info, doctor.OK} {
		for _, c := range r.Checks {
			if c.Status != status {
				continue
			}
			fmt.Printf("  %s %-28s %s\n", marker[status], c.Subject, c.Title)
			if c.Detail != "" {
				fmt.Printf("       %s\n", c.Detail)
			}
			if c.Fix != "" {
				fmt.Printf("       → %s\n", c.Fix)
			}
		}
	}

	fmt.Printf("\n  %d problem(s), %d warning(s), %d note(s)\n",
		r.Count(doctor.Fail), r.Count(doctor.Warn), r.Count(doctor.Info))
	if r.Healthy() {
		fmt.Println("  Nothing is broken that agentbridge can see.")
	}
}
