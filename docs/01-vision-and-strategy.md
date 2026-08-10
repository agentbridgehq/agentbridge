# 01 — Vision & Strategy

## 1. The problem, stated precisely

A working developer in mid-2026 runs **three to six agent clients**: Copilot in VS Code, Cursor, Codex, ChatGPT, Claude Code, plus something in the terminal. Each one loads extensions from a different place, with different config, different secrets, different permissions.

Agent Plugins 1.0 fixes exactly one slice of this: *the shape of the folder*. It does not fix:

- where the folder comes from,
- how it gets onto 400 laptops,
- how you keep it current,
- how you know it isn't malicious,
- how you prove to an auditor what it did.

So the industry now has a **format without a supply chain**. npm without npmjs.com. Docker images without a registry, a scanner, or a pull policy.

Meanwhile a plugin is a uniquely nasty artifact from a security standpoint. It is simultaneously:
- **executable code** (a stdio MCP server is `command + args + env`, run on the developer's machine with their credentials), and
- **untrusted natural-language instruction** (a `SKILL.md` is injected into a model's context and steers its behavior).

Nothing in the ecosystem currently inspects the second category at all. Every existing MCP gateway governs *server traffic*; none of them govern *what instructions your agents have been told to follow*.

## 2. Thesis

> **AgentBridge is the supply chain and control plane for agent extensions.**
> Portable format is the standard's job. Getting the right plugin, verified, onto the right agent, with the right secrets and the right policy — and proving it afterwards — is ours.

Two beliefs this rests on:

1. **Neutrality is the moat.** Every TSC member (OpenAI, Microsoft, Amazon, Cursor, Vercel) will build plugin management *for their own client*. None will build first-class support for a competitor's client. An enterprise with Copilot + Cursor + Claude Code needs one place to govern all three. Only a non-client vendor can credibly be that place.
2. **The buyer is security/platform, not the individual developer.** Developers adopt for convenience (free, OSS). Companies pay for control, evidence, and blast-radius reduction. Build the first to earn the second.

## 3. Product shape — three candidates, one recommendation

| # | Shape | What it is | Pros | Cons |
|---|---|---|---|---|
| A | **Dev tool / translator** | CLI that installs & converts plugins across clients | Fast to build, viral, clear value day one | Thin. A feature, not a company. Vendors absorb it. |
| B | **Runtime gateway** | Proxy fronting all MCP servers; authn, secrets, rate limits, audit | Real revenue, proven category | Crowded (Obot, Arcade, TrueFoundry, AWS OSS, Docker). Ignores skills entirely. |
| C | **Fleet control plane** | Registry + policy + attestation + inventory across every agent client on every machine | Largest TAM, strongest moat, clearest acquirer story | Slow to sell, needs distribution first, enterprise motion |

**Recommendation: A → C, with B as the runtime substrate.**

Ship A as free OSS to win distribution and become the default verb (`agentbridge install …`). The CLI is also the **agent** — it is already installed on every developer machine, which is precisely the beachhead C requires. Add B as the local runtime (the same binary, `--gateway` mode) so that traffic flows through us without a separate deployment decision. Then sell C to the security and platform teams who discover, via A's inventory, that they have no idea what their developers have installed.

This is the Docker Desktop → Docker Hub → Docker Business arc, and the Snyk CLI → Snyk platform arc. Both were acquired-or-scaled on exactly this sequence.

## 4. Positioning statement

> For platform and security teams whose developers use multiple AI coding agents, **AgentBridge** is an agent-extension control plane that gives one inventory, one policy, and one audit trail across every agent client — unlike per-vendor plugin managers, which see only their own client, and unlike MCP gateways, which see only tool traffic and never see skills.

Category name to push: **AXM — Agent Extension Management.** (Deliberately echoes MDM. The analogy sells itself to a CISO in one sentence: *"You have MDM for laptops and nothing for the agents running on them."*)

## 5. Wedge sequence

**Wedge 1 — "It just works everywhere" (free, OSS, individual devs).**
One command installs a plugin into every agent client on the machine, including the non-conformant ones (Claude Code, Gemini CLI, Zed, Windsurf, Continue) via adapters. Lockfile. `agentbridge doctor` explains why a plugin silently did nothing in a given client. This last piece matters more than it looks: because the spec permits a conformant client to support *neither* component type, "why doesn't this work in X?" will be the ecosystem's most common question, and we will own the answer.

**Wedge 2 — "What do we actually have?" (free inventory, paid reporting).**
The CLI reports installed plugins to an optional workspace. A platform lead runs it across the team and gets the first real inventory of agent extensions in their company. Inventory is the trojan horse; every governance sale starts with a scary list.

**Wedge 3 — "Prove it's safe" (paid).**
Static + LLM analysis of skills for injection patterns, MCP servers for exfiltration behavior, provenance and signing, allow/deny policy enforced at install time by the CLI already deployed in Wedge 1.

**Wedge 4 — "Run it under our control" (paid, expansion).**
Hosted gateway, secret brokerage, per-tool authorization, full tool-call audit, cost attribution.

## 6. Moat

| Layer | Durability | Why |
|---|---|---|
| Adapters for non-conformant clients | Medium | Unglamorous, high-maintenance, and no vendor will build them for rivals |
| Compatibility matrix (real, tested, machine-readable) | Medium-high | Requires continuous conformance testing across N clients × M versions; becomes the thing everyone cites |
| Cross-client fleet inventory | High | Network effect inside the org; switching cost is the historical record |
| Skill-level threat intelligence corpus | High | Data moat — grows with usage, cannot be cold-started |
| Attestation/signing trust root | Very high if adopted | Whoever signs becomes load-bearing infrastructure |

## 7. Principal risks

| Risk | Severity | Mitigation |
|---|---|---|
| **A TSC member ships this** (most likely: Microsoft via GitHub, or AWS) | High | Compete on neutrality and competitor-client coverage. Never be beatable on "works with the client they don't own." Stay small enough to be a cheaper acquisition than a build. |
| **The TSC standardizes a registry**, commoditizing distribution | Medium | Do not bet the company on distribution. Bet it on governance + evidence, which a spec body will not standardize. Contribute to the registry proposal and be its best client. |
| **Standard churn** (1.1/2.0 breaks assumptions) | Medium | Keep an internal IR (see [03](03-architecture.md)); treat the spec as one of several input dialects, not the core model. |
| **Anthropic never adopts** | Low/positive | Increases our value — the bridge exists precisely because of that gap. |
| **Enterprises don't feel the pain yet** | Medium-high | The pain arrives with the first public agent-plugin supply-chain incident. Build the scanner before that, be the vendor with the write-up ready. |
| **Nobody pays for "governance of a folder"** | Medium | Anchor to compliance artifacts (evidence exports for SOC 2 / ISO 42001 / EU AI Act), not to convenience. |

## 8. Non-goals (explicit)

- Not building an agent, an IDE, or a model router.
- Not authoring first-party plugins beyond a handful of reference/demo ones.
- Not forking or competing with the Agent Plugins spec. We are its best-behaved citizen and loudest implementer.
- Not a consumer marketplace with revenue share. (Tempting, low margin, and puts us in direct conflict with every client vendor's own store.)

## 9. What "success" looks like at 18 months

- 25k+ weekly active CLI installs.
- Default install instruction in ≥200 third-party plugin READMEs.
- Published compatibility matrix cited by the ecosystem.
- 20–40 paying orgs, ≥$1M ARR run-rate, ≥3 with >1,000 seats.
- At least one publicly-credited supply-chain finding.
- One inbound acquisition conversation from a TSC-member company.
