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
| 3 | [`discussion-conformance-suite.md`](discussion-conformance-suite.md) | Discussions | The offer of the corpus — and what running it against four clients found, which is the stronger half |

The sequencing is the point. Arriving with two specific, reproducible defects
found while implementing the specification establishes that we have actually
built the thing. Arriving first with an offer of a test suite reads as
positioning, however good the suite is.

Since these were drafted the corpus has been run against four clients, and the
results are now the most useful thing in the folder — a specification author is
offered tools often and measurements of their own specification almost never.
They live in draft 3 rather than in a fourth post, because they are the evidence
for holding a corpus centrally rather than a separate announcement.

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
- **Report behaviour, never verdicts.** Four clients have now been measured, and
  that changes what this guardrail says rather than removing it. Write "`codex
  plugin add` returns `missing plugin.json`" and "two of four scan recursively",
  never "Codex is non-conformant". Each result is one version, one platform, one
  day, with the method beside it; a case that could not be delivered is
  `unmeasured`, not failed. Several of the findings implicate clients whose
  vendors sit on the steering committee, which is a reason for more care in the
  wording and none at all for leaving the finding out.
- **Offer §7.1 as a question, not as four bug reports.** Cursor and VS Code load
  two skills; Codex and opencode load three. When half a small sample gets the
  same MUST wrong in the same direction, the requirement is the more likely
  problem, and saying so first is both more accurate and better received.
