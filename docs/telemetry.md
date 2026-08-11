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
| Installing from a registry | The registry host in the `oci://` reference you gave | You |
| Obtaining an anonymous pull token | The auth host that registry names in its challenge | That registry |
| Nothing else | — | — |

Fetching a plugin from a repository shells out to `git` against the remote named
in the reference you typed.

Fetching from an OCI registry is the one place the CLI opens a connection
itself, because the registry API cannot be delegated to a subprocess the way
`git` can. Every destination is built from the reference: the host in
`oci://ghcr.io/org/plugin:v1` is the only host contacted, and
[`internal/privacy`](../internal/privacy/privacy_test.go) fails the build if the
fetcher contains any hardcoded URL at all.

The one exception is worth stating plainly rather than leaving as a surprise.
Registries answer an unauthenticated request with a challenge naming where to
get a pull token, and that is routinely a *different* host — Docker Hub answers
for `registry-1.docker.io` from `auth.docker.io`. So a registry can direct one
request to a host you did not type. That request carries no credentials and no
identity, but it does tell that host which repository this machine is pulling.
It must be HTTPS; agentbridge refuses a plaintext realm.

AgentBridge pulls **anonymously only**. It does not read your Docker
credentials, your keychain, or `~/.docker/config.json`, and a registry that
requires authentication produces an error rather than a silent search for
credentials you never mentioned. A private registry is therefore not supported
yet — that is a real limitation, and the alternative would be a tool that
quietly starts using credentials against hosts you did not name.

Everything else works offline. The Agent Plugins JSON Schemas are embedded in
the binary rather than fetched — the specification requires this ("Clients MUST
NOT retrieve a schema while loading a plugin", §5.2) and it is also what makes
the tool usable on an air-gapped machine.

## How you can check, rather than trust

The claim is enforced by tests in
[`internal/privacy`](../internal/privacy/privacy_test.go), which fail the build
if any package that ships in the binary:

- constructs an HTTP client or makes an HTTP, TCP, WebSocket or gRPC call —
  except `internal/source/oci.go`, named explicitly in an allow-list of exact
  filenames so a second file cannot quietly join it;
- **contains any absolute URL in that one exempt file**, which is what keeps
  "only hosts you named" true rather than merely intended. A hardcoded host is
  how a version check or a failure report arrives, and permitting HTTP without
  this check would leave the door open;
- references a hostname on a domain we operate — this applies to the OCI client
  too, unchanged;
- stops embedding the schemas.

A runtime test in `internal/source` drives a full pull against a fake registry
and asserts every request went to the host in the reference.

The point of testing it rather than promising it is that this kind of claim
erodes quietly. One well-meant crash reporter, one "anonymous" usage ping, and a
tool installed on every developer machine in a company is phoning home. Nobody
notices until a security review does, and by then the documentation is false.

You can also check for yourself:

```bash
grep -rn "net/http" internal/ cmd/ --include="*.go"
```

The results are the OCI registry client and a header-name validity check that
touches no network.

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
