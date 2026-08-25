package secrets_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/secrets"
)

func TestParseRef(t *testing.T) {
	for _, tc := range []struct {
		value string
		name  string
		ok    bool
	}{
		{"${secret:acme/api-token}", "acme/api-token", true},
		{"${secret:TOKEN}", "TOKEN", true},
		{"${secret:a.b_c-d/e}", "a.b_c-d/e", true},

		{"plain", "", false},
		{"${PLUGIN_ROOT}", "", false},
		{"${secret:}", "", false},
		// A reference inside a larger value is not a reference. Partial
		// interpolation would mean reassembling the secret wherever it is used,
		// and every such place becomes one that can log the result.
		{"Bearer ${secret:token}", "", false},
		{"${secret:token}suffix", "", false},
	} {
		ref, ok := secrets.ParseRef(tc.value)
		if ok != tc.ok {
			t.Errorf("ParseRef(%q) ok = %v, want %v", tc.value, ok, tc.ok)
			continue
		}
		if ok && ref.Name != tc.name {
			t.Errorf("ParseRef(%q) name = %q, want %q", tc.value, ref.Name, tc.name)
		}
	}
}

// The near-miss has to be distinguishable from a plain value, because §9.2
// requires a conformant client to pass unrecognized placeholder text through
// literally — so writing one out would send the placeholder to the server.
func TestContainsRefCatchesTheNearMiss(t *testing.T) {
	if !secrets.ContainsRef("Bearer ${secret:token}") {
		t.Error("an embedded reference must be detectable")
	}
	if secrets.ContainsRef("${PLUGIN_ROOT}/bin") {
		t.Error("a specification placeholder is not a secret reference")
	}
}

func TestRefRoundTrip(t *testing.T) {
	ref := secrets.Ref{Name: "acme/token"}
	parsed, ok := secrets.ParseRef(ref.String())
	if !ok || parsed != ref {
		t.Errorf("round trip: %q -> %+v (ok=%v)", ref.String(), parsed, ok)
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"a", "acme/token", "a.b-c_d", "9lives"} {
		if err := secrets.ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/leading", "-leading", "has space", "has$dollar"} {
		if err := secrets.ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) should have failed", bad)
		}
	}
}

// ------------------------------------------------------------ detection

func TestDetectKnownTokenShapes(t *testing.T) {
	// A token in a variable whose name gives nothing away is exactly the case
	// name-matching alone misses.
	for _, tc := range []struct{ key, value string }{
		{"API_URL", "sk-abc123def456ghi789"},
		{"SETTING", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"X", "xoxb-123-456-abcdef"},
		{"CONFIG", "AKIAIOSFODNN7EXAMPLE"},
		{"DATA", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig"},
	} {
		f, ok := secrets.Detect(tc.key, tc.value)
		if !ok {
			t.Errorf("Detect(%q, %q) missed a credential", tc.key, tc.value)
			continue
		}
		if f.Confidence != secrets.High {
			t.Errorf("Detect(%q) confidence = %q, want high", tc.key, f.Confidence)
		}
	}
}

func TestDetectByName(t *testing.T) {
	f, ok := secrets.Detect("DB_API_TOKEN", "hunter2")
	if !ok {
		t.Fatal("a credential-shaped name should be detected")
	}
	if f.Confidence != secrets.Low {
		t.Errorf("confidence = %q, want low for a short value", f.Confidence)
	}

	f, ok = secrets.Detect("DB_API_TOKEN", "T0k3nWithPlentyOfEntropy8812")
	if !ok || f.Confidence != secrets.Medium {
		t.Errorf("a random-looking value should raise confidence: %+v", f)
	}
}

// A false positive here blocks an install, so the quiet cases must stay quiet.
func TestDetectIgnoresNonSecrets(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"LOG_LEVEL", "debug"},
		{"API_URL", "https://api.example.com/v1"},
		{"TOKEN", ""},
		{"TOKEN", "${secret:acme/token}"},
		{"CACHE_DIR", "${PLUGIN_DATA}/cache"},
		{"DESCRIPTION", "a sentence with several words in it"},
	} {
		if f, ok := secrets.Detect(tc.key, tc.value); ok {
			t.Errorf("Detect(%q, %q) false positive: %+v", tc.key, tc.value, f)
		}
	}
}

func TestMaskNeverRevealsEnough(t *testing.T) {
	const value = "sk-abcdefghijklmnopqrstuvwxyz"
	masked := secrets.Mask(value)

	if strings.Contains(masked, "defghijklmnop") {
		t.Errorf("mask leaks the body: %q", masked)
	}
	if len(masked) == 0 {
		t.Error("mask produced nothing")
	}
	if secrets.Mask("short") != "•••••" {
		t.Errorf("a short value should be fully masked, got %q", secrets.Mask("short"))
	}
}

// ------------------------------------------------------------ stores

func TestMemoryStore(t *testing.T) {
	s := secrets.NewMemory()

	if _, err := s.Get("absent"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("Get(absent) = %v, want ErrNotFound", err)
	}
	if err := s.Set("acme/token", "v"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get("acme/token"); err != nil || got != "v" {
		t.Errorf("Get = %q, %v", got, err)
	}

	names, err := s.List()
	if err != nil || len(names) != 1 || names[0] != "acme/token" {
		t.Errorf("List = %v, %v", names, err)
	}

	if err := s.Delete("acme/token"); err != nil {
		t.Fatal(err)
	}
	// Removing something twice must be fine: uninstall has to be idempotent.
	if err := s.Delete("acme/token"); err != nil {
		t.Errorf("second delete = %v", err)
	}
}

func TestEnvStore(t *testing.T) {
	t.Setenv(secrets.EnvVarName("acme/api-token"), "from-env")

	var s secrets.Env
	got, err := s.Get("acme/api-token")
	if err != nil || got != "from-env" {
		t.Errorf("Get = %q, %v", got, err)
	}

	// Read-only by design: a build agent supplies secrets, it does not store
	// them, and pretending otherwise would imply persistence it has not got.
	if err := s.Set("x", "y"); err == nil {
		t.Error("the environment backend must refuse writes")
	}
}

func TestEnvVarNameMapping(t *testing.T) {
	if got := secrets.EnvVarName("acme.db/api-token"); got != "AGENTBRIDGE_SECRET_ACME_DB_API_TOKEN" {
		t.Errorf("EnvVarName = %q", got)
	}
}

// The environment is consulted before the keychain so a CI run can override a
// developer's local value without either knowing about the other — which is
// what lets one lockfile work on a laptop and on a build agent.
func TestChainPrefersEnvironment(t *testing.T) {
	t.Setenv(secrets.EnvVarName("acme/token"), "from-env")

	mem := secrets.NewMemory()
	if err := mem.Set("acme/token", "from-store"); err != nil {
		t.Fatal(err)
	}

	chain := secrets.Chain{secrets.Env{}, mem}
	got, err := chain.Get("acme/token")
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env" {
		t.Errorf("Get = %q, want the environment to win", got)
	}
}

func TestChainFallsThrough(t *testing.T) {
	mem := secrets.NewMemory()
	if err := mem.Set("only/here", "v"); err != nil {
		t.Fatal(err)
	}

	chain := secrets.Chain{secrets.Env{}, mem}
	if got, err := chain.Get("only/here"); err != nil || got != "v" {
		t.Errorf("Get = %q, %v", got, err)
	}
	if _, err := chain.Get("nowhere"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("Get(nowhere) = %v, want ErrNotFound", err)
	}
}

func TestRefsIn(t *testing.T) {
	refs := secrets.RefsIn(map[string]string{
		"A": "${secret:one}",
		"B": "literal",
		"C": "${secret:two}",
	})
	if len(refs) != 2 || refs["A"].Name != "one" || refs["C"].Name != "two" {
		t.Errorf("RefsIn = %+v", refs)
	}
}
