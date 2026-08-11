package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/agentbridge/agentbridge/internal/adapter"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
)

// lossesCmd prints what each client might not carry, before anything is
// installed.
//
// The fidelity report answers this after the fact for one plugin. This answers
// it in advance for every client, which is the question a plugin author has
// ("where will this not work?") and the question a platform lead has ("what are
// we giving up by standardising on X?").
func lossesCmd(args []string) error {
	fs := flag.NewFlagSet("losses", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	client := fs.String("client", "", "restrict to one client id")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	env, err := currentEnv()
	if err != nil {
		return err
	}

	type clientLosses struct {
		Client string             `json:"client"`
		Losses []adapter.LossInfo `json:"losses"`
	}
	var byClient []clientLosses

	for _, a := range adapterreg.Adapters(env) {
		c := a.Client()
		if *client != "" && c.ID != *client {
			continue
		}
		entry := clientLosses{Client: c.ID}
		for _, code := range c.Losses {
			if info, ok := adapter.LookupLoss(code); ok {
				entry.Losses = append(entry.Losses, info)
			}
		}
		byClient = append(byClient, entry)
	}

	if *asJSON {
		return emitJSON(map[string]any{
			"catalog": adapter.LossCatalog(),
			"clients": byClient,
		})
	}

	for _, entry := range byClient {
		fmt.Printf("\n%s\n", entry.Client)
		for _, info := range entry.Losses {
			kind := "fault "
			if info.Expected {
				kind = "by design"
			}
			fmt.Printf("  %-9s %-38s %s\n", kind, info.Code, info.Title)
		}
	}

	fmt.Printf("\n%d loss code(s) in the catalogue. `by design` means the client genuinely\n", len(adapter.LossCatalog()))
	fmt.Printf("cannot do it; `fault` means something went wrong and can be fixed.\n")
	fmt.Printf("Run `agentbridge losses --json` for the full explanations and remedies.\n")
	return nil
}

// describeLoss renders one loss for the install report, leading with whether it
// is a fault or a fact.
func describeLoss(l adapter.Loss) string {
	var b strings.Builder
	if info, ok := adapter.LookupLoss(l.Code); ok && !info.Expected {
		b.WriteString("! ")
	} else {
		b.WriteString("- ")
	}
	b.WriteString(l.Reason)
	if l.Component != "" {
		b.WriteString(" (" + l.Component + ")")
	}
	return b.String()
}
