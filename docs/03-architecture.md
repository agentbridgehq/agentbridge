# 03 — Architecture

Design notes only. No implementation decisions are final; open items are tracked in [07](07-open-questions.md).

## 1. Design principles

1. **The spec is an input dialect, not the core model.** Everything normalizes into an internal representation (IR). Agent Plugins 1.0, Claude Code plugins, raw `mcp.json` fragments, and whatever ships in 1.1 are all *importers* into the same IR. This is the single most important decision in the system — it is what survives spec churn.
2. **Local-first, offline-capable.** The CLI must work with no account and no network beyond fetching artifacts. Cloud is additive.
3. **Never be in the hot path unless asked.** A dev's agent must not break because our service is down. Gateway mode is opt-in; install mode writes files and gets out of the way.
4. **Everything is content-addressed.** Plugins are identified by digest, not by name+version alone. Names are a convenience; digests are the truth.
5. **Adapters are lossy and must say so.** Every install into a client emits a fidelity report. Silent degradation is the ecosystem's default failure mode; refusing to be silent is a feature.

## 2. System overview

```
┌───────────────────────────────────────────────────────────────────┐
│  SOURCES                                                          │
│  git · OCI registry · official MCP registry · Smithery · local dir │
└───────────────┬───────────────────────────────────────────────────┘
                │  fetch + verify (digest, signature, provenance)
                ▼
┌───────────────────────────────────────────────────────────────────┐
│  BRIDGE CORE  (Go/Rust single binary, embeddable library)         │
│                                                                   │
│  Importers ──► Internal Representation (IR) ──► Exporters          │
│   • agent-plugins@1.0      • plugin identity     • agent-plugins   │
│   • claude-code plugin     • skills[]            • claude-code     │
│   • bare mcp.json          • mcpServers[]        • vscode/copilot  │
│   • cursor config          • extensions{}        • cursor          │
│                            • capabilities        • zed/windsurf/…  │
│                            • provenance                            │
│                                                                   │
│  Resolver · Lockfile · Policy engine · Fidelity reporter · doctor  │
└───────┬───────────────────────────────┬───────────────────────────┘
        │ writes client config           │ optional
        ▼                                ▼
┌────────────────────┐        ┌──────────────────────────────────────┐
│  AGENT CLIENTS     │        │  GATEWAY (local or hosted)           │
│  Copilot · Cursor  │◄──MCP──┤  secret broker · per-tool authz      │
│  Codex · ChatGPT   │        │  rate limit · audit log · redaction  │
│  Claude Code · Zed │        └───────────────┬──────────────────────┘
└────────────────────┘                        │
        │ inventory + events (opt-in)          │ events
        ▼                                      ▼
┌───────────────────────────────────────────────────────────────────┐
│  CONTROL PLANE (commercial)                                        │
│  private registry · curation · scanning · policy-as-code · SSO/SCIM│
│  fleet inventory · drift detection · audit & compliance export     │
└───────────────────────────────────────────────────────────────────┘
```

## 3. Internal Representation (IR)

The IR is a superset of every dialect, with explicit provenance for each field.

```
Plugin
  id            digest (sha256 of canonical archive)
  name          namespaced: <scope>/<name>
  version       semver
  source        {kind: git|oci|registry|local, uri, ref, resolvedAt}
  provenance    {signer, attestation, buildRef, verified: bool}
  skills[]      {name, path, frontmatter, contentHash, riskFindings[]}
  mcpServers[]  {name, transport, command|url, args, env(refs), cwd, contentHash}
  extensions{}  namespace -> opaque blob (preserved, never validated)
  capabilities  declared: filesystem, network, exec, secrets
  compat        {clientId -> {supported, degraded, reasons[]}}
```

Key IR-only concepts the spec has no notion of:

- **Secret references.** `env` values are never literals in our model. They are `${secret:openai/api_key}` refs resolved at write time from OS keychain / gateway / vault. Writing a literal secret into a client config file requires an explicit `--allow-plaintext-secrets`.
- **Capabilities.** Derived (static analysis) + declared. Drives policy: "no plugin with `exec` from an unsigned source."
- **Compat.** Per-client, per-version outcome of installing this exact plugin. Populated by the conformance harness (§7).
- **Risk findings.** Per-skill and per-server analysis results, carried with the artifact.

## 4. Adapters (exporters)

Each adapter answers three questions for a target client: *where do files go*, *what config does it write*, and *what does it lose*.

| Client | Conformant? | Mechanism | Expected fidelity loss |
|---|---|---|---|
| VS Code / Copilot | yes | native plugin dir | low |
| Cursor | yes | native plugin dir | low; may ignore foreign `extensions` |
| Codex / ChatGPT | yes | native | varies by component support |
| Kiro | yes | native | low |
| Claude Code | no | translate to its plugin/marketplace + `.mcp.json` | `extensions` dropped; frontmatter mapping |
| Gemini CLI, Zed, Windsurf, Continue, JetBrains | no | write MCP config; skills → client-specific memory/rules file or unsupported | skills often unsupported → **must be reported, not silently dropped** |

**Fidelity report** is a first-class output:

```
$ agentbridge install acme/db-tools
  ✔ cursor        skills 3/3   mcp 2/2
  ✔ vscode        skills 3/3   mcp 2/2
  ▲ claude-code   skills 3/3   mcp 2/2   (1 extension namespace dropped)
  ▲ zed           skills 0/3   mcp 2/2   (client has no skills support)
```

Round-trip property to test: `import(export(p)) ≡ p` modulo documented, enumerated loss.

## 5. Resolution & lockfile

- `agentbridge.yaml` — declared intent (what this repo/team wants).
- `agentbridge.lock` — resolved digests, source refs, signature status, per-client install plan.
- Lockfile is the unit of reproducibility and the unit of review. A PR that changes agent capabilities becomes a reviewable diff — which is the story that sells to security teams.
- Scopes: **project** (repo-local), **user** (machine), **org** (pushed by control plane). Precedence: org policy > project > user, with org able to pin, forbid, or require.

## 6. Gateway (optional runtime)

Same binary, `agentbridge gateway`. Presented to clients as one MCP server that aggregates the plugin's servers.

Responsibilities:
- Spawn/supervise stdio servers; proxy Streamable HTTP/SSE.
- Inject secrets at call time; keep them out of client config files.
- Per-tool allow/deny, argument-level policy, egress restrictions.
- Tool-call audit with request/response redaction.
- Cost and latency attribution per plugin/tool/user.

Deployment modes: **local** (default, dev machine), **team** (shared, self-hosted), **hosted** (ours). Same config, three placements.

Design constraint: adding the gateway must not change which tools the agent sees, except where policy deliberately removes one — and removal must surface a visible reason, not a silent absence.

## 7. Conformance & compatibility harness

A matrix runner: N client versions × M canonical test plugins, in containers/VMs, asserting each conformance rule from the spec plus real-world behaviors.

Outputs:
- Public, machine-readable compatibility matrix (JSON + web page). Free forever; reputational asset.
- The `compat` block in the IR.
- Regression alerts when a client update breaks something — a genuinely useful notification we can send to the entire ecosystem, including the vendors.

This component has outsized strategic value relative to its cost. Build it early.

## 8. Sandboxing (research track)

The default posture — stdio server = arbitrary local exec — is indefensible long-term. Direction:
- Tier 1: run declared-safe servers directly (today's behavior, explicit).
- Tier 2: run in a container/microVM with declared filesystem + network scope.
- Tier 3: remote execution in our hosted runtime.

Do not build Tier 2/3 before there is demand; do design the IR `capabilities` field so they are addable without a format break.

## 9. Control plane

Multi-tenant service. Deliberately *not* required for the CLI to function.

- Private registry (mirror + curate public sources; host internal plugins).
- Scanning pipeline (see [05](05-security-and-trust.md)).
- Policy-as-code, versioned, enforced by the CLI at install and by the gateway at call time.
- Fleet inventory + drift detection ("32 machines run `acme/db-tools@1.2.0`; 4 run an unpinned fork").
- SSO/SCIM, RBAC, audit export, compliance evidence packs.
- API-first; the web UI is a client of the same API.

## 10. Tech choices — leaning

| Decision | Lean | Why |
|---|---|---|
| CLI language | Go | Single static binary, trivial cross-compile, strong container/OCI ecosystem, easy to hire. Rust if sandboxing becomes core. |
| Artifact transport | OCI registries | Digest addressing, signing (Sigstore/cosign), mirroring, auth, and every enterprise already runs one. Avoids building a registry. |
| Signing | Sigstore / cosign + in-toto attestations | Keyless, established, no key-management burden on authors. |
| Control plane | TypeScript or Go + Postgres | Boring and hireable. |
| Policy language | CEL (or Rego) | Embeddable, auditable, no custom DSL. |
| License split | Apache-2.0 core, source-available control plane | See [06](06-business-model-and-acquisition.md) |

## 11. Interfaces worth designing before code

- `agentbridge.yaml` / `.lock` schema (public, stable, versioned).
- Adapter plugin interface — so third parties add client support without touching core.
- Scan-finding schema (align to SARIF so it drops into existing security tooling).
- Inventory event schema (what the CLI reports; must be defensibly minimal — see privacy in [05](05-security-and-trust.md)).
