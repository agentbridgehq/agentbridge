# Agent Plugins conformance corpus

A set of plugin packages, each one designed so that a client's behaviour with it
answers a specific question about [Agent Plugins
v1.0.0](https://agent-plugins.org/specification).

Free to use, no attribution required, no dependency on AgentBridge. If you build
an agent client, this is here to help you check it.


## Results so far (2026-08-30)

Four clients run: [codex](results/codex.yaml), [opencode](results/opencode.yaml),
[cursor](results/cursor.yaml), [vscode](results/vscode.yaml).

| Client | pass | fail | unmeasured |
|---|---|---|---|
| Cursor 3.18.9 | 5 | 1 | 12 |
| Codex 0.144.5 | 4 | 1 | 13 |
| opencode 1.18.3 | 4 | 1 | 13 |
| VS Code 1.135.0 | 2 | 0 | 16 |

### Only one client accepts a conformant package

**Cursor loaded all 18 cases unmodified** — carrying only the specification's
`plugin.json`, deliberately without `.cursor-plugin/plugin.json`. It is the only
client tested that does.

The others each require their own manifest, or read none at all:

| Client | What it actually reads |
|---|---|
| **Cursor** | the specification's `plugin.json` — and its own `.cursor-plugin/plugin.json` |
| Codex | `.codex-plugin/plugin.json` only. `codex plugin add` on a conformant package returns **"missing plugin.json"** |
| Claude Code | `.claude-plugin/plugin.json` only |
| opencode, VS Code | no plugin manifest at all; skills are found by scanning directories |

Codex's rejection was confirmed by control: adding `.codex-plugin/plugin.json`
to an otherwise unchanged case makes the same package install. That is what
turns the inference into a measurement.

### §7.1 splits the field, and the split has a cause

The case ships `alpha`, `beta`, and a third skill at `skills/group/deep/` that
§7.1 says must not be found, because skills are immediate children of `skills/`.

| | 007 |
|---|---|
| Cursor, VS Code | **pass** — `alpha` and `beta` only |
| Codex, opencode | **fail** — all three |

The two that pass scan exactly one level; the two that fail scan recursively.
Neither behaviour looks deliberate with respect to the specification — one
happens to match it and one happens not to. A requirement that half of a small
sample gets wrong, in the same direction, is worth raising upstream as a
question about the requirement rather than only as four bug reports.

### The one manifest failure that could be scored

**Cursor fails 004-missing-required-name.** A manifest with no `name` must not
load and must contribute nothing; Cursor listed the plugin and made its skill
available. It is scored on Cursor alone because Cursor is the only client where
the package went through a real plugin mechanism — on the others the case was
placed in a skills directory, which never consults a manifest, so the same
observation would be evidence about the wrong thing.

### What stays unmeasured, and why

Nine cases per client are about MCP, which no client reads from a package —
servers come from `mcp.json`, `config.toml` and `opencode.json` instead. Three
are about rejecting an invalid manifest, unreachable wherever no manifest is
read. On VS Code a further six are unmeasured because six cases ship a skill
named `one` and VS Code reported the name without saying which root it came
from; Cursor attributed every skill to its plugin folder, which is why the same
six are scored there.

Claude Code and Gemini CLI are not conformance targets: neither claims to
implement the specification, which is the gap this project exists to bridge.

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
agentbridge conformance --list                     # the corpus as a checklist
agentbridge conformance --record cursor > results/cursor-1.x.yaml
```

`--record` writes one entry per case with every outcome pre-set to
`unmeasured` and the observation note inline, so a measurement session is
editing one word per case rather than authoring YAML. Step-by-step instructions
per client are in [PROTOCOL.md](PROTOCOL.md).

Without this tool at all: [`index.json`](index.json) describes every case in
machine-readable form — id, section, requirement, plugin path, expected
behaviour — so a runner can be written in any language against plain plugin
directories.

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
