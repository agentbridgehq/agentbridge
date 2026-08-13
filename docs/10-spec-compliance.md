# 10 — Spec Compliance Matrix

Requirement-by-requirement audit against **Agent Plugins Specification v1.0.0**,
source of truth: [`agentplugins/agent-plugins-spec`](https://github.com/agentplugins/agent-plugins-spec),
`spec/1.0.0.md`, status **Published**, last pushed 2026-08-06.

Audited 2026-08-10 against commit-current `main`. Re-run this audit whenever the
spec repo changes; §10.1 guarantees that any schema change ships as a new
specification release, so a new `spec/<version>.md` is the signal.

**Schemas:** our embedded copies in [`internal/schema`](../internal/schema) are
**byte-identical** to `schemas/1.0.0/*.json` upstream. Verified by diff.

**Authority note.** §5.2 and §7.2.1 both state that the specification text
governs where it conflicts with the schema. That matters more than it sounds:
several requirements below are unexpressible in JSON Schema, and one — the
non-object `extensions` rule — is a case where following the published schema
literally makes a client **non-conformant**.

---

## 1. Summary

| | Count |
|---|---|
| Normative requirements audited | 47 |
| Implemented and tested | 41 |
| Not applicable (we are not a runtime) | 6 |
| Known deviations | 0 |

Ten gaps were found in this audit and all ten are now closed. They are listed in
§4 with the section each violated, because the pattern in them is instructive:
**every gap was a rule JSON Schema cannot express.** Schema validation alone
produces a plausible, confidently wrong client.

---

## 2. Requirement matrix

### §4 Plugin package model

| § | Requirement | Where | Status |
|---|---|---|---|
| 4.1.1 | Plugin is a directory at a single filesystem location | `safepath.NewRoot` | ✅ |
| 4.1.2 | Manifest required at `plugin.json` in the root | `agentplugins.Import` | ✅ |
| 4.1.3 | Filesystem-resolved paths MUST remain within the resolved root; symlinks may resolve inside, MUST reject outside | `safepath.Root.Resolve` | ✅ fuzzed |
| 4.1.4 | Plugin-relative path fields MUST begin with `./` and stay contained | `importer.CheckStdioCommand`, `CheckCwd` | ✅ |
| 4.1.5 | Non-path values (args, env) are opaque; MUST NOT be treated as package paths | no containment check applied to args/env | ✅ |
| 4.1 ladder | Narrowest applicable failure boundary (5 levels) | see §3 below | ✅ |

### §5 Manifest

| § | Requirement | Where | Status |
|---|---|---|---|
| 5.1 | Check for manifest at `plugin.json`; load and validate before component discovery | `agentplugins.Import` order | ✅ |
| 5.2 | Closed schema, 10 permitted top-level fields | embedded schema + `knownManifestFields` | ✅ |
| 5.2 | Unknown top-level field → report, ignore, continue; MUST NOT assign semantics | `CodeManifestUnknownField` | ✅ |
| 5.2 | Non-object `extensions` → report, ignore, continue (**non-fatal**) | `extensionMap`, `CodeManifestBadExtensions` | ✅ |
| 5.2 | Any other schema violation is fatal | `schema.ValidatePluginManifest` | ✅ |
| 5.2 | `$schema` MUST equal the canonical 1.0.0 identifier | schema `const` + pre-check | ✅ |
| 5.2 | Select rules from `$schema`; **MUST NOT retrieve a schema while loading** | `//go:embed`, no network in the load path | ✅ |
| 5.2 | Unsupported version → reject, SHOULD report the version | `CodeUnsupportedSpecVer` path | ✅ |
| 5.3 | `$schema` and `name` required; missing/wrong type/empty → reject, SHOULD say which | schema `required` + `minLength` | ✅ |
| 5.4 | Metadata validated by JSON type only | no semantic validation performed | ✅ |
| 5.4 | MUST NOT reject for non-SemVer `version`, non-URL `homepage`/`repository`/`author.url`, non-email `author.email`, non-SPDX `license` | deliberately unvalidated | ✅ |
| 5.5 | Name: 1–64 chars, `[a-z0-9.-]`, alphanumeric ends, no `--` or `..` | `schema.ValidateName` | ✅ |

### §6 Component discovery

| § | Requirement | Where | Status |
|---|---|---|---|
| 6.1 | Discover from fixed locations; manifest cannot override or inline | constants, no configurability | ✅ |
| 6.2 | Missing fixed location MUST NOT be an error | `DiscoverDirSkills`, `loadMCP` | ✅ |
| 6.2 | Present but wrong filesystem kind → component type invalid, others continue | `IsRegularFile`, `CodeSkillsNotDirectory`, `CodeMCPNotRegularFile` | ✅ |

### §7.1 Skills

| § | Requirement | Where | Status |
|---|---|---|---|
| 7.1 | Immediate child directories of `skills/` containing a `SKILL.md` **regular file** | `DiscoverDirSkills` | ✅ |
| 7.1 | MUST NOT recursively search deeper descendants | single-level scan | ✅ |
| 7.1 | Non-conforming skill → skip, continue, SHOULD report | `CodeSkillInvalidFrontmat` | ✅ |

### §7.2 MCP servers

| § | Requirement | Where | Status |
|---|---|---|---|
| 7.2.1 | Config only at `mcp.json`; never inline in `plugin.json` | `loadMCP` | ✅ |
| 7.2.1 | Object with required `$schema` and `mcpServers`, no other top-level fields; empty `mcpServers` valid | envelope schema | ✅ |
| 7.2.1 | Validate each server independently via `#/$defs/server` | per-entry validation | ✅ |
| 7.2.1 | Each server has `type` and matches exactly one closed variant | `oneOf` + per-transport schemas | ✅ |
| 7.2.1 | `command` is a single token: bare name **or** `./`-relative; no shell string | `CheckStdioCommand` | ✅ |
| 7.2.1 | **MUST NOT** expand placeholders in `command` | `Materialize` resolves, never expands | ✅ |
| 7.2.1 | Bare `command` resolved by platform search; MUST NOT depend on configured `PATH` | left to the OS; never injected | ✅ |
| 7.2.1 | Omitted `cwd` ⇒ plugin root | `Materialize` sets it explicitly | ✅ |
| 7.2.1 | `cwd` is `./`-relative, or exactly/prefixed `${PLUGIN_ROOT}`, or `${PLUGIN_DATA}` | `CheckCwd` | ✅ |
| 7.2.1 | `${PLUGIN_ROOT}`-rooted stays in root; `${PLUGIN_DATA}`-rooted stays in data dir | `CheckCwd` | ✅ |
| 7.2.1 | `url` absolute http/https, **no userinfo, no fragment** | `CheckServerURL` | ✅ |
| 7.2.1 | Non-loopback MUST use HTTPS; http allowed for `localhost`/loopback IP | `CheckServerURL` | ✅ |
| 7.2.1 | Header names/values valid; case-insensitive duplicates invalid | `CheckHeaders` | ✅ |
| 7.2.1 | **MUST NOT** expand in `url`, header names, or header values | `Materialize` passes remote entries through | ✅ |
| 7.2.1 | Support at least one of stdio / streamable-http; SHOULD support both; `sse` OPTIONAL | all three supported | ✅ |
| 7.2.1 | Use the declared transport for the initial attempt | transport carried verbatim into every adapter | ✅ |
| 7.2.2 | Invalid/unsupported/mismatched `mcp.json` → disable MCP, continue, SHOULD report | envelope boundary | ✅ |
| 7.2.2 | Invalid server entry → skip it, continue, SHOULD report | per-entry boundary | ✅ |
| 7.2.2 | Unsupported transport → skip that server only | per-entry boundary | ✅ |
| 7.2.2 | Connection failure → continue loading | n/a — we do not connect | ➖ |

### §8 Client extensions

| § | Requirement | Where | Status |
|---|---|---|---|
| 8.1 | `extensions` is an object of namespace → object | schema (strict), tolerant loader | ✅ |
| 8.1 | Ignore unimplemented namespaces **without validating their contents** | carried as opaque `json.RawMessage` | ✅ |
| 8.2 | File-based extensions live in a top-level namespace directory | preserved by the copy-tree install | ✅ |

### §9 Environment and expansion

| § | Requirement | Where | Status |
|---|---|---|---|
| 9.1 | Provide `PLUGIN_ROOT` and `PLUGIN_DATA` to every plugin subprocess | injected into written `env` | ✅ ¹ |
| 9.1 | Create `PLUGIN_DATA` before launch, writable, preserved across updates | `EnsurePluginData`, lives outside client dirs | ✅ ¹ |
| 9.1 | *(unspecified)* disposal on uninstall | `ReleasePluginData`: empty is removed, non-empty is kept **and reported** | ✅ |
| 9.1 | Configured `env` overlays the base; reserved names set **last** | `Materialize` sets them after user env | ✅ ¹ |
| 9.2 | Expand only the two placeholders, only in `args`, `env` values, `cwd` | `Materialize` | ✅ |
| 9.2 | Single, non-recursive replacement; introduced text not rescanned | one `strings.ReplaceAll` pass per field | ✅ |
| 9.2 | Unrecognized placeholder-like text stays literal | no other expansion performed | ✅ |
| 9.2 | `env` MUST NOT contain `PLUGIN_ROOT`/`PLUGIN_DATA`; such an entry is invalid | `CheckReservedEnv` | ✅ |

¹ We do not launch subprocesses; the target client does. Since no target
implements the Agent Plugins runtime contract, these obligations are discharged
*on the client's behalf* at write time. See §5.

### §10 Versioning

| § | Requirement | Where | Status |
|---|---|---|---|
| 10.1 | `mcp.json` version MUST match `plugin.json`; mismatch invalidates MCP only | `CodeVersionMismatch` | ✅ |
| 10.1 | Canonical identifiers never reassigned | embedded, never fetched | ✅ |
| 10.2 | SHOULD use SemVer; clients MAY use `version` for update checks | carried, not enforced | ✅ |

### §11 Client conformance

All eight minimum requirements in §11.1 are met. §11.2 (incremental adoption)
and §11.3 (failure isolation) are structural properties of the importer design:
a returned error means the plugin is rejected, an Error-severity diagnostic means
one component was, and the two are never conflated.

---

## 3. Failure boundary ladder (§4.1)

The spec requires the **narrowest applicable** boundary. Ours, in the same order:

| Failing path | Required boundary | Implementation |
|---|---|---|
| `plugin.json` outside root | reject plugin | `Import` returns an error |
| Fixed component location outside root | that component type invalid | `DiscoverDirSkills` / `loadMCP` return a diagnostic |
| Discovered `SKILL.md` outside root | skip that skill | `loadSkill` diagnostic |
| MCP `command` or `cwd` fails containment | that server entry invalid | `CheckStdioCommand` / `CheckCwd` |
| Any other package path outside root | deny access | `safepath` returns `ErrEscapes` |

---

## 4. Gaps found in this audit (all closed)

Every one was a rule JSON Schema cannot express. A client built on schema
validation alone would have shipped all ten.

| # | § | Gap | Consequence had it shipped |
|---|---|---|---|
| 1 | 5.2, 8.1 | Non-object `extensions` was **fatal** | Rejected plugins the spec says must load. The canonical schema's type constraint contradicts the normative text; following the schema made us non-conformant. |
| 2 | 5.2 | Unsupported `$schema` reported as an opaque const mismatch | Correct outcome, useless message |
| 3 | 6.2, 7.1 | No filesystem-kind checks | `skills` as a file or `mcp.json` as a directory produced a confusing read error instead of the specified component-type failure |
| 4 | 7.2.1 | `command` accepted absolute and `../` paths | **Arbitrary executable outside the plugin.** The spec's own example calls `../bin/server` invalid |
| 5 | 7.2.1, 9.2 | Placeholders expanded in `command` | Explicitly forbidden |
| 6 | 7.2.1 | Placeholders expanded in headers | Explicitly forbidden |
| 7 | 7.2.1 | `url` userinfo and fragment accepted | Credentials smuggled in a URL |
| 8 | 7.2.1 | Header validity and case-duplicates unchecked | Ambiguous header set silently sent |
| 9 | 9.2 | Reserved `env` names warned, not rejected | Server should be invalid |
| 10 | 9.1, 7.2.1 | `PLUGIN_ROOT`/`PLUGIN_DATA` not provided; default `cwd` not made explicit | Plugins relying on the runtime contract break in every non-conformant client — silently |

Gap 10 is the one worth dwelling on. The others are validation; this one is a
translation obligation. §9.1 requires a client to hand every plugin subprocess
`PLUGIN_ROOT` and `PLUGIN_DATA`, and §7.2.1 makes the plugin root the default
working directory. **No client we write configuration for does either**, because
none of them knows the plugin exists. So a portable plugin that follows the spec
correctly would fail in Cursor, VS Code, Codex and Gemini CLI — and fail
silently, since the config validates and the client starts. Keeping those
promises on the client's behalf is exactly the work a bridge exists to do.

---

## 5. Where we are deliberately not a conformant *client*

AgentBridge is not an agent client: it does not load skills into a model or
connect to MCP servers. It is a loader and a translator. Requirements that
attach to a running client are discharged as translation obligations instead:

| Spec requirement | Our discharge |
|---|---|
| Provide `PLUGIN_ROOT`/`PLUGIN_DATA` to subprocesses (§9.1) | Written into the `env` we hand the target client, set last per the required precedence |
| Create and preserve `PLUGIN_DATA` (§9.1) | Created at install under `~/.agentbridge/data/<plugin>` — outside every client's directory, so a client reinstall cannot destroy it |
| Default `cwd` to the plugin root (§7.2.1) | Written explicitly, since the target would otherwise use its own |
| Placeholder expansion (§9.2) | Performed at write time, in exactly the three permitted fields |
| Use the declared transport (§7.2.1) | Carried verbatim; a client that cannot represent it gets a reported loss, never a substitution |

This is also the strongest argument for the fidelity report existing. Wherever a
target cannot honor a spec obligation, the user is told which obligation and
why — rather than discovering it when the agent quietly fails to do something.

---

## 6. Watch list

Checked automatically every night by
[`internal/tools/specwatch`](../internal/tools/specwatch) (`make upstream`):
the embedded schemas against the canonical ones, the set of published
specification versions, and whether each adapter's cited vendor documentation
still resolves. Drift opens a single tracking issue rather than a new one each
morning.

Re-check by hand on every spec release:

- A new `spec/<version>.md` in the upstream repo. §10.1 guarantees both schemas
  are republished with it even when unchanged, so a schema `$id` bump is the
  reliable trigger.
- `FUTURE_CONSIDERATIONS.md` — non-normative, uncommitted, but it is where the
  TSC records what it is thinking about. Several items map directly onto our
  commercial surface; see [07 §D13](07-open-questions.md).
- The Agent Skills specification at `agentskills.io/specification`, which §7.1
  defers to entirely for `SKILL.md`. We currently parse frontmatter permissively
  and impose no schema, which is the right default while that spec is upstream
  of us — but it means we cannot yet report a skill as *non-conforming*, only as
  unparseable.
