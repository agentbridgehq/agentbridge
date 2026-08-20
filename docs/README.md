# Documentation

Everything written down, grouped by what you are trying to do.
The project overview lives in the [root README](../README.md).

## Using it

| | |
|---|---|
| **[Visual walkthrough](index.html)** | Open in a browser. What the tool is, in one scroll, with a live demonstration of the problem it solves. |
| **[Getting started](getting-started.md)** | The real workflow on your own machine: install, write a plugin, install it, keep a team in sync, back it out. |
| [Client compatibility](clients.md) | What each assistant accepts, and what it will silently drop. **Generated from the adapters** — a test fails if it drifts from the code. |
| [Writing a plugin](plugin-authors.md) | For plugin authors: the conformance traps, and how to avoid each one. |

## Trusting it

| | |
|---|---|
| **[Telemetry](telemetry.md)** | There is none. Every address the tool can reach, and why. The claim is enforced by a test, not promised. |
| [Security & threat model](05-security-and-trust.md) | Eleven threats, the control layers, and — marked explicitly — which controls exist today and which do not. |
| [Spec compliance](10-spec-compliance.md) | Requirement-by-requirement audit against Agent Plugins v1.0.0, including where we are deliberately not a client. |
| [Security policy](../SECURITY.md) | How to report a vulnerability. |

## Verifying it

| | |
|---|---|
| **[Testing by hand](../TESTING.md)** | Drive every feature yourself in a throwaway sandbox, about 30 minutes. Every command in it was run before it was written. |
| [Conformance corpus](../conformance/README.md) | 18 canonical plugin packages for testing **any** client against the specification, not just this one. |
| [Measuring a client](../conformance/PROTOCOL.md) | How to run the corpus against a real assistant and contribute the results. |

## Why it is built this way

The strategy the implementation was derived from. Read `01` and `05` if you
read only two.

| # | | |
|---|---|---|
| 00 | [Research: the standard](00-research-agent-plugins.md) | What Agent Plugins 1.0 actually requires, and the ten things it deliberately leaves out |
| 01 | [Vision & strategy](01-vision-and-strategy.md) | The thesis, three product shapes with a recommendation, the wedge, the moat, the risks |
| 02 | [Competitive landscape](02-competitive-landscape.md) | Registries, MCP gateways, client-native managers — and the empty quadrant |
| 03 | [Architecture](03-architecture.md) | The internal representation, adapters, lockfile, conformance harness |
| 04 | [Roadmap](04-roadmap.md) | Phases 0–4, each with evidence-based exit criteria |
| 05 | [Security & trust](05-security-and-trust.md) | The threat model, and our own obligations as something asking to be installed everywhere |
| 06 | [Business model](06-business-model-and-acquisition.md) | Pricing, metrics, acquirer map, and the anti-patterns to avoid |
| 07 | [Open questions](07-open-questions.md) | Decisions still to make, each with a recommendation |
| 08 | [Technology stack](08-tech-stack.md) | Language choice, libraries, licence hygiene |
| 09 | [UI & surfaces](09-ui-and-surfaces.md) | What a web UI would be for, and what stays out of it |
| 10 | [Spec compliance](10-spec-compliance.md) | The audit table |

## Project state

| | |
|---|---|
| **[MVP.md](../MVP.md)** | The live scope tracker: every milestone, its status, and the review notes behind each decision. Start here to know what is real. |
| [Releasing](../RELEASING.md) | How a release is cut, signed and verified — and the checklist before the first one. |
| [Contributing](../CONTRIBUTING.md) | How to propose a change. |

---

**A note on the generated files.** `clients.md` and `conformance/index.json` are
produced by `make docs` from the adapters and the corpus. Editing them by hand
is pointless — a test compares them against the code and fails on drift. That is
deliberate: a compatibility table maintained by hand is a compatibility table
that is wrong within a month.
