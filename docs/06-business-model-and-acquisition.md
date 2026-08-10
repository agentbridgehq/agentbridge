# 06 — Business Model & Acquisition Strategy

The stated goal is to build something a large company would want to acquire. That goal changes *how* you build, not just what you build. This document is explicit about both.

## 1. Monetization

| Tier | Who | What | Price shape |
|---|---|---|---|
| **OSS** | Individual devs | CLI, adapters, lockfile, validate/doctor, local scan, compatibility matrix | Free, Apache-2.0, forever |
| **Team** | 5–50 dev teams | Shared config, fleet inventory, drift, hosted scan, signature verification, basic policy | ~$15–25 / dev / month |
| **Enterprise** | 200+ devs, security-led | Private registry, policy-as-code, SSO/SCIM, audit + evidence export, gateway, self-host, support/SLA | ~$40–80 / dev / month, annual, floor ~$50k |
| **Runtime** (later) | Teams running agents in prod | Hosted gateway: metered tool calls, secret brokerage, egress control | Usage-based |

Notes on pricing shape:
- Per-developer is the right meter early (matches how the buyer already budgets for Copilot/Cursor seats) and it grows without a new sale.
- Resist per-plugin or per-tool-call pricing in the CLI tiers; it punishes the adoption you need.
- The Enterprise tier is not sold on convenience. It is sold on **inventory, blast radius, and evidence**.

## 2. Metrics that matter

**Leading (adoption):** weekly active CLI installs · plugins installed/week · third-party READMEs recommending us · external adapter contributions · clients covered.

**Mid (product truth):** % installs with a fidelity warning · % of installed plugins that are signed · time-to-answer for "who has plugin X" · scan findings per 1,000 plugins.

**Lagging (business):** paying orgs · ARR · NRR · seats per account · free→paid conversion · security findings published.

**The one number an acquirer will index on:** *distinct developers with our binary installed and active.* Everything else can be rebuilt; installed distribution cannot.

## 3. Who acquires this, and why

| Acquirer | Strategic rationale | What they'd pay for |
|---|---|---|
| **Microsoft / GitHub** | Owns VS Code + Copilot + Azure + Entra. Enterprise governance of agent extensions is a natural GitHub Advanced Security extension. TSC member. | Fleet inventory + policy, cross-client (they'd keep it neutral for credibility, as with npm) |
| **AWS** | TSC member, Kiro, already publishing MCP gateway/registry OSS. Wants enterprise AI governance to run on AWS. | Control plane + compliance evidence |
| **Vercel** | Initiated the standard. Developer-experience-led. | The CLI, the matrix, the ecosystem position |
| **Anysphere (Cursor)** | Enterprise motion needs governance; but least likely to want cross-client neutrality | Policy + secrets for their enterprise tier |
| **OpenAI** | TSC member; enterprise deployment of Codex/ChatGPT agents needs admin controls | Fleet + policy |
| **Snyk / Chainguard / Socket** | Direct category extension: supply-chain security for a new artifact type | The scanner, the corpus, the findings |
| **JFrog** | Artifact management for a new package type | Registry + provenance |
| **Datadog** | Agent observability is an obvious adjacency | Gateway telemetry + inventory |
| **Cloudflare** | Edge runtime for MCP; wants the control plane | Gateway |
| **Docker** | Already distributing MCP servers; needs the governance layer | Registry + sandboxing |
| **Okta / CyberArk** | Identity for non-human actors is their stated frontier | Secret brokerage + per-tool authz |

**Most likely realistic outcomes**, in order: (1) security vendor acquiring the scanner + corpus; (2) hyperscaler/GitHub acquiring the fleet position; (3) acqui-hire by a TSC member who decides to build. Plan for (1) and (2); (3) is the floor, not the goal.

## 4. Build-to-be-acquired checklist

Things that are cheap now and expensive-to-impossible later.

**Legal / IP**
- Clean entity from day one (Delaware C-corp or equivalent), clean cap table, standard vesting.
- **Contributor License Agreement or DCO from the first external PR.** Retrofitting a CLA across 60 contributors kills deals.
- No GPL/AGPL dependencies in anything commercial. Automated license scanning in CI from week one.
- Trademark the name and secure it internationally before launch. Verify `agentbridge` isn't already claimed in this space — assume it may be, and have a fallback.
- Employment agreements with proper IP assignment, including for contractors.

**Technical**
- Clean separation between OSS core and commercial control plane — separate repos, no leakage. An acquirer needs to know exactly what they're buying and what stays open.
- No single-vendor lock-in in the stack that complicates a hyperscaler acquisition (avoid deep coupling to one cloud's proprietary services).
- Documented architecture and runbooks. Diligence goes better when the docs already exist — which is what this repo is.
- Security posture ready for diligence: SOC 2 Type II by the time you have 20 enterprise customers.

**Commercial**
- Multi-year contracts with real logos beat larger ARR from monthly SMB.
- Never let one customer exceed ~20% of revenue.
- Keep customer data cleanly separable per tenant; an acquirer will ask about migration and data rights.

**Relationship**
- Be visibly useful to the TSC: contribute proposals, run the conformance harness for the community, report bugs to every client vendor. The most common path to acquisition in dev infra is *"we already work with them and they're good."*
- Do not become dependent on one vendor's goodwill for distribution.

## 5. Anti-patterns that destroy acquirability

1. **Monetizing the CLI.** Kills the distribution that is the only unique asset.
2. **Picking a side.** The instant we're seen as "the Cursor tool" or "the Anthropic tool," the neutrality moat and the cross-client sale both evaporate — and so do 4 of the 5 likely acquirers.
3. **Becoming a plugin marketplace with revenue share.** Puts us in direct conflict with every client vendor's own store, for low margin.
4. **Copying MCP gateway feature lists.** Commodity fight, free OSS alternative from AWS, no defensible position.
5. **Shipping heavy telemetry.** One privacy incident on a tool with root-adjacent access on developer machines ends the company.
6. **Fighting the spec.** Extending it publicly and contributing is fine; forking it is fatal.
7. **Raising too much, too early, on an adoption-stage metric.** A $60M valuation on 5k CLI users prices out the natural $30–80M tuck-in acquisitions that are the realistic outcome.

## 6. Funding posture

The realistic exit band for a well-executed tuck-in in this category is roughly $30–150M. That implies:
- Bootstrap or a small pre-seed/seed ($1–3M) through Phase 2.
- One Series A only if enterprise pipeline demonstrably needs the headcount.
- Keep the option of profitability at 8–15 people. Optionality *is* the negotiating leverage in an acquisition conversation.

## 7. What to do in the next 90 days that most affects the outcome

1. Ship the CLI and get it into third-party plugin READMEs. Distribution compounds; nothing else does.
2. Publish the compatibility matrix. It creates the neutral-referee reputation.
3. Find and disclose one real security issue in a popular plugin. It creates the category.
4. Get the legal hygiene done while it costs $5k instead of a deal.
