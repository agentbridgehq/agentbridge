# Discussion draft — offering a conformance corpus for 1.0.0

*Post to: https://github.com/agentplugins/agent-plugins-spec/discussions*

*Post **after** the two issue drafts. Arriving with reproducible defects found
while implementing the specification establishes that the work is real; arriving
first with an offer of a test suite reads as positioning, however good the suite
is.*

---

**Title:** Offering a conformance corpus for 1.0.0 — 18 cases, Apache-2.0, no tooling dependency

The [technical charter](https://github.com/agentplugins/agent-plugins-spec/blob/main/GOVERNANCE.md)
lists "managing reference implementations and test suites" among the TSC's
responsibilities, and `FUTURE_CONSIDERATIONS.md` records conformance test suites
as possible future work. While implementing a loader against 1.0.0 we built one,
and would rather donate it than maintain a parallel version.

This is an offer of an artifact, not a proposal to change the portable contract.

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

The two §5.2/§8.1 cases are the ones we would most like other implementers to
run: they are the cases where following the published schema literally produces
a non-conformant client (see the accompanying issue).

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
- Rewriting them into whatever layout the maintainers prefer.
- Continuing to extend them as we find more requirements worth pinning down.

We are happy for this to be adopted wholesale, cherry-picked, used as a starting
point for something the TSC writes itself, or declined. If the answer is no, the
corpus stays where it is and remains free to use.

## What we are not claiming

We have measured **no** third-party client. Our own loader passes all 18 cases;
that is a statement about our implementation and nothing else. We have
deliberately not published pass or fail results for anyone else's software, and
would not without running the cases and saying who ran them and against which
version.

Happy to open a pull request in whatever shape is most useful, or to leave it
here if the appetite is not there.
