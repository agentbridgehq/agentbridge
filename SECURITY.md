# Security Policy

## Reporting a vulnerability

Report privately. Do not open a public issue.

- **Contact:** Masih Moloodian <masihmoloodian@gmail.com>
  *(interim; moves to security@ once the domain is settled — see D-02 in [MVP.md](MVP.md))*
- **Response:** acknowledgement within 3 business days, assessment within 10.
- **Disclosure:** coordinated. We will agree a date with you and credit you unless
  you prefer otherwise.

If a report concerns a third-party plugin or an agent client rather than
AgentBridge itself, say so — we will coordinate with the upstream vendor rather
than publish first. Neutrality across clients is a design goal, not a courtesy.

## Threat model

Full version in [docs/05-security-and-trust.md](docs/05-security-and-trust.md).
The short form of why this project takes security unusually seriously:

An agent plugin bundles two dangerous things at once.

1. **Executable code.** A stdio MCP server is `command + args + env`, launched on
   a developer's machine, in their session, with their credentials, their SSH
   agent, their cloud config, and their source tree. The Agent Plugins
   specification defines no sandbox.
2. **Executable instructions.** A `SKILL.md` is natural-language text loaded into
   a model's context that directs the agent's behavior. It is code for a
   probabilistic interpreter that holds tools.

The second category is new, and no mainstream security tooling inspects it.

## Our own obligations

We ask to be installed on developer machines, so:

- **No telemetry by default.** Anything opt-in is documented field by field.
- **No network calls to AgentBridge-operated services during normal operation.**
  Schemas are embedded; `$schema` is never fetched at load time, as the spec
  requires.
- **Signed releases with published provenance.** We cannot credibly ask for
  supply-chain hygiene while shipping unsigned binaries. (M8-1, M8-2.)
- **Path containment enforced everywhere.** Every path resolved from a manifest
  goes through `internal/safepath`, which rejects absolute paths, `..` escapes,
  and symlink escapes from the plugin root.
- **Foreign extension namespaces are preserved but never executed or trusted.**
  The specification requires clients to ignore namespaces they do not implement
  without validating their contents; we carry them verbatim and treat them as
  opaque data.

## Supported versions

Pre-release. No version is supported for production use yet.
