# 03 — Architecture

**Status.** Sections 1–5 and 11 are **built** — see the package table in the
[README](../README.md) and the conformance matrix in
[10](10-spec-compliance.md). Sections 6–10 are design intent for later phases.
Where the built IR differs from the sketch below, the code is authoritative;
this document records the reasoning, not the field list.

Open items are tracked in [07](07-open-questions.md).

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

As built (`internal/ir`), with a note on the fields the original sketch put here
and the implementation put elsewhere:

```
Plugin
  irVersion     schema version of this representation
  name          plugin name, as the manifest declares it
  version        description  author  homepage  repository  license  keywords
  origin        {dialect, schemaId, root, manifestPath}
  skills[]      {name, description, kind, dir, entrypoint, frontmatter, contentHash}
  mcpServers[]  {name, transport, command, args, env, cwd, url, headers, contentHash}
  extensions{}  namespace -> opaque blob (preserved, never validated)
  native{}      dialect-specific data with no portable home
  capabilities  {exec, network, filesystem, secrets, evidence[]}

  -- deliberately not on the Plugin --
  source        resolved reference        -> agentbridge.lock, and the receipt
  provenance    signature / attestation   -> release artifacts only, not plugins
  compat        per-client support        -> computed per install, as Fidelity
  riskFindings  scanner output            -> agentbridge.lock, and scan --json
```

All four are built; none of them belong on the Plugin. The IR describes *what a
plugin is*, and each of these describes a relationship between a plugin and
something else — where this copy came from, which client is being written to,
what a scan concluded on a particular day. Putting them here would have meant a
plugin's identity changing because it was installed somewhere new, and a
lockfile diff that could not distinguish a changed plugin from a changed
opinion about one.

The one genuine gap is provenance: our release binaries are signed, but a
plugin's provenance is still only the commit or digest recorded in the lock.

Three decisions in the built version that the original sketch got wrong, all
worth recording because they were only obvious once the importers existed:

- **`native` was missing.** `extensions` is a spec-defined field with
  spec-defined semantics; a Claude Code plugin's hooks and agents belong to
  neither it nor any portable field. Conflating the two would have meant either
  discarding them or writing non-spec data into a spec field.
- **Skill bodies are deliberately absent.** Capability inference runs at import
  time while the content is in hand, and only the hash is kept. Lockfiles stay
  small, and untrusted instruction text does not get copied through every layer.
- **`name` is not namespaced in the IR.** The IR mirrors what the manifest
  actually says. Scoping is a resolution concern, and putting it here would mean
  the IR could not represent a real plugin faithfully. See [D11](07-open-questions.md).

Key IR-only concepts the spec has no notion of:

- **Secret references** (M5, built). `env` values become
  `${secret:openai/api_key}` refs resolved at write time from OS keychain,
  gateway or vault. Note the spec is on our side here and more strongly than
  expected: §9.2 and §7.2.1 state that `env` values and headers are *visible
  package data* and that plugins MUST NOT embed secrets in them, and §7.2.1
  adds that v1 defines no portable credential-reference field at all. Today we
  write literals and report each one as a fidelity loss.
- **Capabilities.** Derived (static analysis) + declared. Drives policy: "no plugin with `exec` from an unsigned source."
- **Compat.** Per-client, per-version outcome of installing this exact plugin. Populated by the conformance harness (§7).
- **Risk findings.** Per-skill and per-server analysis results, carried with the artifact.

## 4. Adapters (exporters)

Each adapter answers three questions for a target client: *where do files go*, *what config does it write*, and *what does it lose*.

**This table's original prediction was exactly inverted, and the correction is
the most useful thing M2 produced.** It assumed conformant clients would take
plugins natively with low loss, and that Claude Code — the non-conformant one —
would be the lossy case. The opposite is true, for a reason no amount of
planning would have surfaced: *conformance is not the same as documentation*.

| Client | Conformant? | Mechanism | Actual fidelity |
|---|---|---|---|
| Claude Code | **no** | copy the package + write `.claude-plugin/plugin.json` and `.mcp.json` | **skills + MCP, full coverage** — the only client that takes a whole package, because it is the only one whose layout is documented |
| Cursor | yes | write `mcp.json` | MCP only; skills declined — package location undocumented |
| VS Code / Copilot | yes | write `mcp.json` (`servers`, type `http`) | MCP only; same reason |
| Codex | yes | managed block in `config.toml` | MCP only, no `sse`; same reason |
| Gemini CLI | no | write `settings.json` | MCP only; no skills mechanism exists at all |

The one client the standard does not cover carries the most, and three clients
that *do* implement the standard carry the least — because their vendors have
not published where a portable package goes. We will not guess a path and write
into a developer's machine on a hunch, so those adapters declare skills
`undocumented` and say so on every install. Resolving that is a measurement for
the conformance harness (M10-2), not a decision.

**Fidelity report** is a first-class output, printed by default rather than
behind a flag:

```
deploy-tools

  !! claude-code    user      skills 2/2     mcp 2/2
  !! cursor         user      skills 0/2     mcp 2/2
       - Cursor may support skills, but its vendor has not documented where they
         are installed; 2 skill(s) not installed. We will not write to an
         unverified path
       - env DEPLOY_TOKEN is written as plaintext into .../mcp.json
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
