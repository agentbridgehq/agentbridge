# Upstream contributions to the Agent Plugins specification

Drafts, ready to post. Nothing here has been submitted — that needs a human with
a GitHub account and a view on timing.

Their [CONTRIBUTING](https://github.com/agentplugins/agent-plugins-spec/blob/main/CONTRIBUTING.md)
sets the shape of all three:

- **Discussions** come before pull requests. *"When in doubt, begin with a
  Discussion rather than investing in a specification patch."*
- **Issues** are for *"concrete defects such as contradictions, broken
  references, schema mismatches, or unclear requirements that can lead to
  incompatible implementations."*
- A technically complete pull request *"is not, by itself, evidence of
  implementor consensus"*.

## Order to post them

| # | File | Where | Why this order |
|---|---|---|---|
| 1 | [`issue-name-pattern-lookahead.md`](issue-name-pattern-lookahead.md) | Issues | The most concrete: a schema that cannot be compiled at all by an entire language ecosystem. Easy to verify, hard to argue with |
| 2 | [`issue-schema-text-conflict.md`](issue-schema-text-conflict.md) | Issues | Two places where following the published schema literally makes a client non-conformant |
| 3 | [`discussion-conformance-suite.md`](discussion-conformance-suite.md) | Discussions | The offer of the corpus, landing after two useful bug reports rather than cold |

The sequencing is the point. Arriving with two specific, reproducible defects
found while implementing the specification establishes that we have actually
built the thing. Arriving first with an offer of a test suite reads as
positioning, however good the suite is.

## What not to do

- **Do not open a specification pull request.** Nothing here proposes changing
  the portable contract. Two are defect reports; one offers an artifact for
  something the [technical charter](https://github.com/agentplugins/agent-plugins-spec/blob/main/GOVERNANCE.md)
  already lists among the TSC's responsibilities ("managing reference
  implementations and test suites").
- **Do not lead with the product.** The corpus is useful to them whether or not
  anyone ever installs agentbridge, and it should be offered on those terms. It
  is Apache-2.0 and has no dependency on our tooling — the JSON index exists
  precisely so a runner can be written in any language.
- **Do not assert non-conformance of any client.** We have measured none of
  them. Saying so plainly is what makes the rest credible.
