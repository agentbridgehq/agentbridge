package main

import (
	"os"
	"testing"

	"github.com/agentbridge/agentbridge/internal/conformance"
)

// docs/clients.md is generated. Compatibility documentation written by hand
// goes stale within a release and then actively misleads, which for this page
// would be worse than having none — the product claim is that we tell people
// the truth about what each client takes.
func TestClientDocsAreCurrent(t *testing.T) {
	want := Render("../../../conformance/cases")

	got, err := os.ReadFile("../../../docs/clients.md")
	if err != nil {
		t.Fatalf("docs/clients.md is missing; run `make docs`: %v", err)
	}
	if string(got) != want {
		t.Error("docs/clients.md does not match the adapters. Run `make docs`.")
	}
}

// The corpus index is what lets anyone build a runner in any language, so it
// must describe the cases that actually exist rather than the ones that did
// when somebody last remembered to regenerate it.
func TestCorpusIndexIsCurrent(t *testing.T) {
	want, err := conformance.Index("../../../conformance/cases")
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile("../../../conformance/index.json")
	if err != nil {
		t.Fatalf("conformance/index.json is missing; run `make docs`: %v", err)
	}
	if string(got) != string(want) {
		t.Error("conformance/index.json does not match the corpus. Run `make docs`.")
	}
}
