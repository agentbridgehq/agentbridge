package ir_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/ir"
)

// Map iteration order in Go is randomized, so a digest built by naive
// marshalling would differ between runs. If this ever fails, lockfiles stop
// being reproducible.
func TestCanonicalIsOrderIndependent(t *testing.T) {
	a := map[string]any{"z": 1, "a": 2, "m": map[string]any{"y": 3, "b": 4}}
	b := map[string]any{"m": map[string]any{"b": 4, "y": 3}, "a": 2, "z": 1}

	ca, err := ir.Canonical(a)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := ir.Canonical(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ca) != string(cb) {
		t.Errorf("canonical forms differ:\n  %s\n  %s", ca, cb)
	}
}

// Preserved extension blobs arrive as raw bytes whose whitespace and key order
// reflect however the author wrote the file. Two manifests that differ only in
// formatting describe the same plugin and must hash the same.
func TestCanonicalNormalizesRawMessageFormatting(t *testing.T) {
	compact := json.RawMessage(`{"b":1,"a":2}`)
	spaced := json.RawMessage("{\n  \"a\" : 2,\n  \"b\":  1\n}")

	ca, err := ir.Canonical(map[string]json.RawMessage{"ns": compact})
	if err != nil {
		t.Fatal(err)
	}
	cb, err := ir.Canonical(map[string]json.RawMessage{"ns": spaced})
	if err != nil {
		t.Fatal(err)
	}
	if string(ca) != string(cb) {
		t.Errorf("formatting leaked into the canonical form:\n  %s\n  %s", ca, cb)
	}
}

// Go's encoder escapes <, > and & by default. A manifest URL containing them
// would otherwise hash differently depending on how many times it had been
// through the encoder.
func TestCanonicalDoesNotEscapeHTML(t *testing.T) {
	out, err := ir.Canonical(map[string]string{"url": "https://x.example/?a=1&b=2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `\u0026`) {
		t.Errorf("HTML escaping applied: %s", out)
	}
}

func TestDigestChangesWithContent(t *testing.T) {
	base := &ir.Plugin{IRVersion: ir.Version, Name: "p", Version: "1.0.0"}
	d0, err := base.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d0, ir.DigestPrefix) {
		t.Errorf("digest = %q, want %s prefix", d0, ir.DigestPrefix)
	}

	for name, mutate := range map[string]func(*ir.Plugin){
		"version":     func(p *ir.Plugin) { p.Version = "1.0.1" },
		"skill":       func(p *ir.Plugin) { p.Skills = []ir.Skill{{Name: "s"}} },
		"server":      func(p *ir.Plugin) { p.MCPServers = []ir.MCPServer{{Name: "m"}} },
		"capability":  func(p *ir.Plugin) { p.Capabilities.Exec = true },
		"extension":   func(p *ir.Plugin) { p.Extensions = map[string]json.RawMessage{"a.b": json.RawMessage(`{}`)} },
		"dialect":     func(p *ir.Plugin) { p.Origin.Dialect = ir.DialectClaudeCode },
		"description": func(p *ir.Plugin) { p.Description = "x" },
	} {
		t.Run(name, func(t *testing.T) {
			p := *base
			mutate(&p)
			d, err := p.Digest()
			if err != nil {
				t.Fatal(err)
			}
			if d == d0 {
				t.Errorf("digest unchanged after modifying %s", name)
			}
		})
	}
}

// Two developers with the same plugin in different directories must compute
// the same digest, or the lockfile cannot be shared.
func TestDigestIgnoresRoot(t *testing.T) {
	a := &ir.Plugin{IRVersion: ir.Version, Name: "p", Origin: ir.Origin{Root: "/home/a/plugins/p"}}
	b := &ir.Plugin{IRVersion: ir.Version, Name: "p", Origin: ir.Origin{Root: `C:\Users\b\p`}}

	da, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	db, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Errorf("digest depends on install path:\n  %s\n  %s", da, db)
	}
}

func TestServerContentHash(t *testing.T) {
	a := ir.MCPServer{Name: "db", Transport: ir.TransportStdio, Command: "npx", Args: []string{"x"}}
	b := a

	ha, err := a.ComputeContentHash()
	if err != nil {
		t.Fatal(err)
	}
	hb, err := b.ComputeContentHash()
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Errorf("identical servers hashed differently:\n  %s\n  %s", ha, hb)
	}

	b.Args = []string{"y"}
	hc, err := b.ComputeContentHash()
	if err != nil {
		t.Fatal(err)
	}
	if hc == ha {
		t.Error("hash unchanged after args changed")
	}
}

func TestCapabilityEvidence(t *testing.T) {
	var c ir.Capabilities
	c.Set(ir.CapExec, "db", "stdio server")

	if !c.Has(ir.CapExec) || c.Has(ir.CapNetwork) {
		t.Errorf("Has = %+v", c.List())
	}
	if len(c.Evidence) != 1 || c.Evidence[0].Component != "db" {
		t.Errorf("evidence = %+v", c.Evidence)
	}

	// List order must be stable regardless of the order capabilities were set.
	c.Set(ir.CapNetwork, "api", "http server")
	got := c.List()
	if len(got) != 2 || got[0] != ir.CapExec || got[1] != ir.CapNetwork {
		t.Errorf("List = %v, want [exec network]", got)
	}
}
