package main

import (
	"os"
	"testing"
)

// docs/clients.md is generated. Compatibility documentation written by hand
// goes stale within a release and then actively misleads, which for this page
// would be worse than having none — the product claim is that we tell people
// the truth about what each client takes.
func TestClientDocsAreCurrent(t *testing.T) {
	want := Render()

	got, err := os.ReadFile("../../../docs/clients.md")
	if err != nil {
		t.Fatalf("docs/clients.md is missing; run `make docs`: %v", err)
	}
	if string(got) != want {
		t.Error("docs/clients.md does not match the adapters. Run `make docs`.")
	}
}
