# MVP Scope & Status

**Living tracker.** Update the Status column as work lands. Everything here is Phase 1 from [docs/04-roadmap.md](docs/04-roadmap.md).

Last updated: 2026-08-31 · Overall status: **v0.2.0 released; every implementation item is built, tested and audited** — including M11 (skill content scanner), M3-5 (OCI registry source) and M11-11 (the model pass), all three pulled forward from Phase 2.

**Pushed to GitHub 2026-08-25**, and CI ran for the first time. It found
**eleven defects within minutes**, two of them in product code: a manifestless
Claude Code plugin was named after its own absolute path on Windows
(`path.Base` where `filepath.Base` was meant, one character apart), and
`safepath` accepted a Unix absolute path as relative there, because
`filepath.IsAbs` answers false for a rooted path with no drive letter — the
package already documented that exact principle for the mirror case and
implemented only one direction of it. `safeJoin`, the function between an
attacker-chosen filename and the filesystem, let `"."` through on Windows for
the same family of reason.

The rest were the suite itself being Mac-shaped: `go build -o` produces a
non-executable file on Windows without `.exe`, git rewrites text to CRLF and
broke two structural scans, and `"file://" + a Windows path` is not a URL. One
of those hid a test that had been checking nothing: the ad-hoc install in the
prune test silently failed, and its absence afterwards was indistinguishable
from the deletion the test exists to catch.

**The lesson is the one this project keeps relearning: a gate that has never
run is not a gate.** CI existed as YAML for weeks. Its first actual execution
was worth more than any amount of local green.

**Made ready for CI adoption (2026-08-25).** Two gaps stood between a good CLI
and something a company could deploy, and both are closed:

- **`scan` now finds every plugin beneath a path.** It took exactly one, so a
  team with several internal plugins had to write a shell loop over directories
  they enumerated themselves — a list that stops covering plugins added later,
  which is the failure this scanner exists to prevent. Findings are relocated to
  be repository-relative, because three plugins can each have
  `skills/deploy/SKILL.md` and a dashboard given the plugin-relative path
  annotates whichever it resolves first.
- **`action.yml`** makes CI adoption three lines of YAML instead of a
  hand-written download-verify-run script, and verifies the binary against the
  release checksums before executing it.

Verified end to end from a clean clone against a two-plugin repository: build,
validate, scan, declare, sync, secret injection, the injected-instruction attack
caught by *both* the scan job and the lockfile job, deliberate acceptance, and
removal.

That pass also corrected the documentation. The guides said a gained
high-severity finding "stops the sync"; it stops the **update**. `sync` holds
the pin and fails on a digest mismatch, `update` re-resolves and is where the
content gate applies. Two different messages for two different drifts, now
distinguished in [ci-integration.md](docs/ci-integration.md).

**Tested against four real clients on a developer's Mac (2026-08-30).** Claude
Code, Codex, Cursor, VS Code — and opencode, which had no adapter and now does.
The install was verified where the vendor ships tooling that can answer:
`codex mcp list` reports the server `enabled`, and `opencode mcp list` reports
it `connected`, meaning opencode launched it and completed the MCP handshake.
Cursor and VS Code have no equivalent read-back; VS Code's own `--add-mcp`
writes the same shape we do, which is the closest confirmation available.

Two findings came out of it, and neither would have been found by reading:

- **opencode is the second client that can take skills**, and unlike the
  others the location is documented — its loader scans configured skill
  directories recursively, so a whole plugin package installs in one place.
  But its MCP dialect wants environment variables under `environment`, while
  opencode's own bundled documentation says `env`. A config using `env` is
  **accepted without complaint and then silently discarded**. Following the
  vendor's prose would have cost every plugin the `PLUGIN_ROOT` and
  `PLUGIN_DATA` that §9.1 requires, and the only symptom would have been a
  plugin that does not work. The schema is right and the prose is wrong;
  `opencode debug config` reports what was actually resolved, which is how the
  two were told apart.
- **Removal did not restore a file.** README and getting-started both invite
  the reader to install a plugin, remove it, and diff. Removal was
  semantically exact — no server survived, no hand-written entry was touched —
  but an emptied object reflowed onto two lines, a config with no trailing
  newline gained one, and a config that never had an `mcp` key kept an empty
  one. Only a byte comparison finds that, and nobody had made one. The receipt
  now records which containers an install created and removal reclaims them
  while they are still empty; the invitation to diff is a test.

The second is the same shape as the gitignore, the empty directory and CI's
first run: **a claim nobody had turned into a check.** The first attempt at the
fix judged emptiness recursively and read `{"command": "mine"}` as empty,
because a string has no keys below it — an existing test caught it deleting a
user's server, which is the only reason it is not in the released binary.

**Verified end to end against five real clients, by their own CLIs
(2026-08-30).** Two plugins carrying a skill and a working MCP server were
installed across Claude Code, Cursor, Codex, VS Code and opencode, then each
client was asked what it had:

| | MCP | skills |
|---|---|---|
| Claude Code | 2/2 connected | 2/2 listed |
| Cursor | 2/2 ready | package layout verified |
| Codex | 2/2 enabled | 2/2 loaded |
| opencode | 2/2 connected | 2/2 loaded |

Every server launched, and each one received `PLUGIN_ROOT` and `PLUGIN_DATA`
with `cwd` at the plugin root — checked by a probe reporting its own
environment, not by reading configuration. Removing both returned **all five
configuration files to their pre-install bytes**.

Getting there took four fixes. Two were found by that diff and neither is
visible in a single-plugin test:

- **An empty container outlived every plugin that used it.** Only the first
  install into an empty config records having created the container; by the time
  that plugin is removed the container holds the others, and by the time it is
  empty the receipt that knew we made it is gone. An install now inherits the
  record, so whichever plugin leaves last takes the container with it.
- **The same again in the second config file.** VS Code keeps servers in
  `mcp.json` and skills locations in `settings.json`, and the fix for one did
  not reach the other.

Two more were on the clients' side, and a bridge should absorb them rather than
report them:

- **Two adapters never wrote the working directory at all.** Claude Code and
  Codex have their own encoders and never called `Materialize`, where §7.2.1's
  default lives.
- **Two clients accept the value and ignore it.** VS Code and Claude Code start
  a server wherever the client itself was started. Those two now go through
  `agentbridge run --cwd <plugin root> --`, the same launcher that injects
  secrets, wrapped once when a server needs both. The three clients that honour
  the value are left alone.

**The corpus has been run against four clients (2026-08-30).** VS Code 5/0/13,
Cursor 5/1/12, Codex 4/1/13, opencode 4/1/13 (pass/fail/unmeasured). Results in
[conformance/results/](conformance/results/).

**Exactly one client loads a conformant package.** Cursor accepted all 18 cases
carrying only the specification's `plugin.json`, deliberately without
`.cursor-plugin/plugin.json`. No other client does. Codex requires
`.codex-plugin/plugin.json` and rejects a conformant package with *"missing
plugin.json"* — confirmed by control, since adding that one file makes the same
package install. Claude Code requires `.claude-plugin/`. opencode and VS Code
read no plugin manifest at all and find skills by scanning directories.

That is the clearest evidence yet for why this project exists, and it is now a
measurement rather than an assertion. It also revises an assumption: the three
clients on the specification's launch list do not behave alike, and the one
that honours it is not the one whose vendor is loudest about it.

**§7.1 splits the field, and the split has a mechanical cause.** Skills must be
immediate children of `skills/`; a case ships one nested deeper. Cursor and VS
Code load only the two legitimate skills and pass; Codex and opencode load all
three and fail. The two that pass scan one level, the two that fail scan
recursively — neither behaviour looks chosen with the requirement in mind. Half
a small sample getting it wrong in the same direction is worth raising upstream
as a question about the requirement, not only as four bug reports.

**Cursor also fails 004** — a manifest with no `name` must not load and must
contribute nothing, and Cursor made its skill available. It is scored on Cursor
alone, because Cursor is the only client where the package went through a real
plugin mechanism rather than a skills directory.

**Public, and released.** The repository is public and **v0.2.0 is published**
— to GitHub, Homebrew and npm, the last of those over OIDC with no token. v0.1.0
was the first: six platforms, checksums, a Sigstore signature and SLSA build
provenance. The release pipeline succeeded on its first real run, having been
rehearsed locally first — which is how two defects were caught before they
could ship rather than after.

The larger of the two was in `install.sh`, the path the README leads with:
`REPO` named the organisation `agentbridge`, which is not ours, so **every
install would have failed**. Reading the script would not have found it;
running it against a real published release did. The failure also disguised
itself — a 404 meant no redirect to a tag, `sed` left the URL untouched, and
the URL became the version, so the error blamed the download rather than the
lookup. Verified afterwards against the live release: default install, pinned
version, custom `AGENTBRIDGE_BINDIR`, and `AGENTBRIDGE_REQUIRE_SIGNATURE=1`,
with checksum and cosign signature both confirmed, and `gh attestation verify`
checked against negative controls so that a passing result means something.

The second: GoReleaser's `go mod tidy` hook rewrote `go.mod` as its first act,
because three directly-imported modules were recorded as indirect. That would
have built the published binaries from a manifest differing from the committed
one.

**Documentation audited by running it (2026-08-30).** All 30 markdown files
checked against the code and the published release, and TESTING.md executed
section by section with its documented output compared to the real thing. Most
matched. Four did not, and each was the kind of error only running finds:

- **TESTING.md §7 documented a test that could not work.** It said to prove the
  OCI digest check by editing the plugin directory while the local registry
  runs. The stand-in packs the layer once at startup, so the edit changes
  nothing it serves and the install *succeeds* — manufacturing confidence in
  precisely the check the section exists to demonstrate. The stand-in now takes
  `-tamper`.
- **docs/ci-integration.md told readers to use `@v1`.** No such tag exists, so
  every workflow copied from that page would fail to resolve the action.
- **The wrong organisation appeared four times across three files**, including
  inside the npm README that shipped in the published tarball, and in the
  advice `install.js` prints when an install fails.
- **A skills-only plugin created an empty config in every JSON client** — found
  because getting-started.md documents `== cursor` for that case and the code
  had drifted to `!!`. The documentation was right and the code was wrong.

Three of the four are now enforced by tests rather than remembered: the
organisation name across every file type, the action tag against git's own tag
list, and the empty-config behaviour. All 132 relative links resolve, and the
three documented install paths — `install.sh`, Homebrew, npm — were each run
against the published release and produce a working 0.1.0.

**What remains is not code.** None of these can be finished by writing Go:

| | Blocked on |
|---|---|
| **M10-2** — measure the target clients (§6) | **Partly done, and the partial result is the interesting one.** The corpus has been run against Codex and opencode (4 pass, 1 fail, 13 unmeasured each). Cursor and VS Code cannot be run without a person driving 18 cases through a GUI. See [conformance/README.md](conformance/README.md) for what the runs found. |
| **Release automation** | Both package managers work by hand and neither is automated. `brew install agentbridgehq/tap/agentbridge` and `npm i -g @agentbridgehq/agentbridge` were each verified end to end, but the tap formula was hand-written — GoReleaser needs `HOMEBREW_TAP_TOKEN`, a credential scoped to the tap repository, since a workflow's own token cannot write to another repo. npm needs its trusted publisher configured on npmjs.com. Both are one manual step, and neither can be done from here. |
| **D-02 / M9-4** — the name | Now urgent rather than administrative. `agentbridge` was taken on GitHub; npm refuses it unscoped as too similar to `agent-bridge`; the `@agentbridge` scope belongs to an unrelated framework; and three further published packages carry the name in this exact space, two of them shipping per-client adapters. A name three other projects reached for independently does not distinguish this one. Trademark and domain remain unchecked. |

**Third full review (2026-08-11).** Docs and implementation re-checked after
M11 and M3-5, then every status marker in this file re-checked against the code
it claims. The corpus passes 18/18, every documentation link resolves, and the
claimed counts match. Nine defects found and fixed — and **two of the ✅ marks
in this file were false**, which is the finding that matters most, because a
status tracker nobody re-derives is just a list of intentions:

1. **M6-7 (`--json` on every command) was ✅ and untrue.** `cache` rejected the
   flag outright; `version` printed prose. Both now emit JSON — and the
   contract test no longer takes a hand-written list of commands, since a
   hand-written list is precisely what let two of them go missing. It reads the
   command names out of the dispatch switch, so a new command joins the
   contract by existing rather than by being remembered.

2. **M10-2 (manual client matrix) was ✅ and untrue**, and contradicted both §6
   and README's own "what is not done". The apparatus exists; the run has not
   happened. Marked 🟨.

3. **`agentbridge conformance` did not work outside this repository** — see the
   M10 note. The command exists specifically for client vendors, and a client
   vendor is exactly who does not have `conformance/cases` on disk.

4. **`telemetry.md` was quietly wrong about which hosts get contacted.** It said
   the registry named in the reference was the only one. It is not: no large
   registry serves blobs itself, so a pull follows a **redirect to a CDN** the
   registry chooses. Following it is not optional — refusing would break ghcr.io
   and Docker Hub — so the doc now states it, next to the token-realm exception
   it already carried. The audit also closed a real gap while confirming this:
   nothing stopped a registry redirecting an HTTPS pull to plain HTTP. The
   digest would still catch substituted content, but the request would go out in
   the clear, telling anyone on the path which plugin is being installed.
   Downgrades are now refused and chains capped at five.

5. **`install --json` wrote zero bytes when the scanner blocked it.** `scan` and
   `validate` both emit their findings as JSON *and* exit non-zero; `install`
   refused on exactly those findings and handed a script an empty pipe. It now
   emits the blocking findings as a refusal document.

6. **The CLI had no tests at all.** Every test lived under `internal/`, so the
   `--json` contract — a documented promise since M6-7 — was enforced by
   nothing, which is how both this defect and the `sync --json` one before it
   survived. `cmd/agentbridge/json_test.go` now builds the binary and asserts
   the contract across thirteen commands, including exit codes and a clean
   stdout. Verified by reintroducing the defect and watching it fail.

7. **`docs/05` described unbuilt controls in the same voice as shipped ones.**
   §3.2 had been rewritten to split shipped from not-yet during M11; §3.1 and
   §3.3–3.5 had not, so "Signature verification (Sigstore/cosign)" read as
   existing. It does not: *our own release binaries* are signed, and nothing
   verifies a signature on a **plugin**. Every layer now marks its status.

8. **The scanner silently skipped files it could not read.** "Nothing found" and
   "nothing found in the parts I could read" are different claims. Making it an
   error would have been worse — the install gate treats a scan error as
   advisory, so one unreadable file would have switched the gate off entirely,
   which is exactly what an attacker would arrange. It now records the gap and
   says so.

9. Two stale forward-looking claims, one of them **printed to every user** who
   runs `agentbridge` with no arguments: "Lockfiles and secret handling arrive
   in M4-M5." Both have been built for weeks.

**The recurring shape is worth naming.** Almost all of these were true when
they were written. Code drifts under prose silently, and a status column drifts
faster than either, because nothing recomputes it. The defence is the same one
used elsewhere here — make the claim a property something checks: the loss
catalogue, the privacy scan, the generated client table, and now the `--json`
contract, which reads the command list out of the dispatch switch precisely
because the hand-written version is what failed. A further test now asserts from
the build graph that no first-party package ships without being covered by the
privacy scan, which the new top-level `conformance` package would otherwise have
escaped.

Each of those would have caught its own defect on the commit that introduced it,
without waiting for somebody to re-read the file.

**The repository did not contain its own CLI (2026-08-11).** While committing
M11, `git status` showed no change to files that had certainly been edited.
`.gitignore` carried a bare `agentbridge` line, intended for the built binary at
the repository root — but an unanchored pattern matches at **any depth**, so it
had been silently excluding the whole `cmd/agentbridge/` source directory since
the first commit. 217 tracked files, none of them the CLI. It also swallowed
`npm/bin/agentbridge`, the published npm package's declared entry point.

A clone of this repository would not have built, and nothing in the local
working tree could reveal it: every build, test and gate passed, because they
all read the working tree rather than the index. Fixed by anchoring the pattern
to `/agentbridge`. The general lesson is narrow and worth keeping: **a gitignore
pattern without a leading slash is a directory-name match, not a filename
match** — and the failure it produces is invisible to everything except a fresh
clone.

**Second full review (2026-08-10).** Docs, command surface and implementation
re-checked. Command names, flags and every relative documentation link resolve;
the corpus passes 18/18; upstream is unmoved. Two defects found and fixed:

1. **Two plugins sharing a name silently orphaned one of them.** Configuration
   entries are keyed by plugin name and a receipt is the only record of what to
   remove, so installing a second plugin under an existing name overwrote the
   first's receipt. `remove` then cleaned only the second, leaving the first's
   entries in the client's configuration **permanently**, with nothing left that
   knew they existed. Nothing in Agent Plugins prevents the collision — §5.5
   constrains the name string and no authority allocates it (threat T4 in
   [05](docs/05-security-and-trust.md)).

   The fix refuses the install and says which source holds the name. Receipts now
   record a **source identity** — the upstream with the revision removed — so an
   upgrade from v1.0.0 to v1.1.0 is not mistaken for a second plugin. Verified
   both ways: upgrades pass, a different repository claiming the same name is
   refused, and removing the incumbent frees it.

2. **`sync --json` printed prose on an empty workspace.** A script piping to
   `jq` broke on the case where nothing is declared, which is exactly the case a
   fresh checkout hits.

Both are the same shape as the defects the first review found: correct-looking,
silent, and only visible by asking what the code would do if it were wrong.

**A note on method.** Three apparent failures in this review turned out to be
faults in the throwaway shell used to probe, not in the tool — zsh not
word-splitting an unquoted variable, and `echo` interpreting the `\n` escapes
inside JSON output. Worth recording because the instinct on seeing eight
commands "fail" at once should be to doubt the harness first.

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
| D-02 | Name + trademark availability verified (npm, GitHub org, domain, USPTO/EUIPO) | [07 D6](docs/07-open-questions.md) | 🟨 org + npm claimed; trademark and domain unchecked |
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
| M2-10 | Clean uninstall | Removes only what we added; user's other config untouched | ✅ ⁶ |
| M2-11 | Adapter: opencode | Skills *and* MCP; the second client that takes skills | ✅ |

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

**Post-M2 review pass (2026-08-10).** A full re-read of the docs and code against
the canonical spec turned up three real defects, all now fixed with regression
tests:

1. **`remove` silently did nothing for whole-package installs.** Removal
   operations carry no "before" content, and the no-op check read a nil before
   as "nothing to do" — so Apply skipped the deletion and the plan printed
   *already up to date*. It affected Claude Code, the highest-value adapter, and
   survived because every other client's removal edits a config file. The exact
   silent-failure mode this project exists to eliminate, shipped inside it.
2. **The TOML editor's `Original()` re-rendered instead of returning the bytes
   it read.** Renderer output is normalized, so for any file a human had touched
   the `--dry-run` diff claimed changes we were not making, and the no-op check
   compared against the wrong baseline.
3. **Claude Code did not receive the §9.1 environment.** We had fixed this for
   the clients we write config files for and missed the one that gets a whole
   package. Now mapped onto Claude Code's own placeholders, which it expands, so
   the process receives exactly what a conformant client would give it.

The first two are worth remembering as a class: both were *silent*, both passed
a green test suite, and both were found by asking "what would this do if it were
wrong?" rather than by adding coverage to what was already tested.

**Uninstall (M2-10)** is driven entirely by receipts recorded at install time,
never by pattern-matching the config. A user entry that happens to share our
`<plugin>.<server>` naming is provably untouched; there is a test for exactly
that case.

⁶ Uninstall removed everything it wrote from the first day, but did not restore the *file*: an emptied object reflowed, a missing trailing newline appeared, and a container we created stayed behind empty. Found by diffing a real machine against a backup on 2026-08-30, not by the suite. Now enforced by `TestInstallThenRemoveRestoresConfigsExactly`.

### M3 — Sources & fetch · P0 · ~1 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M3-1 | Local directory source | | ✅ |
| M3-2 | Git source, pinned to resolved commit SHA | Tag/branch resolves to an immutable SHA in the lock | ✅ |
| M3-3 | Digest computation + verification | Content-addressed; tamper detected on re-fetch | ✅ |
| M3-4 | Local content cache | Offline re-install works | ✅ |
| M3-5 | OCI source | **P1** — defer if it costs schedule | ✅ ⁴ |

⁴ Deferred as planned, then built once the rest of the MVP was done. The
argument for it is not that anyone publishes plugins this way today — nobody
does — but that every organization which has adopted containers *already* runs a
registry, already mirrors it into air-gapped networks, already scans and signs
what is in it, and already has an answer to who may push. A plugin distributed
this way inherits all of that on the day it is published, and none of it is ours
to operate. The registry is also content-addressed by construction: a tag
resolves to a manifest digest before anything is downloaded, so the protocol
enforces the pinning discipline instead of us.

`oci://ghcr.io/org/plugin:v1.2.0`, or `@sha256:…` to pin. The scheme is required
rather than inferred, because `ghcr.io/org/plugin` is already a perfectly good
*git* shorthand and choosing a protocol from the hostname would be a surprise
that costs trust once.

**This is where the project's no-network rule was spent, and it was spent
deliberately.** Until now `internal/privacy` enforced a blanket ban on opening a
connection: fetching shelled out to `git`, and nothing else reached out. A
registry API cannot be delegated to a subprocess the same way, so the ban is now
an allow-list of exact filenames — and it was only worth giving up for something
stronger, so it was replaced with a narrower property: **the fetcher may contain
no absolute URL at all**, so every address it *originates* comes from the
reference. A runtime test drives a full pull against a fake registry and asserts
the same from the outside.

Two destinations are chosen by the *registry* rather than by the user, and the
third audit is what pinned the second one down. A registry may direct the
anonymous token request to an auth host of its choosing, as Docker Hub does —
that one was documented at the time. It also answers a blob request with a
**redirect to a CDN**, because no large registry serves layers itself, so
following one is not optional and `telemetry.md` was quietly wrong to imply the
reference host was the only one contacted. Both are now stated there. The
transport is enforced in both directions: a plaintext realm is refused, and a
redirect may never downgrade an HTTPS pull to HTTP — the digest would catch
substituted content either way, but a plaintext request tells anyone on the path
which plugin is being installed and invites them to answer first. Chains are
capped at five.

Pulling is **anonymous only**. Reading a developer's Docker credentials would
mean the tool starts using credentials the user never mentioned, against hosts
they did not name; a private registry therefore does not work yet, and that is
the better failure.

**The riskiest code in the project is the layer unpacker**, and it is worth
saying why plainly: it writes attacker-chosen filenames, with attacker-chosen
contents, to a path on the user's machine, in a process that will shortly hand
the result to an agent holding their credentials. Only regular files and
directories are created — symlinks, hardlinks, devices and FIFOs are dropped,
not followed — permissions are normalized so no artifact can ask for setuid,
duplicate entries are refused, and totals are bounded so a small download cannot
become a full disk.

Writing the traversal check surfaced the classic version of that bug in our own
code: `filepath.Clean("/../escaped.txt")` returns `/escaped.txt`, because Clean
treats `..` above the root as meaningless and silently drops it. A check applied
*after* cleaning therefore never sees the traversal it was written to catch.
Nothing escaped — the paths were contained — but a hostile archive was being
quietly rewritten into something harmless-looking and unpacked without
complaint, which is not an acceptable outcome for an artifact that is malformed
at best. Every path element is now inspected before anything is cleaned. The
test that found it was written first and failed on all five inputs.

**Two further design decisions in M3 worth recording.**

*Git is invoked as a subprocess, not through a library* — a reversal of the plan
in [08 §3](docs/08-tech-stack.md), and the reason is authentication. A pure-Go
implementation has to reimplement credential helpers, SSH agent forwarding,
`insteadOf` rewrites, enterprise proxies and SSO device flows, and the first
plugin an enterprise developer installs is the private one in their own
organization. Shelling out inherits all of that correctly on day one. The costs
are accepted and handled: `git` must be present (detected, with a clear error),
nothing goes through a shell, and arguments that could be read as flags are
rejected before anything executes.

*There are now two digests, and both are needed.* The IR digest asks "is this
the same plugin?" and is computed from the parsed model. The **tree digest** asks
"are these the same bytes?" — and that is the question integrity actually turns
on, because a script under a skill's `scripts/` directory can be replaced
without changing a single field the IR records. That is precisely the tamper a
supply chain has to catch, and it is why the cache re-verifies every entry it
serves rather than trusting its own contents.

### M4 — Lockfile & resolution · P0 · ~1.5 weeks

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M4-1 | `agentbridge.yaml` schema (declared intent) | Documented and versioned | ✅ |
| M4-2 | `agentbridge.lock` schema | Digests, source refs, per-client install plan; human-reviewable diff | ✅ |
| M4-3 | Scopes: project vs. user, with precedence | Documented, tested | ✅ |
| M4-4 | `sync` — make machine match lockfile | Idempotent; converges from any starting state | ✅ |
| M4-5 | `update` — re-resolve and rewrite lock | Shows what changed before writing | ✅ |

**The lock is the security artifact, not the build artifact.** Its most
important line is `capabilities`. A plugin is not only code but instruction text
handed to an agent with tool access, so "what changed when we bumped this
version" is a security question. `update --dry-run` therefore reports the
capability delta first:

```
  ~ acme.db                  ca0a9d4c2d1d
      version 1.0.0 -> 1.1.0
      !! gains capability: network
      + skill report
      + server telemetry
```

That is the reviewable-diff story from [03 §5](docs/03-architecture.md) working
end to end: a version bump that grants an agent the ability to reach the network
is a different event from one that does not, and without this the difference is
invisible.

**Convergence is bounded by ownership.** `sync` may remove a plugin a manifest
used to declare and no longer does, and must never touch one a developer
installed by hand — a sync that deletes someone's own work is a tool nobody runs
twice. Receipts therefore record which manifest scope declared each install, and
prune only considers those.

**Two bugs the tests caught, both worth remembering.** An emptied manifest
produced zero entries, and a sync that keyed everything off the entry list then
never opened that lock — leaving it listing a plugin nobody declared any more.
And the receipt store is a whole-file document: an instance loaded before
another write would erase receipts it never saw, making those plugins
unremovable with nothing to say why. Saving now checks the file has not changed
since it was read and refuses rather than clobbering.

### M5 — Secrets · P0 · ~1 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M5-1 | Secret reference syntax (`${secret:...}`) in IR | Never a literal in our model | ✅ |
| M5-2 | OS keychain backend (macOS / Windows / libsecret) | Read + write + delete | ✅ |
| M5-3 | Refuse plaintext secrets by default | Requires explicit `--allow-plaintext-secrets`; warning is loud | ✅ |
| M5-4 | Detect existing plaintext secrets in client configs and offer migration | Read-only detection; migration is opt-in | ✅ |

**The specification made this harder than expected, and said so.** §9.2 and
§7.2.1 both state that `env` values and headers are *visible package data* and
that plugins MUST NOT embed credentials in them, and §7.2.1 adds that "Agent
Plugins v1 defines no OAuth configuration or portable credential-reference
fields." Read together: **there is no conformant way to give an MCP server a
credential in v1.0.0.** Every plugin that needs one is either violating the
specification or relying on client-specific behavior.

So `${secret:...}` is deliberately **ours alone and not portable**. §9.2 requires
a conformant client to leave unrecognized placeholder text literal, which means
a reference written into an `mcp.json` would be handed to the server as the
eleven-character string it is. A reference must therefore be resolved before
anything reaches a client — never written into a portable artifact.

**How the value actually reaches the server.** A referenced secret is not
resolved at install; the server is rewritten to launch through `agentbridge run`,
which reads the credential store at spawn time and execs the real command. The
value exists only in the process environment of the server that needs it, never
in a file that gets committed, backed up, or shared on a screen. Verified end to
end: after installing a plugin with a reference, the credential appears nowhere
in the written config.

This is the only part of the tool that sits in a running agent's path, and it
gets there only because someone chose to use a reference — so
[03 principle 3](docs/03-architecture.md) still holds: the default install
writes plain configuration with no runtime dependency on us.

**Refusal is the default.** A credential-shaped literal is not written unless
`--allow-plaintext-secrets` is passed, and the message says all three ways
forward rather than just objecting. Detection is by value as well as name,
because a token in a variable called `API_URL` is exactly the case
name-matching misses.

### M6 — Commands · P0 · ~1.5 weeks

| ID | Command | Acceptance criteria | Status |
|---|---|---|---|
| M6-1 | `install <source>` | Installs to all detected clients or `--client` subset | ✅ |
| M6-2 | `list` | What's installed, where, at what version | ✅ |
| M6-3 | `remove` | Clean removal across clients | ✅ |
| M6-4 | `sync` / `update` | Per M4 | ✅ |
| M6-5 | `validate` | Spec conformance for plugin authors + practical warnings the spec doesn't cover | ✅ ³ |
| M6-6 | `doctor` | Explains why a plugin did nothing in client X — the ecosystem's most common question | ✅ |
| M6-7 | `--json` output on every command | Scriptable from day one | ✅ |

³ Landed early, during the spec-alignment review: it is the author-side of
conformance and it wired up a strict validator that otherwise had no caller.
Every finding cites the clause it comes from. It is the only place the
author-binding MUST NOTs get reported — §9.2 and §7.2.1 forbid credentials in
`env` values and headers, and no *client* can enforce that, because by the time
a client sees them they are already package data.

**`doctor` is the command the positioning rests on.** The specification permits
a conformant client to support *neither* skills nor MCP servers (§11.1, §11.2),
component locations are fixed so a plugin either lands or silently does not, and
every client spells its configuration differently. "I installed it, why is
nothing happening in X?" is therefore the ecosystem's most common question, and
nothing else answers it.

The checks are deliberately not a health dashboard. Each exists because it is a
real reason a plugin appears installed and does nothing — a client with no
documented skills location, entries another tool removed after installation, a
deleted package, a command that is not on PATH, a referenced secret that was
never stored — and each carries the specific next action. **A check that cannot
say what to do next has not earned its place**, and there is a test asserting
every failure carries a fix.

One check was written and then removed before shipping: comparing an installed
package against its recorded tree digest. That digest addresses the *source*
package, and an installed copy legitimately differs — the Claude Code adapter
writes a manifest on top of the copied tree — so it would have reported every
such install as modified. A check that always fires trains people to ignore the
ones that matter.

**Ergonomics.** `agentbridge install ./plugin --dry-run` previously ignored the
flag and installed for real, because the standard library's flag parser stops at
the first non-flag argument. That is the worst kind of failure: the command
appears to work and does the opposite of what was asked. Flags may now appear on
either side of the argument.

### M7 — Fidelity reporting · P0 · ~0.5 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M7-1 | Per-client fidelity report on install | Shows skills n/m, mcp n/m, and every dropped element with a reason | ✅ |
| M7-2 | Documented, enumerated loss list per adapter | No silent drops anywhere; each has a stable reason code | ✅ |

**"No silent drops" cannot be a matter of discipline, so it is enforced.** Three
rules, each with a test:

1. Every loss code is catalogued with a meaning and, where one exists, a remedy.
2. Every adapter declares the codes it can emit, so what a client might not
   carry is knowable *before* installing anything — `agentbridge losses`.
3. **An adapter may not emit a code it did not declare.** This is the rule that
   keeps the rest honest: without it, a new drop can be reported perfectly at
   runtime and still be a surprise, because the list of what that client might
   not carry never mentioned it.

Writing the catalogue immediately found `client.mcp_unsupported`, declared and
never emitted — dead documentation for a failure mode that does not exist. It
was deleted.

**Faults are now distinguished from facts.** Some losses are permanent
properties of an ecosystem where clients genuinely differ: Gemini CLI has no
skills mechanism and no amount of effort changes that. Others mean something is
wrong and can be fixed. A user looking at six warnings needs to know which two
deserve their attention, so the report marks them differently and every
non-expected loss is required by test to carry a remedy.

One test was written and then rewritten: it asserted that each catalogue entry's
prose contained one of a set of phrases. That tested the wording rather than the
contract, and failed on a description that was perfectly clear. It now checks
that the meaning explains rather than restates.

### M8 — Distribution · P0 · ~1 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M8-1 | GoReleaser pipeline, signed releases | Cosign signature + checksums on every artifact | ✅ |
| M8-2 | SLSA provenance for our own binary | We cannot sell provenance while shipping unsigned | ✅ |
| M8-3 | Homebrew tap | | 🟨 tap live, `brew install` verified; GoReleaser cannot update it until HOMEBREW_TAP_TOKEN exists |
| M8-4 | npm wrapper package (downloads + verifies the binary) | `npm i -g` works without a Node runtime dependency at execution | ✅ published as `@agentbridgehq/agentbridge`, install verified |
| M8-5 | Install script with signature verification | `curl \| sh` verifies before executing | ✅ verified against the published v0.1.0 |
| M8-6 | Scoop / winget | **P1** | ⏸️ same as M8-3 |

**The pipeline has now run, and succeeded on its first tag (2026-08-30).**
v0.1.0 publishes six platforms, SBOMs, checksums, a cosign signature and SLSA
provenance. It was rehearsed locally first — `goreleaser check`, then a full
snapshot build of all six targets — and that rehearsal is what caught the
`go mod tidy` hook rewriting `go.mod` mid-release.

**The installer was broken, and only running it found out.** `REPO` named the
organisation `agentbridge` rather than `agentbridgehq`, so every install would
have failed on the path the README leads with. Earlier testing had been against
a *fake* release with `AGENTBRIDGE_BASE_URL` set, which bypasses the repository
constant entirely — the one variable the fake could not exercise was the one
that was wrong. Verified since against the real release: default install,
pinned version, custom `AGENTBRIDGE_BINDIR`, and
`AGENTBRIDGE_REQUIRE_SIGNATURE=1`, with `gh attestation verify` checked against
a tampered archive and a wrong repository so that a pass means something.

That earlier fake-release testing was still worth its cost: it found that
`tar -xzf FILE -C DIR` fails on BSD tar, which applies options in order, so
`-C` changed directory before the archive path resolved. On macOS every install
would have failed *after* passing verification.

**Verification is not optional anywhere.** The usual `curl | sh` installer
downloads a binary and runs it having checked nothing, which is precisely the
posture this project exists to argue against. So: checksum verification cannot
be turned off; an artifact the checksums file does not list is refused rather
than waved through (`--ignore-missing` is deliberately unused, and a test
enforces that); signatures are verified whenever cosign is present and can be
made mandatory; and the npm postinstall verifies before writing anything.

**Drift tests.** Distribution breaks in a characteristic way — a platform added
to CI but not to the release build, or an archive naming template changed while
the installers keep constructing the old name. Neither is caught until a tag is
pushed, which is the worst moment to find out because the fix needs another
release. `internal/release` moves both to every commit.

### M9 — Docs & launch · P0 · ~0.5 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M9-1 | README, quickstart, per-client compatibility notes | | ✅ |
| M9-2 | Plugin author guide (`validate` workflow) | | ✅ |
| M9-3 | Public threat model + telemetry statement | Telemetry is opt-in; schema published field-by-field | ✅ |
| M9-4 | Launch: HN / relevant communities / plugin-author outreach | Target ≥15 third-party READMEs recommending us | ⏸️ |

**M9-4 is not something the code can do.** Posting to communities and
approaching plugin authors is a human task; the materials are ready and the exit
criteria are in §9. Marked blocked rather than done, because marking it complete
would misrepresent the state of the project.

**Two documents are generated or enforced rather than written**, because both
are the kind that rot into being actively misleading:

- [`docs/clients.md`](docs/clients.md) is generated from the adapters
  (`make docs`), and a test fails when it drifts. Compatibility documentation
  written by hand goes stale within a release, and for this page that would be
  worse than having none — the whole product claim is that we tell people the
  truth about what each client takes.
- [`docs/telemetry.md`](docs/telemetry.md) says the tool collects nothing, and
  [`internal/privacy`](internal/privacy) fails the build if any shipping package
  makes an HTTP, TCP, WebSocket or gRPC call, references a domain we operate, or
  stops embedding the schemas. That claim erodes quietly otherwise: one
  well-meant crash reporter and a tool on every developer machine in a company
  is phoning home, with the documentation still saying it does not.

The audit behind that test found the claim already true — the only `net/http`
import in the codebase is a header-name validity check that touches no network,
and derived schema identifiers use the reserved `.invalid` TLD so they cannot be
fetched even by accident.

### M10 — Conformance harness seed · P1 · ~1 week

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M10-1 | Canonical test plugins (valid, partially-invalid, edge cases) | Covers each conformance rule in the spec | ✅ |
| M10-2 | Manual matrix run across MVP target clients | Produces the honest per-client support table for M9-1 | 🟨 ⁵ |
| M10-3 | Automated nightly runs | **P2** — Phase 2 | 🟨 ⁶ |

⁵ **The apparatus is built; the run has not happened.** `agentbridge conformance
--list` and `--record` produce a checklist and a results template,
[PROTOCOL.md](conformance/PROTOCOL.md) says how to run it, and `results/` takes
contributions. But `results/` contains only `agentbridge.json` — our own loader.
No third-party client has been measured, which is why every row in §6 is ⬜ and
why [clients.md](docs/clients.md) reports what we *write*, based on vendor
documentation, rather than what a client does with it. This was marked ✅ on the
strength of the mechanism existing; the acceptance criterion asks for the
**table**, and there is no table. Marked 🟨 by the third audit.

⁶ The nightly workflow is written and, like the release pipeline, has never run
— there is no repository for it to run in. Written ≠ verified.

Enough of M10 must exist to make the fidelity reports factually correct. Full automation is Phase 2.

**The corpus ships in the binary** (`conformance/corpus.go`), which the third
audit found it did not. `agentbridge conformance` read `conformance/cases`
relative to the working directory, so the one command written specifically for
people outside this repository — a client vendor checking their own
implementation — worked only inside it. It now extracts the embedded copy into
the cache, keyed by content digest, so `--list` still prints a durable path a
vendor can point their client at. `--corpus` still overrides, because somebody
editing the cases needs the ones on disk rather than the ones compiled in.

### M11 — Skill content scanner · P0 · ~1 week

Originally scheduled for Phase 2 and pulled forward, because it is the only part of this product that no other tool does *at all*. Everything else in the MVP is a better version of something that exists: a package manager, a lockfile, a config editor. Reading the instruction text is the part where there is no incumbent.

| ID | Item | Acceptance criteria | Status |
|---|---|---|---|
| M11-1 | Rule catalogue with rationale and remedy | Every rule documented; enforced by a test, as with the loss catalogue | ✅ |
| M11-2 | Instruction-text rules | Override, concealment, exfiltration, credential references, destructive actions | ✅ |
| M11-3 | Concealment rules | Bidi controls, zero-width, homoglyphs, HTML comments, encoded blobs | ✅ |
| M11-4 | `references/` and `scripts/` are read, not just `SKILL.md` | A client loads them like the skill body; a reviewer does not | ✅ |
| M11-5 | Server rules | Credential literals graded by confidence; remote egress as a stated fact | ✅ |
| M11-6 | `agentbridge scan <ref>` | Accepts a remote ref, so the question is answerable before installing | ✅ |
| M11-7 | SARIF 2.1.0 output | Valid document, `security-severity` set, rules resolve within the document | ✅ |
| M11-8 | Blocking gate on `install` and `sync` | High findings stop it; `--allow-flagged-content` to proceed | ✅ |
| M11-9 | **A benign fixture that produces zero findings** | The calibration property, asserted by test | ✅ |
| M11-10 | Diff-based re-scan on version bump | Report what changed in the text, not only that it changed | ✅ |
| M11-11 | LLM classifier for phrasing no regex reaches | Opt-in; local rules remain the floor | ✅ ⁷ |

**M11-10 is the item that makes the gate survivable.** A scan on its own answers "what is in this plugin?". At update time the useful question is different — *what is in it that was not in the version I approved?* — and threat T5, a plugin that was clean when reviewed and gains an injected instruction three commits later, is only visible in the second. It is invisible to a lockfile alone: the digest changes honestly, because the author really did edit the file.

So a locked plugin records the findings accepted when it was locked, and only what is **new** can block. That cuts both ways and both matter: a plugin with one permanently awkward sentence stops demanding an override flag every week — so the override keeps meaning something — while a bump that introduces an instruction to conceal activity stops a sync that would otherwise have looked like an ordinary version change. The acceptance lives in `agentbridge.lock` rather than in local state because "we looked at this and decided it was fine" belongs in a pull request, not on one person's laptop.

⁷ **M11-11 was twice deferred here as "it would break the offline guarantee", and building it is what showed that framing to be wrong.** The guarantee is not "this binary contains no HTTP client" — it is *every address this tool reaches is one you supplied, and nothing leaves your machine unless you ask*. Both survive intact:

- **Off unless asked, structurally.** `scanner.Scan` takes no classifier and cannot be handed one; only `ScanWith` runs a model. That is a default expressed in a type signature rather than a flag some later edit could invert, and a test runs the real binary with an endpoint configured in the environment to prove nothing is contacted without `--classify`.
- **No default endpoint.** The privacy scan forbids any absolute URL in that file, so there is nowhere in the code for a destination to live. The user names the host exactly as they name a git remote. A model on `localhost` is a first-class option, which means the air-gapped case keeps both the pass and the guarantee.
- **What is sent is documented** in [telemetry.md](docs/telemetry.md): the skill bodies and `references/`, one request per file, and nothing else — not scripts, not `mcp.json`, not configuration, not anything about the machine.

The interesting design problem was not the API call but **the classifier reading text an attacker wrote, and being asked about it.** The plugin's own words can address the model. Four constraints make that unprofitable rather than merely discouraged: a model finding can only ever be *added* — it can never clear or downgrade one the rules produced, so injection buys silence rather than authority; a quoted span absent from the file is a fabrication and is dropped; an unrecognised category has no rule, no rationale and no remedy, so it cannot be reported as one; and severity is assigned by us from a confidence label rather than taken from the reply. Model findings are also capped below the blocking threshold unless `--classify-can-block`, because one hallucinated High that stops a legitimate deploy teaches a team to pass `--allow-flagged-content` by reflex — which switches off the *regex* findings too, and those are the ones with evidence behind them.

**The design constraint that shaped every rule: false positives are the real failure mode.** A scanner that misses a hostile plugin has failed once. A scanner that fires on an ordinary one gets muted, and a muted scanner produces the appearance of coverage while checking nothing — so it fails on every plugin after that, invisibly. Severity is therefore assigned by *how hard a pattern is to reach innocently*, not by how bad it would be if malicious. M11-9 is the item that keeps this honest, and it is why ZWNJ is not flagged in Persian text and ZWJ is not flagged next to emoji.

---

## 6. MVP target clients

| Client | Conformant | Priority | Status |
|---|---|---|---|
| Cursor | yes | P0 | ✅ **skills and MCP both install.** Plugin path found by reading an installed plugin, then confirmed: Cursor listed the package and named `~/.cursor/plugins/local` itself |
| VS Code / Copilot | yes | P0 | ✅ **skills and MCP both install.** MCP shape matches its own `--add-mcp`; skills registered through `chat.agentSkillsLocations` and confirmed by VS Code listing the skill |
| Codex | yes | P0 | ✅ **skills and MCP both install.** `codex mcp list` reports the server enabled; `codex debug prompt-input` names the installed skill file back |
| Claude Code | **no** | P0 — highest strategic value | ⬜ package installs; not measured |
| One of Zed / Windsurf / Gemini CLI | no | P0 | ⬜ Gemini CLI adapter built, unmeasured |
| opencode | no | added after the fact | 🟨 `opencode mcp list` reports it connected; skills load |
| ChatGPT, Kiro | yes | P1 | ⬜ |

Claude Code is P0 despite being harder: it has the densest plugin ecosystem and is absent from the standard, which is precisely the gap that justifies a bridge existing.

opencode was not on this list and is now the **only client besides Claude Code that takes skills** — at a location its vendor documents, which none of the conformant three do. It is the clearest evidence that the target list should follow where skills can actually land rather than who has signed the standard.

---

## 7. Explicitly out of MVP

| Deferred | Phase |
|---|---|
| Accounts, workspaces, any server | 2 |
| Fleet inventory, drift | 2 |
| Risk *scoring*, LLM-based classification, signature *policy* | 2 |
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
- [ ] Instruction text is read before it is installed, and an ordinary plugin produces no findings.

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
