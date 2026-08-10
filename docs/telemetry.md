# Telemetry and data collection

**AgentBridge collects no telemetry.** There is no usage reporting, no crash
reporting, no version check, no analytics, and no account. The CLI makes no
network requests to any infrastructure we operate — not on install, not on
first run, not ever.

This is a short document because there is nothing to enumerate.

## What the tool does reach

| Action | Destination | Chosen by |
|---|---|---|
| Installing from a repository | The git remote in the reference you gave | You |
| Nothing else | — | — |

Fetching a plugin shells out to `git` against the remote named in the reference
you typed. That is the only outbound connection the CLI can make, and you
supplied the address.

Everything else works offline. The Agent Plugins JSON Schemas are embedded in
the binary rather than fetched — the specification requires this ("Clients MUST
NOT retrieve a schema while loading a plugin", §5.2) and it is also what makes
the tool usable on an air-gapped machine.

## How you can check, rather than trust

The claim is enforced by tests in
[`internal/privacy`](../internal/privacy/privacy_test.go), which fail the build
if any package that ships in the binary:

- constructs an HTTP client or makes an HTTP, TCP, WebSocket or gRPC call;
- references a hostname on a domain we operate;
- stops embedding the schemas.

The point of testing it rather than promising it is that this kind of claim
erodes quietly. One well-meant crash reporter, one "anonymous" usage ping, and a
tool installed on every developer machine in a company is phoning home. Nobody
notices until a security review does, and by then the documentation is false.

You can also check for yourself:

```bash
grep -rn "net/http" internal/ cmd/ --include="*.go"
```

The single result is a header-name validity check, which touches no network.

## What is stored locally

| Path | Contents |
|---|---|
| `~/.agentbridge/receipts.json` | What was installed where, so removal is exact |
| `~/.agentbridge/cache/` | Fetched plugin packages, addressed by content |
| `~/.agentbridge/data/<plugin>/` | The `PLUGIN_DATA` directory §9.1 requires |
| OS credential store | Secrets you stored with `agentbridge secret set` |

None of it leaves the machine. Secrets are held in the platform credential
store — Keychain, Credential Manager, Secret Service — and never written into a
configuration file unless you pass `--allow-plaintext-secrets`.

## If this ever changes

Any future workspace or control-plane features are opt-in, require an account
you create deliberately, and will document every field transmitted before they
ship. The commitments that will not change:

- The CLI keeps working with no network access to anything we operate.
- Nothing is sent by default.
- Source code, prompts, file contents and tool-call payloads are never
  transmitted.

See [docs/05 §4](05-security-and-trust.md) for why this is treated as a
product constraint rather than a policy preference: a tool asking to be
installed on every developer machine in a company, with the access this one
needs, does not get a second chance at a privacy incident.
