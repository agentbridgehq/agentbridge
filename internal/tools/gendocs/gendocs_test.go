package main

import (
	"os"
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/conformance"
)

// docs/clients.md is generated. Compatibility documentation written by hand
// goes stale within a release and then actively misleads, which for this page
// would be worse than having none — the product claim is that we tell people
// the truth about what each client takes.
// normaliseEOL strips CR before comparing.
//
// .gitattributes pins the working tree to LF, but a checkout made before that
// existed — or a contributor with core.autocrlf set — still has CRLF on disk,
// and the generator always emits LF. Comparing raw bytes then reports "run
// make docs" for a difference make docs cannot fix.
func normaliseEOL(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func TestClientDocsAreCurrent(t *testing.T) {
	want := Render("../../../conformance/cases")

	got, err := os.ReadFile("../../../docs/clients.md")
	if err != nil {
		t.Fatalf("docs/clients.md is missing; run `make docs`: %v", err)
	}
	if normaliseEOL(string(got)) != normaliseEOL(want) {
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
	if normaliseEOL(string(got)) != normaliseEOL(string(want)) {
		t.Error("conformance/index.json does not match the corpus. Run `make docs`.")
	}
}
