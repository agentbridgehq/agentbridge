# MVP Scope & Status

**Living tracker.** Update the Status column as work lands. Everything here is Phase 1 from [docs/04-roadmap.md](docs/04-roadmap.md).

Last updated: 2026-08-10 · Overall status: **M0, M1 and M2 complete, audited against the canonical spec. M3 (sources and fetch) is next.**

**Spec conformance audit (2026-08-10).** Implementation and docs were checked
requirement-by-requirement against the canonical
[`agentplugins/agent-plugins-spec`](https://github.com/agentplugins/agent-plugins-spec)
`spec/1.0.0.md`. Our embedded schemas are byte-identical to upstream. Ten gaps
were found and closed; full matrix in
[docs/10-spec-compliance.md](docs/10-spec-compliance.md). The pattern is worth
carrying forward: **every gap was a rule JSON Schema cannot express**, and one —
a non-object `extensions` field — was a case where obeying the published schema
literally made us *non-conformant*, because the normative text says to report
and ignore it. §5.2 and §7.2.1 both state the spec text governs over the schema.

---

## 1. MVP in one sentence

> One command installs an agent plugin into **every** agent client on the machine — including the ones the standard doesn't cover — reproducibly, with an honest report of what each client actually got, and no plaintext secrets on disk.

Nothing else. No accounts, no server, no scanning, no gateway, no UI.

## 2. Why this is the right MVP

- It is useful to a single developer on day one, with no account and no network calls to us. That is what makes it spread.
- It forces us to build the IR and the adapters — the two hardest, most durable pieces — before anything else.
- The **fidelity report** is the differentiator and it falls out of the architecture for free. Silent degradation is the ecosystem's default failure mode; being the tool that refuses to be silent is the whole positioning.
- It produces the artifact everything downstream needs: a lockfile describing exactly what is installed where.

## 3. Status legend

| | Meaning |
|---|---|
| ⬜ | Not started |
| 🟨 | In progress |
| ✅ | Done |
| ⏸️ | Blocked |
| ❌ | Cut from MVP |

Priority: **P0** = MVP does not ship without it · **P1** = ships if time allows · **P2** = deliberately deferred

---

## 4. Blocking decisions (must close before M1)

| ID | Decision | Ref | Status |
|---|---|---|---|
| D-01 | Planning docs written | [docs/](docs/) | ✅ |
| D-02 | Name + trademark availability verified (npm, GitHub org, domain, USPTO/EUIPO) | [07 D6](docs/07-open-questions.md) | ⬜ |
| D-03 | Language confirmed: Go | [08 §1](docs/08-tech-stack.md) | ✅ |
| D-04 | License confirmed: Apache-2.0 core, separate commercial repo | [07 D3](docs/07-open-questions.md) | ✅ |
| D-05 | IR design reviewed and signed off | [03 §3](docs/03-architecture.md) | ✅ |
| D-06 | Target client list for MVP locked | §6 below | ✅ |

---

## 5. Work items

### M0 — Foundations · P0 · ~1 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M0-1 | Repo, module layout, Go toolchain | `go build ./...` green; `cmd/` + `internal/` split; core importable as a library | ✅ |
| M0-2 | CI: build, test, vet, lint, cross-compile matrix | darwin/linux/windows × amd64/arm64 all build on every PR | ✅ |
| M0-3 | License scanning in CI, fail on AGPL/SSPL | Build fails on a policy-violating dependency | ✅ |
| M0-4 | DCO enforcement on PRs | First external PR cannot merge unsigned | ✅ |
| M0-5 | `SECURITY.md`, threat model summary, `security.txt` | Published before first public release | ✅ ¹ |

¹ `SECURITY.md`, the threat model summary and `CONTRIBUTING.md` are in place.
`security.txt` is deferred: it is served from a domain, and the domain is
blocked on D-02.

### M1 — Internal Representation & importers · P0 · ~2 weeks

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M1-1 | IR types + canonical serialization | Stable digest for identical input; round-trip test | ✅ |
| M1-2 | Embedded Agent Plugins 1.0 JSON Schemas | Validation works fully offline; `$schema` never fetched at load | ✅ |
| M1-3 | Importer: `agent-plugins@1.0` | Closed-schema validation; unknown top-level fields reported and ignored; foreign `extensions` namespaces preserved unvalidated | ✅ |
| M1-4 | Importer: Claude Code plugin format | Real-world plugin imports with an enumerated, documented loss list | ✅ |
| M1-5 | Importer: bare `mcp.json` fragment | Common case — someone pastes a server config | ✅ |
| M1-6 | Skill parsing (`SKILL.md` frontmatter + body hash) | Per-skill content hash; malformed skill skipped, not fatal | ✅ |
| M1-7 | Path-containment enforcement | Symlink/`..`/absolute-path escape attempts rejected with a clear error; fuzz-tested | ✅ |
| M1-8 | Capability inference (exec / network / fs / secrets) | Derived from `mcp.json` + skill content; recorded in IR | ✅ |

**Result of the M1-4 spike: GO.** The round trip works and the IR design holds.
Losses are real but all enumerable and reportable, which is the bar that
mattered. Findings:

- `skills/<name>/SKILL.md` is byte-identical across both formats. This is the
  one component that crosses with no transformation at all.
- Claude Code expands `${CLAUDE_PLUGIN_ROOT}` inside an MCP server's `command`;
  Agent Plugins expands placeholders only in `args`, `env` values and `cwd`.
  A converter that merely renamed the placeholder would emit a manifest that
  passes schema validation and fails at launch. We rewrite it to a
  plugin-relative command and report the rewrite.
- Claude Code MCP entries need no `type`. Transport is inferred from shape and
  the inference is reported, since guessing wrong changes how the server
  connects. `http` maps to `streamable-http`; `ws` has no portable equivalent
  and is skipped with a reason.
- `commands/*.md` flat skills have no portable layout and need restructuring
  before export.
- Components with no equivalent at all — agents, hooks, workflows, output
  styles, themes, monitors, LSP servers, bundled executables, plugin settings —
  are preserved (manifest under a reverse-domain extension namespace, on-disk
  components in `Native`) and each produces a diagnostic.

Two upstream surprises worth recording, both handled in `internal/schema`:

- The canonical `name` pattern uses a negative lookahead, which Go's RE2 engine
  cannot compile *at schema-compile time*. Left in place it takes the whole
  loader down. The rule is enforced in code by `schema.ValidateName` instead.
- The canonical manifest schema closes the object (`additionalProperties:
  false`), but the conformance rules require unknown top-level fields to be
  reported and tolerated. Both cannot be satisfied by schema validation alone,
  so the loader uses a relaxed variant and reports unknown keys itself, while
  strict author-facing validation keeps the closed-object constraint.

### M2 — Exporters (adapters) · P0 · ~3 weeks

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M2-1 | Adapter interface + registry | Third party can add a client without touching core | ✅ |
| M2-2 | Client detection (which agents are installed on this machine) | Correct on macOS/Linux/Windows; no false positives | ✅ |
| M2-3 | **Formatting-preserving config editing** (JSONC/JSON/TOML/YAML) | User comments, key order and indentation survive a write; golden-file tested | ✅ |
| M2-4 | Adapter: Cursor | Install + remove + idempotent re-install | ✅ |
| M2-5 | Adapter: VS Code / Copilot | As above | ✅ |
| M2-6 | Adapter: Codex | As above | ✅ |
| M2-7 | Adapter: Claude Code (non-conformant) | Skills + MCP land correctly; dropped `extensions` reported | ✅ |
| M2-8 | Adapter: one more non-conformant client (Zed / Windsurf / Gemini CLI) | Chosen by measured user overlap, not guess | ✅ ² |
| M2-9 | Dry-run mode (`--dry-run`) showing exact file diffs | No writes; diff is reviewable | ✅ |
| M2-10 | Clean uninstall | Removes only what we added; user's other config untouched | ✅ |

² Gemini CLI was chosen on documentation quality, **not** on measured user
overlap — we have no telemetry and deliberately none is planned (see [D9](docs/07-open-questions.md)). The
acceptance criterion is therefore only half met, and the choice should be
revisited once the conformance harness gives real data. Zed and Windsurf remain
unimplemented.

**M2-3 was, as predicted, the item that mattered most.** It is built on
`github.com/tailscale/hujson`, which parses JSONC into a syntax tree that
preserves comments and whitespace exactly. Two properties are asserted by test:
installing then removing a plugin leaves a config **byte-identical** to how it
started, and an inserted entry adopts the file's own indentation style
(including tabs) rather than ours. Codex is TOML, where no comment-preserving
editor was worth the dependency, so that adapter owns a marker-delimited block
and leaves every byte outside it untouched.

**The honest gap.** Cursor, VS Code and Codex are Agent Plugins launch clients,
but none of their vendors documents where a portable plugin package is
installed. Rather than guess a path and write into a developer's machine on a
hunch, those adapters declare skills `undocumented`, install MCP servers
normally, and say plainly in every fidelity report why skills were not carried.
Closing that gap is M10-2's job — it is a measurement, not a guess. Claude Code
does document its layout, so it takes the whole package and reaches full skill
coverage, which is exactly the asymmetry the strategy predicted.

**Two translation hazards found building the adapters**, both of which fail
silently — the config validates, the client starts, the server never appears:

- VS Code is the odd one out twice: the container key is `servers`, not
  `mcpServers`, and a streamable-HTTP server's type is spelled `http`.
- Nothing expands `${PLUGIN_ROOT}` or `${PLUGIN_DATA}` on our behalf, and a
  plugin-relative `./bin/server` means nothing in a config file living
  elsewhere. Both are resolved to absolute paths at write time. This is the
  mirror image of the placeholder problem found importing from Claude Code, and
  the Claude Code adapter reverses it exactly — closing the round trip.

**Uninstall (M2-10)** is driven entirely by receipts recorded at install time,
never by pattern-matching the config. A user entry that happens to share our
`<plugin>.<server>` naming is provably untouched; there is a test for exactly
that case.

### M3 — Sources & fetch · P0 · ~1 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M3-1 | Local directory source | | ⬜ |
| M3-2 | Git source, pinned to resolved commit SHA | Tag/branch resolves to an immutable SHA in the lock | ⬜ |
| M3-3 | Digest computation + verification | Content-addressed; tamper detected on re-fetch | ⬜ |
| M3-4 | Local content cache | Offline re-install works | ⬜ |
| M3-5 | OCI source | **P1** — defer if it costs schedule | ⬜ |

### M4 — Lockfile & resolution · P0 · ~1.5 weeks

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M4-1 | `agentbridge.yaml` schema (declared intent) | Documented and versioned | ⬜ |
| M4-2 | `agentbridge.lock` schema | Digests, source refs, per-client install plan; human-reviewable diff | ⬜ |
| M4-3 | Scopes: project vs. user, with precedence | Documented, tested | ⬜ |
| M4-4 | `sync` — make machine match lockfile | Idempotent; converges from any starting state | ⬜ |
| M4-5 | `update` — re-resolve and rewrite lock | Shows what changed before writing | ⬜ |

### M5 — Secrets · P0 · ~1 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M5-1 | Secret reference syntax (`${secret:...}`) in IR | Never a literal in our model | ⬜ |
| M5-2 | OS keychain backend (macOS / Windows / libsecret) | Read + write + delete | ⬜ |
| M5-3 | Refuse plaintext secrets by default | Requires explicit `--allow-plaintext-secrets`; warning is loud | ⬜ |
| M5-4 | Detect existing plaintext secrets in client configs and offer migration | Read-only detection; migration is opt-in | ⬜ |

### M6 — Commands · P0 · ~1.5 weeks

| ID | Command | Acceptance criteria | Status |
|---|---|---|---|
| M6-1 | `install <source>` | Installs to all detected clients or `--client` subset | ⬜ |
| M6-2 | `list` | What's installed, where, at what version | ⬜ |
| M6-3 | `remove` | Clean removal across clients | ⬜ |
| M6-4 | `sync` / `update` | Per M4 | ⬜ |
| M6-5 | `validate` | Spec conformance for plugin authors + practical warnings the spec doesn't cover | ⬜ |
| M6-6 | `doctor` | Explains why a plugin did nothing in client X — the ecosystem's most common question | ⬜ |
| M6-7 | `--json` output on every command | Scriptable from day one | ⬜ |

### M7 — Fidelity reporting · P0 · ~0.5 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M7-1 | Per-client fidelity report on install | Shows skills n/m, mcp n/m, and every dropped element with a reason | ⬜ |
| M7-2 | Documented, enumerated loss list per adapter | No silent drops anywhere; each has a stable reason code | ⬜ |

### M8 — Distribution · P0 · ~1 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M8-1 | GoReleaser pipeline, signed releases | Cosign signature + checksums on every artifact | ⬜ |
| M8-2 | SLSA provenance for our own binary | We cannot sell provenance while shipping unsigned | ⬜ |
| M8-3 | Homebrew tap | | ⬜ |
| M8-4 | npm wrapper package (downloads + verifies the binary) | `npm i -g` works without a Node runtime dependency at execution | ⬜ |
| M8-5 | Install script with signature verification | `curl \| sh` verifies before executing | ⬜ |
| M8-6 | Scoop / winget | **P1** | ⬜ |

### M9 — Docs & launch · P0 · ~0.5 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M9-1 | README, quickstart, per-client compatibility notes | | ⬜ |
| M9-2 | Plugin author guide (`validate` workflow) | | ⬜ |
| M9-3 | Public threat model + telemetry statement | Telemetry is opt-in; schema published field-by-field | ⬜ |
| M9-4 | Launch: HN / relevant communities / plugin-author outreach | Target ≥15 third-party READMEs recommending us | ⬜ |

### M10 — Conformance harness seed · P1 · ~1 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M10-1 | Canonical test plugins (valid, partially-invalid, edge cases) | Covers each conformance rule in the spec | ⬜ |
| M10-2 | Manual matrix run across MVP target clients | Produces the honest per-client support table for M9-1 | ⬜ |
| M10-3 | Automated nightly runs | **P2** — Phase 2 | ⬜ |

Enough of M10 must exist to make the fidelity reports factually correct. Full automation is Phase 2.

---

## 6. MVP target clients

| Client | Conformant | Priority | Status |
|---|---|---|---|
| Cursor | yes | P0 | ⬜ |
| VS Code / Copilot | yes | P0 | ⬜ |
| Codex | yes | P0 | ⬜ |
| Claude Code | **no** | P0 — highest strategic value | ⬜ |
| One of Zed / Windsurf / Gemini CLI | no | P0 | ⬜ |
| ChatGPT, Kiro | yes | P1 | ⬜ |

Claude Code is P0 despite being harder: it has the densest plugin ecosystem and is absent from the standard, which is precisely the gap that justifies a bridge existing.

---

## 7. Explicitly out of MVP

| Deferred | Phase |
|---|---|
| Accounts, workspaces, any server | 2 |
| Fleet inventory, drift | 2 |
| Scanning, risk scoring, signature *policy* | 2 |
| Public compatibility matrix website | 2 |
| Web UI of any kind | 2 |
| Gateway / runtime interception | 3 |
| Policy-as-code enforcement | 3 |
| Sandboxing | 4 |
| Plugin dependencies, plugin→plugin composition | later |
| Authoring/scaffolding tools (`agentbridge new`) | later |

Signature *verification* on fetch is P1 in MVP; signature *policy* (`--require-signed` as an org rule) is Phase 2.

---

## 8. Definition of done

The MVP ships when all P0 items are ✅ **and** these hold end to end:

- [ ] A developer installs the binary and, in one command, gets a real plugin working across ≥4 clients on their machine.
- [ ] Zero network calls to any AgentBridge-operated service during normal operation.
- [ ] Every install produces a fidelity report; no component is ever dropped silently.
- [ ] Re-running `sync` on a second machine from the same lockfile produces byte-identical client configs.
- [ ] No plaintext secret is written to disk without an explicit flag.
- [ ] Uninstall leaves the user's hand-written config untouched, comments and all.
- [ ] Our own release binaries are signed with published provenance.

## 9. Exit criteria (start Phase 2 only when met)

| Metric | Target | Current |
|---|---|---|
| Weekly active installs | 2,000 | 0 |
| Third-party plugin READMEs recommending us | 15 | 0 |
| External contributors | 5 | 0 |
| Community-contributed adapters | 1 | 0 |
| Clients supported | 6 | 0 |

## 10. Rough sizing

~12–13 engineer-weeks of P0 work. With 1 engineer: ~3 months. With 2: ~6–7 weeks, since M2 adapters parallelize cleanly once M2-1 and M2-3 exist.

These are estimates for planning, not commitments. The two items most likely to blow up are **M1-4** (Claude Code round-trip) and **M2-3** (formatting-preserving config edits).

## 11. Top risks to the MVP itself

| Risk | Mitigation |
|---|---|
| Client config formats are undocumented and change without notice | Golden-file tests per client version; treat adapter breakage as a P0 bug class, budget for it permanently |
| A client vendor objects to us writing their config | Only write documented config locations; never patch binaries; engage vendors early and publicly |
| The IR can't cleanly express a real dialect | De-risk in M1-4 before building adapters |
| Formatting-preserving edits prove harder than expected | Spike M2-3 early; fall back to append-only sections with clear markers if needed |
| Spec 1.1 lands mid-build | The IR is the insulation — that's what it's for |
