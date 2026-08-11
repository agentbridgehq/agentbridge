package source

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The redirect policy is tested directly because the case that matters cannot
// be reached from an httptest server: every test server is loopback, and
// loopback is exactly where plain HTTP is legitimate. The rule being checked is
// about a *remote* registry, so the rule itself is what gets exercised.
func TestCheckRedirectRefusesADowngrade(t *testing.T) {
	request := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: u, Host: u.Host}
	}

	for _, tc := range []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{
			name: "https to https, a CDN handoff",
			from: "https://ghcr.io/v2/org/p/blobs/sha256:abc",
			to:   "https://cdn.example.net/blob/xyz",
		},
		{
			name:    "https downgraded to http",
			from:    "https://ghcr.io/v2/org/p/blobs/sha256:abc",
			to:      "http://cdn.example.net/blob/xyz",
			wantErr: true,
		},
		{
			// A loopback registry started on http may redirect within http:
			// there is no downgrade, because there was nothing to downgrade
			// from.
			name: "http to http, a local registry",
			from: "http://127.0.0.1:5000/v2/org/p/blobs/sha256:abc",
			to:   "http://127.0.0.1:5001/blob/xyz",
		},
	} {
		err := checkRedirect(request(tc.to), []*http.Request{request(tc.from)})
		if tc.wantErr && err == nil {
			t.Errorf("%s: allowed", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: refused: %v", tc.name, err)
		}
		if tc.wantErr && err != nil && !strings.Contains(err.Error(), "https") {
			t.Errorf("%s: error should say why: %v", tc.name, err)
		}
	}
}

// A chain of redirects is a registry that is not doing its job, and following
// one indefinitely is how a client gets walked around the internet.
func TestCheckRedirectBoundsTheChain(t *testing.T) {
	u, err := url.Parse("https://ghcr.io/x")
	if err != nil {
		t.Fatal(err)
	}
	req := &http.Request{URL: u, Host: u.Host}

	var via []*http.Request
	for i := 0; i < maxRedirects; i++ {
		via = append(via, req)
	}
	if err := checkRedirect(req, via); err == nil {
		t.Errorf("a chain of %d redirects was followed", maxRedirects)
	}
	if err := checkRedirect(req, via[:1]); err != nil {
		t.Errorf("a single redirect was refused: %v", err)
	}
}

// safeJoin is the function standing between an attacker-chosen filename and the
// user's filesystem, so it is worth testing on its own terms as well as through
// a whole pull.
func TestSafeJoinRefusesEscapes(t *testing.T) {
	dst := "/tmp/pkg"

	for _, name := range []string{
		"../escaped", "../../escaped", "a/../../escaped", "/absolute",
		`..\windows`, `a\..\..\escaped`, "..", "./..", "a/./../../x",
		"C:\\windows\\x", "", ".",

		// An interior `..` that would resolve back inside the tree is refused
		// too. Allowing it would mean cleaning the path before checking it,
		// which is precisely the ordering that let `../escaped.txt` through
		// during development — filepath.Clean absorbs the traversal and the
		// check then has nothing to find. No archive tool emits `a/../b`, so
		// the strictness costs nothing real and removes the hazard entirely.
		"skills/x/../y/SKILL.md",
	} {
		if got, ok := safeJoin(dst, name); ok {
			t.Errorf("safeJoin(%q) = %q, want refusal", name, got)
		}
	}

	for _, name := range []string{
		"plugin.json", "skills/query/SKILL.md", "./plugin.json",
		"a/b/c/d.txt", "skills/query/references/notes.md",
	} {
		if _, ok := safeJoin(dst, name); !ok {
			t.Errorf("safeJoin(%q) refused an ordinary path", name)
		}
	}
}
