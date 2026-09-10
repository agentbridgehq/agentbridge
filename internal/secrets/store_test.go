package secrets_test

import (
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/secrets"
)

// TestRemovingAnEnvSecretDoesNotReportSuccess.
//
// The environment backend is read-only: a secret set through it stays set. The
// chain used to skip a store whose Delete failed and then return nil anyway, so
// `agentbridge secret rm` printed "Removed" over a credential that was still
// live and still listed by `secret list`. For a credential that is the worst
// possible thing to be wrong about — somebody believes they revoked access and
// they have not.
func TestRemovingAnEnvSecretDoesNotReportSuccess(t *testing.T) {
	t.Setenv(secrets.EnvVarName("acme/token"), "live-value")

	chain := secrets.Chain{secrets.Env{}}
	if err := chain.Delete("acme/token"); err == nil {
		t.Fatal("deleting a read-only secret reported success; it is still set")
	}

	// It really is still there, which is the point.
	if v, err := chain.Get("acme/token"); err != nil || v != "live-value" {
		t.Errorf("Get after the failed delete = %q, %v; want the value still present", v, err)
	}
}

// Removing something no store holds stays quiet and successful. That is
// idempotence and the interface promises it; the honesty above must not turn
// into noise here.
func TestRemovingAnAbsentSecretIsNotAnError(t *testing.T) {
	chain := secrets.Chain{secrets.Env{}}
	if err := chain.Delete("nothing/here"); err != nil {
		t.Errorf("deleting an absent secret errored: %v", err)
	}
}
