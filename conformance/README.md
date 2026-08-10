# Agent Plugins conformance corpus

A set of plugin packages, each one designed so that a client's behaviour with it
answers a specific question about [Agent Plugins
v1.0.0](https://agent-plugins.org/specification).

Free to use, no attribution required, no dependency on AgentBridge. If you build
an agent client, this is here to help you check it.

## Why this exists

The specification leaves more room for divergence than it looks:

- A conformant client may support **neither** skills nor MCP servers (§11.1,
  §11.2), so "it loads Agent Plugins" tells a user very little.
- Several requirements **cannot be expressed in JSON Schema** — transport
  security, executable-token resolution, working-directory containment, header
  validity — so a client validating only against the published schema will
  accept plugins the specification forbids.
- In one case, following the published schema literally makes a client
  **non-conformant**: the schema closes the manifest object, but §5.2 requires
  an unknown top-level field to be reported and ignored rather than rejected.
- The `extensions` escape hatch requires clients to ignore each other's data
  *without validating it* (§8.1), which makes divergence cheap by design.

None of that is a criticism of the specification — a small interoperability
floor is what it set out to be. But it means real compatibility is an empirical
question, and right now nobody is answering it.

## Running the corpus

Against a loader you can drive programmatically:

```bash
agentbridge conformance
```

Against any other client, by hand:

```bash
agentbridge conformance --list
```

Each case prints the plugin directory and what to look for. Install the plugin
into your client the way a user would, then check the observation note.

## What a case looks like

```
conformance/cases/002-unknown-top-level-field/
├── case.yaml        what a conformant client must do
└── plugin/          an ordinary plugin package
```

```yaml
id: 002-unknown-top-level-field
title: An unknown top-level field is reported and ignored
section: "5.2"
requirement: MUST
expect:
  loads: true
  skills: 1
observe: |
  The plugin loads and its skill is available. The client should report the
  unknown field somewhere; it must not reject the plugin.
```

`expect.diagnostics` lists reason codes specific to this implementation. Ignore
it when testing another client — the `observe` note is the portable statement.

## Contributing a result

Copy `results/_template.yaml`, fill in what you observed, open a pull request.

Three rules, because a compatibility matrix is only worth anything if its
failures are trustworthy:

1. **Record what you saw, not what you expected.** A case you did not run is
   `unmeasured`. It is never inferred from a related case, from documentation,
   or from a vendor's claim.
2. **Name the version.** Client behaviour changes between releases, and a result
   without a version cannot be reproduced or retired.
3. **A failure is a bug report, not a verdict.** If a client fails a case, the
   most likely explanations in order are: the corpus is wrong, the installation
   was wrong, and only then the client is wrong. Say what you saw and let the
   vendor respond — we would rather withdraw a claim than defend one.

## Adding a case

A case earns its place if a reasonable implementer could get it wrong and the
mistake would be invisible to a user. Include the specification section, say
whether it is a MUST or a SHOULD, and write an observation note somebody can act
on without reading this repository's source.

Cases that only exercise the published JSON Schema are the least valuable ones
here: any client using a schema validator gets those right for free. The
valuable cases are the requirements the schema cannot express.
