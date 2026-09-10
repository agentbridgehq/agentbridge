package main

import "testing"

// TestALocalSourceIsRecognisedOnEveryPlatform.
//
// The audit has to know whether an installed plugin came from a directory on
// this machine, because `sources.local: deny` turns on that answer. The
// obvious test — does the path start with a "/" — is true on Unix and false
// for C:\Users\… on Windows, so a local install there was classified as remote
// and the rule silently did not apply to it.
//
// That is the fail-open direction, and CI on Windows was the only thing that
// caught it. This test makes it catchable anywhere: ParseRef classifies a
// drive-letter path as local on every platform, so the case can be asserted
// from a Mac.
func TestALocalSourceIsRecognisedOnEveryPlatform(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   bool
		why    string
	}{
		{"/Users/masih/plugin", true, "a Unix absolute path"},
		{`C:\Users\masih\plugin`, true, "a Windows path with backslashes — the case that shipped broken"},
		{"C:/Users/masih/plugin", true, "a Windows path with forward slashes"},
		{"./relative", true, "a relative directory"},
		{"", true, "an unrecorded source is treated as local, the more restrictive reading"},
		{"github.com/acme/db", false, "a git remote"},
		{"oci://ghcr.io/acme/db", false, "an OCI reference"},
	} {
		if got := isLocalSource(tc.source); got != tc.want {
			t.Errorf("isLocalSource(%q) = %v, want %v — %s", tc.source, got, tc.want, tc.why)
		}
	}
}
