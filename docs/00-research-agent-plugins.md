# 00 — Research: The Agent Plugins Standard (v1.0.0)

*Sources: the canonical repository [`agentplugins/agent-plugins-spec`](https://github.com/agentplugins/agent-plugins-spec)
(`spec/1.0.0.md`, `schemas/1.0.0/*.json`, `GOVERNANCE.md`, `FUTURE_CONSIDERATIONS.md`)
and the documentation site https://agent-plugins.org. Read 2026-08-10.*

*Spec status: **Published**, v1.0.0. Not a working draft — the normative text is
final for this version, and §10.1 requires any schema change to ship as a new
specification release. Our per-requirement audit is in [10 — Spec compliance](10-spec-compliance.md).*

**Authority rule worth knowing before reading anything else:** §5.2 and §7.2.1
both state that the **specification text governs** where it conflicts with the
published JSON Schema. This is not a formality. Several requirements cannot be
expressed in JSON Schema at all, and in one case — a non-object `extensions`
field — following the schema literally makes a client *non-conformant*.

## 1. What it is

Agent Plugins is an open, vendor-neutral **packaging format** for things that extend AI agents. It bundles two existing standards into one portable directory:

- **Agent Skills** (Anthropic's `SKILL.md` spec) — prompt-level capabilities.
- **MCP servers** (Model Context Protocol) — tool-level capabilities.

Announced 2026-08-06. Proposal initiated by Vercel; 1.0 shaped by Amazon, Anysphere (Cursor), GitHub, Microsoft, OpenAI, Vercel. Technical Steering Committee = core maintainers from Amazon, Cursor, Microsoft, OpenAI, Vercel. Launch clients cited in coverage: **ChatGPT, Codex, GitHub Copilot, VS Code, Cursor, Kiro (AWS)**.

The stated philosophy is a *"small interoperability floor"*: define the package shape, leave distribution, installation, permissions and UX to clients.

## 2. The normative surface (what you must implement)

### 2.1 Package shape

```
my-plugin/
  plugin.json            # REQUIRED — portable identity + metadata
  mcp.json               # OPTIONAL — MCP server configuration
  skills/                # OPTIONAL — each immediate child dir with SKILL.md is one skill
    my-skill/SKILL.md
  com.example.client/    # OPTIONAL — client-owned files, reverse-domain namespace
```

A plugin is *"a directory rooted at a single filesystem location."* Every resolved path MUST stay inside the filesystem-resolved plugin root. Component locations are **fixed** — `skills/` and `mcp.json` are not configurable.

### 2.2 `plugin.json`

- **Closed schema.** Only permitted top-level fields: `$schema`, `name`, `version`, `description`, `author`, `homepage`, `repository`, `license`, `keywords`, `extensions`.
- Required: `$schema` (must be `https://agent-plugins.org/schemas/1.0.0/plugin.schema.json`) and `name`.
- `name`: 1–64 chars, lowercase alphanumeric + `-` + `.`, must start/end alphanumeric, no consecutive `-` or `.`.
- `author` is an object: `{name, email, url}`.
- `extensions`: reverse-domain-keyed object of client-specific data. A client **MUST ignore** namespaces it does not implement, *without validating their contents*.

### 2.3 `mcp.json`

- Requires `$schema` at the **same spec version** as `plugin.json`, plus an `mcpServers` object.
- Each entry declares a `type` selecting one transport:
  - **stdio** — `command` (bare executable name, or `./`-relative path), optional `args`, `env`, `cwd`.
  - **Streamable HTTP** — `url` (absolute http/https), optional `headers`.
  - **HTTP+SSE** — legacy, same shape.
- Non-loopback endpoints MUST use HTTPS.
- Working directory defaults to plugin root. `cwd` may use `./`, `${PLUGIN_ROOT}`, `${PLUGIN_DATA}`.
- Placeholder expansion is limited to exactly those two variables, and only in `args`, `env` *values*, and `cwd`.
- stdio commands resolve as **single executable tokens** (no shell string parsing).

### 2.4 Skills

Discovery only. Agent Plugins says: skills are immediate child directories of `skills/` containing `SKILL.md`; the **Agent Skills spec is the source of truth** for frontmatter, `scripts/`, `references/`, `assets/`.

### 2.5 Versioning

- `plugin.json` and `mcp.json` MUST declare the same Agent Plugins version via `$schema`.
- Schema identifiers are immutable; breaking changes ship as new spec releases.
- Plugin `version` SHOULD be SemVer.

### 2.6 Failure boundary ladder (§4.1)

When a path fails containment, the client must apply the **narrowest applicable**
boundary. This ladder is the clearest statement in the spec of its whole
philosophy — a plugin should degrade, not die:

| Failing path | Boundary |
|---|---|
| `plugin.json` outside the root | reject the plugin |
| A fixed component location outside the root | that component type is invalid |
| A discovered `SKILL.md` outside the root | skip that skill |
| An MCP `command` or `cwd` failing containment | that server entry is invalid |
| Any other package path outside the root | deny access to that path |

### 2.7 Runtime contract (§9.1)

Easy to miss, and the part that matters most for a bridge. A client launching a
plugin subprocess MUST:

- provide `PLUGIN_ROOT` and `PLUGIN_DATA` in the environment;
- **create** `PLUGIN_DATA` before launch, make it writable, and **preserve it
  across plugin updates** (it MAY be deleted on uninstall);
- overlay the configured `env` on its chosen base environment, then set the two
  reserved names **last**, so they cannot be overridden.

A plugin may not depend on any other ambient variable, except the platform
executable search used to resolve a bare `command`.

### 2.8 Explicit anti-secret rules

The spec states twice, normatively, that the format is not a secret mechanism:

- §9.2 — "Configured `env` values are visible package data... Plugins MUST NOT
  embed credentials or other secrets in `env`."
- §7.2.1 — "Header values are visible package data, not a portable secret
  mechanism. Plugins MUST NOT embed credentials or other secrets in `headers`."

Also §7.2.1: a `url` MUST NOT contain user information. Together these mean
there is *no* portable way to give an MCP server a credential in v1.0.0 —
which is stated outright: "Agent Plugins v1 defines no OAuth configuration or
portable credential-reference fields." Every plugin needing a credential today
is therefore either non-conformant or relying on client-specific behavior. See
[05 §3](05-security-and-trust.md) and M5.

### 2.9 Conformance checklist (condensed)

| Area | Requirement |
|---|---|
| Loader | Load from a directory; enforce the package boundary; select rules from `$schema` **without fetching it** at load time |
| Validation | Enforce the closed schema; reject on missing/malformed required fields |
| Tolerance | Unknown top-level fields → report and continue. Unimplemented extension namespaces → non-fatal |
| Isolation | An invalid single component (e.g. one bad MCP entry) MUST NOT reject the whole plugin |
| Coverage | Support **at least one** of skills or MCP servers |
| MCP | If supported, at least one of stdio / Streamable HTTP; use the declared transport on first attempt; provide `PLUGIN_ROOT` and `PLUGIN_DATA`; enforce cwd containment; keep loading when one server fails |
| Versioning | Require matching spec versions across manifest files |

## 3. What the spec deliberately does **not** define

This is the entire commercial opportunity. Confirmed absent from v1.0.0:

| Missing layer | Consequence in the real world |
|---|---|
| **Distribution / registry / marketplace** | No canonical way to publish, name, discover or fetch a plugin. Everyone will `git clone`. |
| **Install & update lifecycle** | No lockfile, no integrity hash, no upgrade path, no rollback, no "what version is on this machine?" |
| **Identity, signing, provenance** | Nothing proves who authored a plugin or that bytes are unmodified. |
| **Permissions & consent model** | Explicitly left "to clients." A plugin's stdio server is arbitrary local code execution with `env` you supply. |
| **Secrets handling** | `env` values are plaintext in a JSON file that people will commit to Git. |
| **Cross-client resolution** | Nothing tells you which of your 5 agent clients has which plugin, at what version, with what config. |
| **Observability / audit** | No tool-call log, no cost attribution, no "which skill caused this action". |
| **Skill safety** | A `SKILL.md` is untrusted natural-language instructions loaded into an agent's context. No scanning, no policy, no classification. |
| **Dependency semantics** | No plugin→plugin deps, no capability requirements, no compat matrix vs. client versions. |
| **Non-conformant clients** | Claude Code, Gemini CLI, Zed, Windsurf, Continue, JetBrains AI etc. are not launch clients. Their formats need translation. |

## 4. Notable strategic observations

1. **Anthropic is conspicuously absent from the TSC** despite owning both underlying specs (Agent Skills, MCP). Claude Code is the most plugin-dense agent client in the market and has its own plugin/marketplace format. That fracture — the standard's largest missing client — is a durable reason for a neutral bridge to exist.
2. **The `extensions` + `com.example.client/` escape hatch guarantees divergence.** The spec makes it *cheap* for each client to add proprietary behavior and *mandatory* for others to ignore it. In 12 months a "portable" plugin will carry 4 vendor namespaces, and "portable" will mean "loads, but does less."
3. **The floor is deliberately low.** "Support at least one of skills or MCP servers" means a conformant client may support neither of the things a given plugin ships. Real-world compatibility is a matrix, not a boolean — and nobody owns publishing that matrix.
4. **`$schema` must not be fetched at load time.** Clients pin locally-known rules. So spec upgrades roll out at client-release speed, unevenly. Version skew across a developer's machine is guaranteed.
5. **The spec is 4 days old.** Tooling, conventions and defaults are unset. This is the shortest window this opportunity will ever have.

## 5. What the TSC says it may do next

`FUTURE_CONSIDERATIONS.md` in the spec repo is non-normative and explicitly
uncommitted — "none of these items is required for conformance or committed for
inclusion in a future release" — but it is the clearest available signal of
direction, and several items land directly on our commercial surface:

| Recorded consideration | Overlap with us |
|---|---|
| **Permission and approval UX** — manifest permission declarations, client-enforced capability restrictions, graduated trust levels | Our capability inference becomes a *declaration* to verify against, which is strictly better |
| **Provenance verification** — signatures, attestation chains, publisher trust policies | Directly our M8/Phase 2. A standard here helps us; we should implement it early and loudly |
| **Secret handling** — a `secrets` field, client-mediated injection, scoping, rotation | Overlaps M5. Worth tracking closely so our secret-reference syntax can converge rather than compete |
| **Enterprise controls** — allow/blocklists, org-scoped registries with approval workflows, centralized overrides, compliance reporting | **The most direct overlap with the commercial thesis.** See below |
| **Audit-trail standardization** — event schema for install/enable/update/uninstall, SIEM forwarding | Helps us: a standard event schema makes our audit export portable |
| **Dependency resolution** — a `dependencies` field with version constraints | Affects the lockfile design in M4 |
| **Plugin testing and validation** — a standard linter, conformance test suites | Overlaps our conformance harness. A published suite would be a gift, not a threat |

The enterprise-controls item deserves a clear-eyed read rather than alarm. A spec
body can standardize a *format* — a policy file, an event schema, a signature
envelope. It does not ship a fleet inventory, a scanner, a threat corpus, or an
auditor-facing evidence pack, and it certainly does not do so across competing
clients. The strategic response is therefore the opposite of defensive:
**implement each of these the day it stabilizes**, and compete on the parts that
are products rather than formats. Tracked as [D13](07-open-questions.md).

## 6. Open questions to track upstream

- Will the TSC take on a registry, or explicitly disclaim it? Notably,
  `FUTURE_CONSIDERATIONS.md` mentions "organization-scoped plugin registries"
  but no public one.
- Provenance is on the list but uncommitted. Does it land in 1.1, or get
  delegated to registries?
- Does Anthropic adopt, ignore, or fork?
- Does the `CompatibleClients` matrix on agent-plugins.org become authoritative
  and machine-readable? (Today it renders from a component; there is no
  published feed — which is why the harness must measure it.)
- Governance: the TSC is defined by a Technical Charter with core maintainers
  from Amazon, Cursor, Microsoft, OpenAI and Vercel, with public proposals and
  open participation. That is a real avenue for us, and cheap to use.
