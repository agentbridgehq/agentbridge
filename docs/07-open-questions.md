# 07 — Open Questions & Decisions to Make

Ordered by how much downstream work they block. Each has a recommendation so nothing is stuck waiting for a meeting.

## Blocking — decide before writing code

| # | Question | Options | Recommendation |
|---|---|---|---|
| D1 | **Is the primary product the CLI or the control plane?** | (a) CLI-first, monetize later (b) enterprise-first | **(a)**. The fleet position is the only asset a competitor can't buy. See [01 §3](01-vision-and-strategy.md). |
| D2 | **Implementation language for the CLI** | Go · Rust · TypeScript | **Go**. Single static binary, cross-compile, OCI/Sigstore ecosystem, hiring pool. Rust only if sandboxing becomes core early. |
| D3 | **License split** | All Apache-2.0 · Apache core + BSL control plane · open core | **Apache-2.0 core + proprietary/source-available control plane, in separate repos.** Clean for acquirers, credible for OSS adoption. Avoid AGPL entirely. |
| D4 | **Artifact distribution mechanism** | Build a registry · use OCI registries · git-only | **OCI**. Digests, signing, mirroring, auth, and every enterprise already runs one. Support git as a convenience source. |
| D5 | **Do we depend on the spec's `$schema` version model, or our own IR?** | Direct · IR | **IR.** Non-negotiable; it's what survives 1.1/2.0. |
| D6 | **Name & trademark** | `agentbridge` · alternatives | Verify availability (npm, GitHub org, domain, USPTO/EUIPO) **before** the first public commit. Assume it's taken and prepare two fallbacks. |

## Important — decide during Phase 1

| # | Question | Notes |
|---|---|---|
| D7 | **Which non-conformant clients ship first?** | Claude Code is highest value (largest plugin ecosystem, absent from the standard). Then Gemini CLI, Zed, Windsurf, Continue. Pick by measured user overlap, not by guess. |
| D8 | **How aggressive is the "silent degradation" warning?** | Warn always, or only on `--strict`? Leaning: warn always, concise; it's our differentiator and it teaches the ecosystem. |
| D9 | **Telemetry default** | Recommendation: **opt-in only**, anonymous counts, published schema. Costs us data; buys the trust that the enterprise motion requires. |
| D10 | **Do we write client configs, or run as a proxy?** | Both eventually. Start with writing configs (works everywhere, zero runtime risk). Gateway is opt-in later. |
| D11 | **Namespacing authority** | The spec's `name` is unscoped and collision-prone. Do we impose `<scope>/<name>` in our lockfile? Leaning yes, mapped to source org — but must not break spec conformance. |
| D12 | **Skill risk scoring methodology** | Heuristics vs. LLM classifier vs. both. Needs a labeled corpus — start collecting from day one, even before the scanner exists. |

## Strategic — revisit quarterly

| # | Question |
|---|---|
| D13 | **Now partly answered.** `FUTURE_CONSIDERATIONS.md` in the spec repo records permissions, provenance, secret handling, **enterprise controls**, audit-trail standardization, dependency resolution and a conformance test suite as possible future work — explicitly uncommitted, but that is the TSC's stated thinking. Pre-commit to: **implement each the day it stabilizes**, and compete on products rather than formats. A spec body can standardize a policy file, an event schema, a signature envelope; it does not ship a fleet inventory, a scanner, a threat corpus, or an auditor-facing evidence pack, and it will not do so across competing clients. See [00 §5](00-research-agent-plugins.md). |
| D14 | If Anthropic adopts Agent Plugins, how much of the Claude Code adapter's value evaporates? (Some — but fleet inventory and scanning are unaffected. Don't over-index on the adapter as the moat.) |
| D15 | Do we ever ship first-party plugins? (Risk: competing with the ecosystem we serve. Recommendation: reference/demo only.) |
| D16 | Do we support non-coding agent platforms (support, ops, finance)? Larger TAM, different buyer, dilutes focus. Not before Phase 4. |
| D17 | At what point does not having a sandbox become an unacceptable liability we're publicly on the hook for? |

## Unknowns requiring external research

- Real per-client component support today — the `CompatibleClients` data on agent-plugins.org renders from a component and is not published as a machine-readable feed. **Must be measured empirically**; this is the first job of the conformance harness and the reason the matrix is a defensible asset.
- Whether the TSC intends to address signing/provenance in 1.1. Watch the public proposal repo.
- Actual plugin supply growth rate (how many plugins exist, published where, at what quality). Drives whether curation has value.
- Whether enterprises are already blocking these clients wholesale — if so, the sale is "unblock safely," which is a much better pitch than "govern what you allow."

## Things deliberately not decided yet

Pricing exactness, hosted-vs-self-host default, the gateway's protocol surface, and the marketplace question. All depend on Phase 1/2 evidence, and deciding them early only creates sunk-cost pressure.
