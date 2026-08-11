# Telemetry and data collection

**AgentBridge collects no telemetry.** There is no usage reporting, no crash
reporting, no version check, no analytics, and no account. The CLI makes no
network requests to any infrastructure we operate — not on install, not on
first run, not ever.

Every address this tool can reach is one you supplied: a git remote, a registry
in an `oci://` reference, or — only if you ask for it — a model endpoint you
configure. There is no destination compiled into the binary, and a test fails
the build if one appears.

## What the tool does reach

| Action | Destination | Chosen by |
|---|---|---|
| Installing from a repository | The git remote in the reference you gave | You |
| Installing from a registry | The registry host in the `oci://` reference you gave | You |
| Obtaining an anonymous pull token | The auth host that registry names in its challenge | That registry |
| Downloading a layer | The CDN that registry redirects to | That registry |
| **`scan --classify`** (off by default) | The model endpoint you configure | You |
| Nothing else | — | — |

Fetching a plugin from a repository shells out to `git` against the remote named
in the reference you typed.

Fetching from an OCI registry is the one place the CLI opens a connection
itself, because the registry API cannot be delegated to a subprocess the way
`git` can. Every destination it *originates* is built from the reference — the
host in `oci://ghcr.io/org/plugin:v1` and nothing else — and
[`internal/privacy`](../internal/privacy/privacy_test.go) fails the build if the
fetcher contains any hardcoded URL at all. There is no address in this tool that
you did not supply.

**Two exceptions, both of which a registry chooses and neither of which you
type.** They are stated here rather than left to be discovered:

1. **The token endpoint.** Registries answer an unauthenticated request with a
   challenge naming where to get a pull token, and that is routinely a different
   host — Docker Hub answers for `registry-1.docker.io` from `auth.docker.io`.
2. **The blob CDN.** No large registry serves layers itself. Both ghcr.io and
   Docker Hub answer a blob request with a redirect, so downloading a plugin
   means contacting whichever CDN they nominate.

Neither request carries credentials or any identity — the pull is anonymous, and
`net/http` strips the authorization header on a cross-host redirect, so the pull
token does not reach the CDN. Both do tell that host which plugin this machine
is fetching.

What is enforced in both cases is the transport: a realm must be HTTPS, and a
redirect may never *downgrade* an HTTPS pull to plain HTTP. The layer digest
would catch substituted content either way, but a plaintext request tells anyone
on the path exactly which plugin is being installed and invites them to answer
first. Redirect chains are capped at five.

AgentBridge pulls **anonymously only**. It does not read your Docker
credentials, your keychain, or `~/.docker/config.json`, and a registry that
requires authentication produces an error rather than a silent search for
credentials you never mentioned. A private registry is therefore not supported
yet — that is a real limitation, and the alternative would be a tool that
quietly starts using credentials against hosts you did not name.

## The model pass, which is the one that sends your content

Everything above *fetches*. `agentbridge scan --classify` is different in kind:
it **sends the text of your plugin's skills** to a model, so it deserves its own
section rather than a row in a table.

It is **off by default and cannot turn itself on.** The function the whole CLI
calls, `scanner.Scan`, takes no classifier and cannot be given one; only
`ScanWith` can run a model pass, and only when handed a configured client.
That is a property of the type signature rather than a flag, and
[a test](../cmd/agentbridge/json_test.go) runs the real binary with an endpoint
configured in the environment and asserts nothing is contacted without
`--classify`.

When you do enable it:

| | |
|---|---|
| **What is sent** | The contents of `SKILL.md` and the files under `references/`. One request per file. |
| **What is not sent** | Bundled scripts, `mcp.json`, your configuration, your lockfile, file paths outside the plugin, anything about your machine. |
| **Where** | The endpoint in `--classifier-endpoint` or `AGENTBRIDGE_CLASSIFIER_ENDPOINT`. **There is no default** — agentbridge will not choose a destination for you, and errors if you ask for the pass without naming one. |
| **Credentials** | The API key comes from the OS credential store (`agentbridge secret set classifier-key`) or `AGENTBRIDGE_CLASSIFIER_KEY`. It is never written to a config file, and never appears in output — the endpoint is redacted to scheme and host, in case its path carries a token. |

The endpoint must be HTTPS unless it is on this machine, so **a model running
locally is a first-class option**: point `--classifier-endpoint` at
`http://localhost:11434/...` and the air-gapped, send-nothing property is fully
preserved while still getting the pass. `--classify` together with `--offline`
is an error rather than a silent skip.

If your plugin text is confidential, this is a decision to take deliberately —
which is exactly why it is not a default.

Everything else works offline. The Agent Plugins JSON Schemas are embedded in
the binary rather than fetched — the specification requires this ("Clients MUST
NOT retrieve a schema while loading a plugin", §5.2) and it is also what makes
the tool usable on an air-gapped machine.

## How you can check, rather than trust

The claim is enforced by tests in
[`internal/privacy`](../internal/privacy/privacy_test.go), which fail the build
if any package that ships in the binary:

- constructs an HTTP client or makes an HTTP, TCP, WebSocket or gRPC call —
  except `internal/source/oci.go` and `internal/scanner/classify.go`, named
  explicitly in an allow-list of exact filenames so a third file cannot quietly
  join them;
- **contains any absolute URL in either exempt file**, which is what keeps "only
  hosts you named" true rather than merely intended. A hardcoded host is how a
  version check or a failure report arrives, and permitting HTTP without this
  check would leave the door open. It is why the classifier has no default
  endpoint: there is nowhere in the code for one to live;
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

The results are the OCI registry client, the optional model pass, and a
header-name validity check that touches no network.

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
