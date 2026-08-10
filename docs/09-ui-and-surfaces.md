# 09 — The UI: What It's For

## 1. The governing principle

> **The CLI answers questions about *your* machine. The UI answers questions about *other people's* machines.**

That single sentence determines what belongs in the web UI and what doesn't.

A developer never needs a UI to install a plugin — `agentbridge install` is faster and scriptable. But no CLI can answer:

- *"Which of our 400 developers has `acme/db-tools` installed, and at what version?"*
- *"A popular plugin was backdoored last night. What's our blast radius?"*
- *"Which machines drifted from the policy we agreed on?"*
- *"Show the auditor every agent extension in use in Q3 and who approved it."*

These are **aggregate, cross-machine, cross-time** questions. They require a server, and once you have a server, the humans who ask these questions — platform leads, security engineers, compliance — are not CLI users. That is the UI's entire reason to exist.

A second, honest reason: **the inventory screen is the sales artifact.** The moment a platform lead sees the real list of what their developers have installed, the budget conversation starts. That is a legitimate product function, not a marketing afterthought.

## 2. Who uses it

| Persona | Primary surface | What they need |
|---|---|---|
| **Individual developer** | CLI (99%) | Almost never opens the UI. May hit a public plugin report page from a README link |
| **Platform / DevEx lead** | UI | Inventory, drift, catalog curation, rollout |
| **Security engineer** | UI | Findings triage, blast radius, incident response |
| **Engineering manager** | Slack + UI | Approvals, spend, adoption |
| **CISO / compliance** | UI (quarterly) | Posture dashboard, evidence export |
| **Plugin author (external)** | Public UI | Compatibility results, validation, a badge for their README |

Note the split: the people who *use* the product daily barely touch the UI, and the people who *buy* it live in it. Both are fine, as long as you don't confuse them.

## 3. Surface map

### 3.1 Public — no login (adoption engine, free forever)

**Compatibility matrix.** Client × component × version, generated nightly from the conformance harness ([03 §7](03-architecture.md)). Answers "does this work in Zed?" — the ecosystem's most common question, which nobody currently answers. This page is the neutrality asset: cheap to produce, impossible for any client vendor to publish credibly, and it makes us the referee.

**Plugin report cards.** One page per known public plugin: what it contains (skills, MCP servers), inferred capabilities (exec? network? reads home dir?), signature and provenance status, per-client compatibility, risk summary, version history and diffs. Shareable URL, linkable from any README.

This is the Socket.dev / Snyk Advisor playbook: free public pages that rank in search, get cited in security discussions, and quietly train the market to check before installing. It is the single highest-leverage top-of-funnel asset, and it costs almost nothing beyond the scanner we're building anyway.

**Docs and the spec conformance report.** Including our own conformance results, published honestly.

### 3.2 Workspace — logged in (the commercial product)

Ordered roughly by build priority.

**1. Fleet inventory.** The core screen. A table of every plugin across every machine and every client: name, version, source, signature status, risk, machine count, client breakdown, first seen, last seen. Filterable and groupable by team, client, risk, source.

This is where a customer discovers they have 14 versions of the same plugin, 3 from a fork nobody recognizes, and 40 machines running something unsigned. Everything else in the product is downstream of this screen.

**2. Plugin detail (internal view).** For one plugin in *this* org: who has it, which clients, which versions, where it came from, a diff against the previous version, scan findings, approval state and history, and a one-click "quarantine fleet-wide."

**3. Findings & triage.** Scan results across the fleet, deduplicated, severity-ranked, each showing affected machines and users. Actions: accept risk (with expiry), suppress with justification, require remediation, quarantine. Everything logged.

**4. Drift.** Declared intent (`agentbridge.yaml`, org policy) versus observed reality. Unpinned installs, stale versions, machines that haven't checked in, manual edits to client configs that bypassed us. Drift is what converts a one-time inventory into a recurring reason to log in.

**5. Policy — view and simulate.** Policy is authored **as code in git**, not in the UI. The UI's job is to *show* the active policy and, critically, to **simulate** it: *"this rule would block 3 currently-installed plugins across 12 machines — here they are."*

No security team enables enforcement blind. The dry-run pane is what makes them click enable, and it is the feature most likely to be missing from a naive build.

**6. Catalog / internal registry.** The curated, approved set. Two audiences on one screen: platform leads curate it, and developers browse it to find what's already allowed. The developer-facing half is deliberately boring and fast — it competes with "just install the random one from GitHub," and it only wins by being easier.

**7. Approvals.** A developer hits a policy block, requests an exception, a reviewer approves or denies with justification. **Most of this interaction happens in Slack, not the UI** — the UI is the record and the queue of last resort. Building this UI-first is a common and expensive mistake.

**8. Activity & tool-call audit** (Phase 3+, requires the gateway). Tool calls over time by plugin, tool, user, and machine. Anomaly flags. Cost and latency attribution. Redacted payload inspection for incident response.

**9. Evidence export.** Pick a date range and framework, get an auditor-ready pack: inventory snapshots, policy versions, approvals, findings and their resolution, access logs. This is the screen that converts a security feature into a budget line ([05 §6](05-security-and-trust.md)).

**10. Settings.** SSO/SAML/OIDC, SCIM, RBAC, enrollment and machine registration, integrations (Slack, Jira, Datadog, Artifactory), API tokens, data retention.

### 3.3 Non-UI surfaces that carry more traffic than the UI

Do not assume the web app is the main interaction point. It isn't.

| Surface | Why it matters |
|---|---|
| **Slack app** | Where approvals and alerts actually get handled. Highest-frequency human touchpoint in the product |
| **GitHub PR checks** | A lockfile diff annotated with risk changes — "this PR adds a plugin that can exec and read `~/.aws`" — reviewed in the tool people already use |
| **SARIF → GitHub code scanning / GitLab** | Findings appear in the security tooling they already run, with zero new UI to learn |
| **Webhooks + public API** | Enterprises automate. Every UI action must have an API equivalent |
| **The CLI's own output** | Fidelity reports, `doctor`, install warnings. The most-read "UI" in the whole product |

## 4. Design rules

1. **API-first.** The UI is a client of the public API and gets no privileged endpoints. If the UI can do it, a script can.
2. **Read-heavy, write-light.** The UI is for *seeing and deciding*. Configuration changes flow through git so they're reviewable and revertible. Legitimate UI writes: triage decisions, approvals, quarantine, settings.
3. **Never in the enforcement path.** Policy is enforced by the CLI on the machine and by the gateway at call time. If our web app is down, nothing breaks and nothing silently becomes permissive. This is also what makes the product deployable in air-gapped environments.
4. **No feature is UI-only.** Enterprises automate everything eventually; a UI-only capability becomes a blocker in a large deal.
5. **Every number is drillable to machines and people.** "23 high-risk plugins" is useless without the list. Aggregates that dead-end are the most common failure of governance dashboards.
6. **Optimize the empty state.** A new workspace with no data must immediately show *how to enroll machines* and, ideally, results within minutes. Governance products die in onboarding.

## 5. Explicitly not in the UI

| Not building | Why |
|---|---|
| Installing plugins from the web | That's the CLI's job. A web install button implies remote code execution on developer machines — an attack surface with no upside |
| A plugin authoring/editing environment | Not our product. Authors use their editor |
| Policy as the source of truth in a form builder | Git is the source of truth. UI shows and simulates |
| A consumer marketplace with ratings and revenue share | Non-goal per [01 §8](01-vision-and-strategy.md) — conflicts with every client vendor's own store, low margin |
| A chat interface / agent | We govern agents. We are not one |
| Real-time dashboards with live-updating charts | Nobody watches these. Alerts go to Slack |

## 6. Build order

Phase 2 ships the **public compatibility matrix and plugin report cards** (free, no login — adoption, not revenue) plus a minimal **workspace: inventory + findings**. That's enough for the first paid teams.

Phase 3 adds drift, policy simulation, catalog, approvals, audit, and evidence export — the enterprise set.

Phase 3+ adds tool-call activity once the gateway exists.

The order matters: inventory before policy, because you can't govern what you can't see, and because the inventory screen is what makes someone want to pay for the policy screen.
