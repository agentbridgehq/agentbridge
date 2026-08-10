# 00 — Research: The Agent Plugins Standard (v1.0.0)

*Source: https://agent-plugins.org — spec, plugin-author guides, client-implementer guides, conformance checklist. Read 2026-08-10. Spec status: Working Draft, v1.0.0.*

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

### 2.6 Conformance checklist (condensed)

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

## 5. Open questions to track upstream

- Will the TSC take on a registry, or explicitly disclaim it? (Watch the public proposal repo.)
- Will signing/provenance land in 1.1, or be delegated to registries?
- Does Anthropic adopt, ignore, or fork?
- Does the `CompatibleClients` matrix on agent-plugins.org become authoritative and machine-readable? (Today it renders from a component; there is no published feed.)
