# Discussion draft — offering a conformance corpus for 1.0.0

*Post to: https://github.com/agentplugins/agent-plugins-spec/discussions*

*Post **after** the two issue drafts. Arriving with reproducible defects found
while implementing the specification establishes that the work is real; arriving
first with an offer of a test suite reads as positioning, however good the suite
is.*

---

**Title:** Offering a conformance corpus for 1.0.0 — Apache-2.0, no tooling dependency, and what running it found

The [technical charter](https://github.com/agentplugins/agent-plugins-spec/blob/main/GOVERNANCE.md)
lists "managing reference implementations and test suites" among the TSC's
responsibilities, and `FUTURE_CONSIDERATIONS.md` records conformance test suites
as possible future work. While implementing a loader against 1.0.0 we built one,
and would rather donate it than maintain a parallel version.

This is an offer of an artifact, not a proposal to change the portable contract.

We have also now run it against four clients, and the results are below. They
are the reason we think a corpus is worth holding centrally rather than the
reason we built one.

## What it is

18 cases. Each is an ordinary plugin package plus a statement of what a
conformant client must do with it:

```
cases/002-unknown-top-level-field/
├── case.yaml     section, requirement, expected behaviour, what to observe
└── plugin/       an ordinary plugin package
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

There is a machine-readable `index.json` so a runner can be written in any
language. No dependency on our tooling, and nothing in the cases mentions it.

## What it covers

Deliberately weighted toward requirements **JSON Schema cannot express**, since
any client with a validator gets the rest right for free:

| § | What it checks |
|---|---|
| 4.1 | A `command` escaping the plugin root is invalid |
| 5.2 | Unknown fields and a non-object `extensions` are non-fatal |
| 5.3, 5.5 | Required fields; the name rule, including the `--` case |
| 6.2 | A plugin with no components is valid |
| 7.1 | Skills are immediate children only; a directory without `SKILL.md` is skipped |
| 7.2.1 | Transport security, empty `mcpServers`, all three transports |
| 7.2.2 | One malformed server does not affect the others |
| 9.2 | A reserved environment name invalidates the server |
| 10.1 | A version mismatch disables MCP without failing the plugin |

A second corpus covers the same MCP requirements through each client's own
configuration file, which is the only path any of them actually reads. Two of
its cases are answered by a probe that records the environment and working
directory it was launched with: §9.1 and §7.2.1 describe what a *process*
receives, and a client that writes the right value into a file it then fails to
apply looks correct everywhere except at run time.

The two §5.2/§8.1 cases are the ones we would most like other implementers to
run: they are the cases where following the published schema literally produces
a non-conformant client (see the accompanying issue).

## What running it found

Four clients, on macOS, in August 2026: Codex 0.144.5, opencode 1.18.3, Cursor
3.18.9 and VS Code 1.135.0. Codex has moved since — see finding 1. Full method and per-case notes are in the
[results](https://github.com/agentbridgehq/agentbridge/tree/main/conformance/results),
including which observations came from a client's own CLI and which required a
person looking at a window.

We are reporting **behaviour, not verdicts**. Every one of these has a plausible
reading in which the specification, not the implementation, is the thing worth
changing — which is exactly why we would rather raise them here than publish a
scoreboard.

**1. §7.2 was implemented by nobody when we measured, and by Codex a week
later.** At the time of the run no client read a package's `mcp.json`; servers
reached all four through the client's own configuration file instead —
`~/.cursor/mcp.json`, `config.toml`, `opencode.json`, VS Code's own `mcp.json`.
Nine of the eighteen cases had no path to the thing they were asking about,
which is why we wrote a
[second corpus](https://github.com/agentbridgehq/agentbridge/tree/main/conformance/mcp)
for the routes servers actually take.

That has since changed under us, and we would rather say so than let the finding
stand. Codex 0.144.5 contains no Agent Plugins MCP support — zero occurrences of
`${PLUGIN_ROOT}`, zero of `Agent Plugins MCP config`. Codex 0.151.0 contains two
and seventeen, along with `.plugin-data` and two strings that are §4.1 and §9.2
enforcement quoted almost verbatim: *"Agent Plugins MCP config resolves outside
the plugin root; disabling MCP"* and *"failed to create Agent Plugins data
directory; disabling stdio MCP servers"*. Juexin Wang reports it landing in
0.147.0 ([openai/codex#38438](https://github.com/openai/codex/issues/38438)).
We have not yet watched a plugin server start — that needs an authenticated
session, and `codex mcp list` shows configured servers only — so we are
reporting the change rather than a re-measurement. **The useful lesson is about
the suite, not the client: a result like this one is stale within a week, so the
value is in a corpus anyone can re-run, not in the numbers we happen to publish
with it.**

**2. One client of four loads a package as the specification defines it, and
finding 1 does not change that.** Cursor accepts an unmodified case. `codex
plugin add` on the same directory returns `missing plugin.json` — still true on
0.151.0, re-checked after the above — because it requires
`.codex-plugin/plugin.json`, and adding that one file with nothing else changed
makes the same package install. Claude Code requires `.claude-plugin/`. Three
vendors have each introduced a private manifest at a private path. Reading the
spec's `mcp.json` and accepting the spec's package are separate things, and
Codex is now the case that shows they can come apart: it understands the
contents without accepting the container.

**3. §7.1 splits the field, and the split has a mechanical cause.** The case
ships `alpha`, `beta`, and a third skill at `skills/group/deep/` that must not
be found. Cursor and VS Code load two; Codex and opencode load three. The two
that pass scan exactly one level; the two that fail scan recursively. Neither
behaviour looks chosen with the requirement in mind, and when half a small
sample gets a MUST wrong in the same direction we think that is more likely to
be a question about the requirement than four independent bugs. **Is
"immediate children only" load-bearing, or would `**/SKILL.md` be acceptable?**

**4. §7.2.1's working directory is accepted and ignored by two of five.** A
server started by VS Code or Claude Code runs in the directory the *client* was
started from, not the plugin root — verified by a small MCP server that reports
its own working directory, since this is invisible in configuration. A plugin
that opens `./config.json` therefore reads a different file depending on where
its user was standing.

**5. The published schema and §5.2 disagree about unknown fields inside a server
entry.** We wrote a corpus case asserting such a field is tolerated, on the
strength of §5.2, and our own validator rejected it: the MCP schema sets
`additionalProperties: false` on every server definition. We now believe §5.2
governs the manifest's top level only — but a first-time reader made the other
assumption, and the two documents can both be read as authoritative.

## Why it might be worth having centrally

A conformant client may support **neither** component type (§11.1, §11.2), so
"implements Agent Plugins" tells a user very little on its own. Several
requirements are unexpressible in JSON Schema. And §8.1 requires clients to
ignore each other's extension data *without validating it*, which makes
divergence cheap by design — sensibly, but it does mean real compatibility is an
empirical question.

A shared corpus lets each client answer that question about itself, in the same
terms as every other client. Held by the TSC it is a neutral reference; held by
any one implementer it is that implementer's opinion, which is a much weaker
thing however carefully it is written.

## What we are offering

- The 18 cases and the JSON index, Apache-2.0, with no attribution required.
- The second corpus for MCP servers as clients actually receive them — eleven
  cases covering transport spelling, isolation of a malformed entry, the
  loopback exception to the HTTPS rule, the reserved environment names, and two
  that are answered by a small MCP server reporting the environment and working
  directory it was started with, because §9.1 and §7.2.1 are about what a
  process receives and cannot be checked by reading a file.
- Rewriting either into whatever layout the maintainers prefer.
- Continuing to extend them as we find more requirements worth pinning down.

We are happy for this to be adopted wholesale, cherry-picked, used as a starting
point for something the TSC writes itself, or declined. If the answer is no, the
corpus stays where it is and remains free to use.

## What we are not claiming

**None of the above is an assertion that any client is non-conformant.** Each
result is a recorded observation of one version on one platform on one day, with
the method written down beside it, and a case we could not deliver is recorded
as `unmeasured` rather than failed — a client that never reads a manifest is not
failing to validate one.

We also got two of our own cases wrong on the first pass, both caught by an
implementation that already enforced the rules. That is the argument for a
shared corpus rather than against one: a case is a claim about what the
specification requires, and claims benefit from review by the people who wrote
it.

Our own loader passes all 18. That is a statement about our implementation and
nothing else.

Happy to open a pull request in whatever shape is most useful, or to leave it
here if the appetite is not there.
