package agentplugins_test

import (
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/diag"
	"github.com/agentbridgehq/agentbridge/internal/importer"
	"github.com/agentbridgehq/agentbridge/internal/importer/agentplugins"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

func load(t *testing.T, dir string) (*importer.Result, error) {
	t.Helper()
	root, err := safepath.NewRoot(dir)
	if err != nil {
		t.Fatalf("NewRoot(%s): %v", dir, err)
	}
	return agentplugins.New().Import(root)
}

func mustLoad(t *testing.T, dir string) *importer.Result {
	t.Helper()
	res, err := load(t, dir)
	if err != nil {
		t.Fatalf("Import(%s): %v", dir, err)
	}
	return res
}

func TestImportValid(t *testing.T) {
	res := mustLoad(t, "testdata/valid")
	p := res.Plugin

	if p.Name != "acme.db-tools" || p.Version != "1.2.0" {
		t.Errorf("identity = %q@%q, want acme.db-tools@1.2.0", p.Name, p.Version)
	}
	if p.Origin.Dialect != ir.DialectAgentPlugins {
		t.Errorf("dialect = %q", p.Origin.Dialect)
	}
	if p.Author == nil || p.Author.Email != "dev@acme.example" {
		t.Errorf("author = %+v", p.Author)
	}
}

// An unknown top-level field must be reported and ignored, never fatal. The
// canonical schema closes the object but the conformance rules require
// tolerance; this is the case where following the schema literally would make
// us non-conformant.
func TestUnknownTopLevelFieldWarnsButLoads(t *testing.T) {
	res := mustLoad(t, "testdata/valid")

	if !hasCode(res.Diagnostics, diag.CodeManifestUnknownField) {
		t.Fatalf("expected %s diagnostic, got codes %v",
			diag.CodeManifestUnknownField, res.Diagnostics.Codes())
	}
	for _, d := range res.Diagnostics.Filter(diag.Error) {
		if d.Code == diag.CodeManifestUnknownField {
			t.Errorf("unknown field must not be an error: %v", d)
		}
	}
}

func TestSkillDiscovery(t *testing.T) {
	res := mustLoad(t, "testdata/valid")

	if len(res.Plugin.Skills) != 2 {
		t.Fatalf("got %d skills, want 2: %+v", len(res.Plugin.Skills), res.Plugin.Skills)
	}

	s, ok := res.Plugin.Skill("db-review")
	if !ok {
		t.Fatal("db-review not found")
	}
	if s.Kind != ir.SkillDirectory {
		t.Errorf("kind = %q, want %q", s.Kind, ir.SkillDirectory)
	}
	if s.Description != "Review SQL migrations before they ship" {
		t.Errorf("description = %q", s.Description)
	}
	// Frontmatter belongs to the Agent Skills spec, not to us; unknown keys
	// must survive rather than being filtered to a known set.
	if got := s.Frontmatter["custom-field"]; got != "preserved" {
		t.Errorf("custom frontmatter key not preserved: %v", s.Frontmatter)
	}
	if !strings.HasPrefix(s.ContentHash, ir.DigestPrefix) {
		t.Errorf("content hash = %q", s.ContentHash)
	}

	// A skill with no frontmatter still loads, named after its directory.
	if _, ok := res.Plugin.Skill("pdf-export"); !ok {
		t.Errorf("skill without frontmatter should fall back to the directory name")
	}
	if !hasCode(res.Diagnostics, diag.CodeSkillNoName) {
		t.Errorf("expected a %s warning for the unnamed skill", diag.CodeSkillNoName)
	}

	// A directory without SKILL.md is skipped with a warning, not an error.
	if !hasCode(res.Diagnostics, diag.CodeSkillMissingFile) {
		t.Errorf("expected a %s warning for skills/not-a-skill", diag.CodeSkillMissingFile)
	}
}

// One malformed server entry must not take the plugin, or the other servers,
// down with it. This is the isolation rule the conformance checklist states
// explicitly, and it is the difference between a typo costing one tool and a
// typo costing the whole plugin.
func TestInvalidServerIsIsolated(t *testing.T) {
	res := mustLoad(t, "testdata/valid")

	if _, ok := res.Plugin.MCPServer("db"); !ok {
		t.Error("valid stdio server was dropped")
	}
	if _, ok := res.Plugin.MCPServer("remote"); !ok {
		t.Error("valid streamable-http server was dropped")
	}
	if _, ok := res.Plugin.MCPServer("malformed"); ok {
		t.Error("server missing a required field should have been skipped")
	}
	if !hasCode(res.Diagnostics, diag.CodeMCPServerInvalid) {
		t.Errorf("expected %s, got %v", diag.CodeMCPServerInvalid, res.Diagnostics.Codes())
	}
}

// Non-loopback endpoints must use HTTPS. The schema cannot express this, so a
// client validating only against the schema would connect anyway.
func TestInsecureURLRejected(t *testing.T) {
	res := mustLoad(t, "testdata/valid")

	if _, ok := res.Plugin.MCPServer("insecure"); ok {
		t.Error("plain-http server to a non-loopback host should have been rejected")
	}
	if !hasCode(res.Diagnostics, diag.CodeMCPInsecureURL) {
		t.Errorf("expected %s, got %v", diag.CodeMCPInsecureURL, res.Diagnostics.Codes())
	}
}

// Foreign extension namespaces are carried verbatim. The specification forbids
// validating their contents, so preservation is the whole contract.
func TestExtensionsPreserved(t *testing.T) {
	res := mustLoad(t, "testdata/valid")

	raw, ok := res.Plugin.Extensions["com.example.client"]
	if !ok {
		t.Fatalf("extension namespace dropped: %v", res.Plugin.Extensions)
	}
	if !strings.Contains(string(raw), "anything") {
		t.Errorf("extension contents altered: %s", raw)
	}
}

func TestCapabilityInference(t *testing.T) {
	res := mustLoad(t, "testdata/valid")
	c := res.Plugin.Capabilities

	if !c.Exec {
		t.Error("a stdio server must imply exec")
	}
	if !c.Network {
		t.Error("an http server must imply network")
	}
	if !c.Secrets {
		t.Error("DB_API_TOKEN in env must imply secrets")
	}
	if len(c.Evidence) == 0 {
		t.Error("capabilities without evidence are unreviewable")
	}
	// A literal credential in a manifest is a real finding: manifests get
	// committed.
	if !hasCode(res.Diagnostics, diag.CodeSecretLiteralInEnv) {
		t.Errorf("expected %s, got %v", diag.CodeSecretLiteralInEnv, res.Diagnostics.Codes())
	}
}

func TestFatalCases(t *testing.T) {
	for _, tc := range []struct {
		dir  string
		want string
	}{
		{"testdata/bad-name", "consecutive hyphens"},
		{"testdata/missing-required", "name"},
		{"testdata/not-json", "invalid JSON"},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			_, err := load(t, tc.dir)
			if err == nil {
				t.Fatalf("expected rejection")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestDigestIsStableAndContentSensitive(t *testing.T) {
	a := mustLoad(t, "testdata/valid")
	b := mustLoad(t, "testdata/valid")

	da, err := a.Plugin.Digest()
	if err != nil {
		t.Fatal(err)
	}
	db, err := b.Plugin.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Fatalf("digest is not stable across loads:\n  %s\n  %s", da, db)
	}

	b.Plugin.Skills[0].ContentHash = "sha256:changed"
	dc, err := b.Plugin.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if dc == da {
		t.Error("digest did not change when skill content changed")
	}
}

// The digest must not depend on where the plugin happens to live, or two
// developers would compute different digests for identical bytes and the
// lockfile would be worthless.
func TestDigestIgnoresInstallLocation(t *testing.T) {
	a := mustLoad(t, "testdata/valid")
	da, err := a.Plugin.Digest()
	if err != nil {
		t.Fatal(err)
	}

	a.Plugin.Origin.Root = "/somewhere/else/entirely"
	db, err := a.Plugin.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Errorf("digest changed with install path:\n  %s\n  %s", da, db)
	}
}

func hasCode(ds diag.Diagnostics, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}
