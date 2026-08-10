# 04 — Product Scope & Roadmap

Phases are gated on **evidence**, not calendar. Do not start phase N+1 until phase N's exit criteria are met — the failure mode for this company is building the control plane before anyone has the CLI.

---

## Phase 0 — Foundations (weeks 0–3)

**Goal:** know exactly what we're building on and prove the hardest technical assumption.

- Full read of the Agent Plugins spec + Agent Skills spec + MCP spec; write conformance notes.
- Build the **compatibility harness skeleton** and run canonical test plugins against 3 clients. Publish nothing yet; just learn how divergent reality already is.
- Spike: import a Claude Code plugin → IR → export to Agent Plugins format, and back. This is the round-trip that the whole product depends on. If it's lossy in ways that can't be reported cleanly, rethink.
- Decide language, license, name/trademark availability, org structure.

**Exit:** a written fidelity report for one real plugin across 4 clients, and a go/no-go on the IR design.

---

## Phase 1 — The CLI (weeks 3–12) · *free, OSS, Apache-2.0*

**Goal:** be the default way a human installs an agent plugin.

Scope:
- `agentbridge install <source>` — git, local path, OCI ref.
- Detect installed clients; install into all or a selected subset.
- IR + importers (agent-plugins@1.0, Claude Code, bare mcp.json) + exporters (≥6 clients, ≥2 non-conformant).
- `agentbridge.yaml` + `agentbridge.lock`, `sync`, `list`, `remove`, `update`.
- **Fidelity report** on every install.
- `agentbridge doctor` — "why did nothing happen in client X."
- `agentbridge validate` / `lint` — spec conformance for authors, plus practical warnings the spec doesn't cover.
- Secret refs → OS keychain; hard-fail on plaintext secrets unless overridden.

Explicitly out: accounts, telemetry beyond opt-in anonymous counts, gateway, scanning.

**Exit criteria:** 2,000 weekly active installs · ≥15 third-party plugin READMEs recommending it · ≥5 external contributors · ≥1 adapter contributed by someone else.

---

## Phase 2 — Trust & visibility (months 3–7) · *free tier + first paid*

**Goal:** turn "installer" into "the thing that tells you what you have and whether it's safe."

- **Public compatibility matrix** — machine-readable, auto-updated, free. Marketing engine.
- **Local scanner**: skill prompt-injection heuristics, dangerous-capability detection, secret leakage, suspicious network egress in MCP configs, typosquat detection. SARIF output.
- **Signing & verification**: cosign/Sigstore verify on install; `--require-signed`; publish attestations for plugins we mirror.
- **Workspace (paid, team tier)**: opt-in inventory across a team's machines; drift detection; shared `agentbridge.yaml`; policy file enforced by the CLI.
- Import from Smithery + official MCP registry.

**Exit criteria:** 10k WAU · matrix cited by ≥3 external sources · 10 paying teams · ≥1 real vulnerability found and responsibly disclosed.

---

## Phase 3 — Control plane (months 7–15) · *enterprise*

**Goal:** be buyable by a platform/security org.

- Private registry + mirroring + curated internal catalog.
- Policy-as-code (CEL), org > project > user precedence, enforced at install and at runtime.
- SSO/SAML/OIDC, SCIM, RBAC, org-wide audit log.
- Compliance evidence export (SOC 2 / ISO 42001 / EU AI Act mapping) — the artifact that justifies the line item.
- Gateway GA: secret brokerage, per-tool authz, tool-call audit, redaction, cost attribution.
- Integrations: Datadog/Splunk export, Jira/ServiceNow approval workflows, Artifactory mirror.

**Exit criteria:** 25 paying orgs · ≥$1M ARR run-rate · ≥3 accounts >1,000 seats · net revenue retention >120%.

---

## Phase 4 — Platform (months 15+)

- Sandboxed execution tiers (container/microVM) for untrusted plugins.
- Marketplace of *verified* plugins (verification as the product, not the listing).
- Behavioral runtime detection — anomaly detection on tool-call patterns, informed by the corpus.
- Non-coding agents: support/ops/finance agent platforms adopting the same format.

---

## Cross-cutting workstreams (run continuously from Phase 1)

| Stream | Cadence | Why |
|---|---|---|
| Spec participation (TSC proposals, public comments) | ongoing | Early signal on registry/signing decisions; credibility; relationship with future acquirers |
| Compatibility harness maintenance | weekly | Client releases break things constantly |
| Security research + disclosure | monthly | The single highest-leverage marketing activity in this category |
| Adapter coverage | per client release | The moat's maintenance cost |
| Content: "what actually works where" | biweekly | Owns the search intent that matters |

## Sequencing rules

1. **Never ship a paid feature that makes the free CLI worse.** Adoption is the asset.
2. **The CLI must work with zero network calls to us.** The moment it doesn't, enterprises stop deploying it, and the fleet position dies.
3. **Do not build the gateway before the inventory.** Gateway is a crowded me-too on its own; inventory is what makes it ours.
4. **Publish the compatibility matrix before monetizing anything.** It buys the neutrality reputation the rest depends on.
