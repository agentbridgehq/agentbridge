package adapter_test

import (
	"strings"
	"testing"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/secrets"
)

func stdio(env map[string]string) ir.MCPServer {
	return ir.MCPServer{
		Name: "db", Transport: ir.TransportStdio,
		Command: "npx", Args: []string{"@acme/db"}, Env: env,
	}
}

func hasLoss(f adapter.Fidelity, code string) bool {
	for _, l := range f.Losses {
		if l.Code == code {
			return true
		}
	}
	return false
}

// M5-3: these files are routinely committed — .vscode/mcp.json is documented as
// something to share with a team — and the specification says outright that env
// values are visible package data (§9.2). Writing a live credential into one by
// default is not a defensible position.
func TestPlaintextCredentialIsRefusedByDefault(t *testing.T) {
	var f adapter.Fidelity
	_, _, allowed := adapter.PrepareSecrets(
		stdio(map[string]string{"API_TOKEN": "sk-abc123def456ghi"}),
		adapter.PlanOptions{}, &f, "/home/u/.cursor/mcp.json")

	if allowed {
		t.Error("a credential-looking literal must not be written by default")
	}
	if !hasLoss(f, adapter.LossSecretPlaintextRefused) {
		t.Errorf("refusal not reported: %+v", f.Losses)
	}
	// The message has to say what to do next, or the only discoverable
	// response is to give up.
	joined := lossText(f)
	for _, want := range []string{"agentbridge secret set", "${secret:", "--allow-plaintext-secrets"} {
		if !strings.Contains(joined, want) {
			t.Errorf("message does not mention %q: %s", want, joined)
		}
	}
}

func TestPlaintextCredentialAllowedExplicitly(t *testing.T) {
	var f adapter.Fidelity
	out, _, allowed := adapter.PrepareSecrets(
		stdio(map[string]string{"API_TOKEN": "sk-abc123def456ghi"}),
		adapter.PlanOptions{AllowPlaintextSecrets: true}, &f, "/cfg")

	if !allowed {
		t.Fatal("explicit permission should allow the write")
	}
	if out.Env["API_TOKEN"] == "" {
		t.Error("the value was dropped despite permission")
	}
	// Permitted is not the same as unremarkable.
	if !hasLoss(f, adapter.LossSecretInPlaintext) {
		t.Errorf("writing a credential should still be reported: %+v", f.Losses)
	}
}

func TestNonSecretsPassThroughUntouched(t *testing.T) {
	in := stdio(map[string]string{"LOG_LEVEL": "debug", "API_URL": "https://api.example.com"})

	var f adapter.Fidelity
	out, notes, allowed := adapter.PrepareSecrets(in, adapter.PlanOptions{}, &f, "/cfg")

	if !allowed {
		t.Fatalf("ordinary configuration was refused: %+v", f.Losses)
	}
	if out.Command != "npx" || len(notes) != 0 || len(f.Losses) != 0 {
		t.Errorf("configuration without credentials should be untouched: %+v", out)
	}
}

// The core of M5: a referenced secret is injected at launch, so the value never
// reaches a configuration file.
func TestSecretReferenceRoutesThroughTheLauncher(t *testing.T) {
	store := secrets.NewMemory()
	if err := store.Set("acme/token", "s3cr3t"); err != nil {
		t.Fatal(err)
	}

	in := stdio(map[string]string{
		"API_TOKEN": "${secret:acme/token}",
		"LOG_LEVEL": "debug",
	})

	var f adapter.Fidelity
	out, notes, allowed := adapter.PrepareSecrets(in, adapter.PlanOptions{
		Launcher: "/usr/local/bin/agentbridge",
		Secrets:  store,
	}, &f, "/cfg")

	if !allowed {
		t.Fatalf("a stored secret should install: %+v", f.Losses)
	}
	if out.Command != "/usr/local/bin/agentbridge" {
		t.Errorf("command = %q, want the launcher", out.Command)
	}

	args := strings.Join(out.Args, " ")
	if !strings.Contains(args, "--secret API_TOKEN=acme/token") {
		t.Errorf("args do not carry the reference: %v", out.Args)
	}
	if !strings.Contains(args, "-- npx @acme/db") {
		t.Errorf("the real command was lost: %v", out.Args)
	}

	// The whole point: the value is nowhere in what gets written.
	rendered := out.Command + " " + args
	for k, v := range out.Env {
		rendered += " " + k + "=" + v
	}
	if strings.Contains(rendered, "s3cr3t") {
		t.Errorf("the secret value reached the plan: %s", rendered)
	}
	// Non-secret environment survives.
	if out.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("ordinary env was dropped: %v", out.Env)
	}
	if len(notes) == 0 {
		t.Error("launching indirectly should be explained, not silent")
	}
}

// Discovering at launch that a credential was never stored produces a server
// that fails inside a client with no useful diagnostic. Catching it at install
// time is the difference between a clear message and an afternoon.
func TestMissingSecretIsCaughtAtInstall(t *testing.T) {
	var f adapter.Fidelity
	_, _, allowed := adapter.PrepareSecrets(
		stdio(map[string]string{"API_TOKEN": "${secret:acme/never-stored}"}),
		adapter.PlanOptions{Launcher: "/bin/agentbridge", Secrets: secrets.NewMemory()},
		&f, "/cfg")

	if allowed {
		t.Error("a reference to a secret that is not stored must not install")
	}
	if !hasLoss(f, adapter.LossSecretMissing) {
		t.Errorf("not reported: %+v", f.Losses)
	}
	if !strings.Contains(lossText(f), "agentbridge secret set") {
		t.Errorf("message should say how to fix it: %s", lossText(f))
	}
}

// §9.2 requires a conformant client to leave unrecognized placeholder text
// literal, so an embedded reference would be handed to the server as the
// placeholder string itself.
func TestEmbeddedReferenceIsRefused(t *testing.T) {
	var f adapter.Fidelity
	_, _, allowed := adapter.PrepareSecrets(
		stdio(map[string]string{"AUTH_HEADER": "Bearer ${secret:acme/token}"}),
		adapter.PlanOptions{Launcher: "/bin/agentbridge", Secrets: secrets.NewMemory()},
		&f, "/cfg")

	if allowed {
		t.Error("a reference inside a larger value cannot be resolved and must not be written")
	}
	if !hasLoss(f, adapter.LossSecretPartialRef) {
		t.Errorf("not reported: %+v", f.Losses)
	}
}

func TestNoLauncherMeansNoSecretReferences(t *testing.T) {
	store := secrets.NewMemory()
	if err := store.Set("acme/token", "v"); err != nil {
		t.Fatal(err)
	}

	var f adapter.Fidelity
	_, _, allowed := adapter.PrepareSecrets(
		stdio(map[string]string{"API_TOKEN": "${secret:acme/token}"}),
		adapter.PlanOptions{Secrets: store}, &f, "/cfg")

	if allowed {
		t.Error("without a launcher there is no way to inject the value")
	}
	if !hasLoss(f, adapter.LossSecretNoLauncher) {
		t.Errorf("not reported: %+v", f.Losses)
	}
}

func lossText(f adapter.Fidelity) string {
	var b strings.Builder
	for _, l := range f.Losses {
		b.WriteString(l.Reason)
		b.WriteString("\n")
	}
	return b.String()
}
