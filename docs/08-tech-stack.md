# 08 — Technology Stack

Concrete recommendations with reasoning. Library choices are starting points, not commitments — verify current maturity before adopting, especially anything MCP-related, since that ecosystem is months old.

## 0. The two decisions that actually matter

Everything else is replaceable. These two are not:

1. **The CLI must be a single static binary with no runtime dependency.** It installs on developer laptops and, later, on managed enterprise fleets. "Install Node 20 first" loses a meaningful fraction of users and disqualifies us from MDM-style deployment entirely. → **Go**.
2. **Backend and CLI must share one language**, because they share the IR, the adapters, the scanner heuristics, and the policy evaluator. Duplicating that logic across Go and TypeScript is a permanent tax on a small team. → **Go on both sides**, TypeScript only for UI.

Everything below follows from those.

---

## 1. Language choice

### Recommendation: **Go** for CLI, gateway, and control-plane API. **TypeScript** for web UI and docs. **Python** only for the ML/classifier pipeline, if and when it exists.

| Candidate | Case for | Case against | Verdict |
|---|---|---|---|
| **Go** | Single static binary; trivial cross-compile (darwin/linux/windows × amd64/arm64); the OCI, Sigstore, in-toto, container and policy ecosystems are all natively Go; excellent process supervision and concurrency for spawning N stdio servers; large hiring pool; boring in the way infrastructure should be | Less expressive than Rust; not the language plugin authors write in | ✅ **Primary** |
| **Rust** | Best choice if sandboxing/isolation becomes the core product; strongest memory-safety story for a security tool; excellent single-binary story too | Slower iteration for a small team; smaller hiring pool; would have to reimplement or FFI the Sigstore/OCI stack | Revisit only if Tier-2 sandboxing (see [03 §8](03-architecture.md)) becomes the headline feature |
| **TypeScript/Node** | Ecosystem alignment — the MCP SDK is most mature here, and most plugin authors are JS devs; fastest UI story | **Runtime dependency on the endpoint is disqualifying.** Bundling via bun/pkg produces 50–100MB binaries with awkward native-module edges | UI only |
| **Python** | Best ML/LLM tooling for the skill classifier | Terrible endpoint distribution story | Cloud scanner only, if needed |

**Distribution nuance:** many developers will reflexively reach for `npm install -g agentbridge`. Solve this the way esbuild and Biome do — publish a thin npm wrapper package that downloads and verifies the correct Go binary for the platform. You get npm's discovery without Node's runtime dependency. Same for a Homebrew tap, Scoop/winget, and a `curl | sh` installer that verifies signatures before executing.

---

## 2. CLI / bridge core (Go)

| Concern | Choice | Note |
|---|---|---|
| CLI framework | `spf13/cobra` + `spf13/pflag` | Standard; good completions and help |
| JSON Schema validation | `santhosh-tekuri/jsonschema` | Supports draft 2020-12, which the spec's schemas use. Schemas are **embedded** in the binary — the spec forbids fetching `$schema` at load time |
| Surgical config editing | `tailscale/hujson` (JSONC) + `tidwall/sjson` | **Critical and underestimated.** VS Code settings are JSONC with user comments. Rewriting a user's config file and destroying their comments and key order is the fastest way to get uninstalled. Every adapter write must be a minimal, formatting-preserving edit |
| YAML | `goccy/go-yaml` or `gopkg.in/yaml.v3` | For `agentbridge.yaml` |
| Frontmatter parsing | `adrg/frontmatter` | For `SKILL.md` |
| Keychain access | `99designs/keyring` | macOS Keychain / Windows Credential Manager / libsecret behind one interface |
| Golden-file CLI testing | `rogpeppe/go-internal/testscript` | Ideal for a tool whose output *is* the product. Snapshot every adapter's file writes |
| Release engineering | GoReleaser + GitHub Actions | Multi-platform builds, checksums, Homebrew tap, packages |

**Filesystem safety** is a first-class concern, not a library choice: the spec requires all resolved paths stay inside the plugin root. Use `os.Root` (Go 1.24+) or explicit `filepath.EvalSymlinks` + prefix checks. Symlink escape is threat T7 in [05](05-security-and-trust.md) and we will be judged on getting it right.

---

## 3. Artifact transport, signing, provenance

**Do not build a registry.** Use OCI registries as the artifact substrate — content-addressed by digest, signable, mirrorable, authenticated, and every enterprise already runs one (ECR, GAR, ACR, Artifactory, Harbor, GHCR).

| Concern | Choice |
|---|---|
| OCI push/pull of arbitrary artifacts | `oras-go` (ORAS v2) or `google/go-containerregistry` |
| Signing / verification | Sigstore `cosign` as a library — keyless OIDC signing, so plugin authors never manage keys |
| Provenance | in-toto attestations + SLSA build provenance, recorded in Rekor |
| Git sources | **Revised in M3: invoke the `git` binary, not a library.** The deciding factor is authentication — a pure-Go client has to reimplement credential helpers, SSH agent forwarding, `insteadOf` rewrites, enterprise proxies and SSO device flows, and the first plugin an enterprise developer installs is the private one in their own org. Always pinned to a resolved commit. |

We must sign and publish provenance for **our own** binary from day one. Selling provenance while shipping unsigned releases is not survivable in diligence.

---

## 4. Gateway / MCP runtime (Go)

| Concern | Choice |
|---|---|
| MCP protocol | Official MCP Go SDK — **verify current maturity before committing.** If it's not ready, the protocol is small enough (JSON-RPC 2.0 over stdio/HTTP) to implement directly, and doing so gives us the interception hooks we need anyway |
| HTTP server | stdlib `net/http` + `go-chi/chi` |
| Process supervision | stdlib `os/exec` with explicit process groups, timeouts, and reaping. Orphaned MCP servers are a real and common failure mode |
| Streaming | SSE + Streamable HTTP, both required by the spec |

**Sandboxing (Phase 4 research track), by platform:**
- **Linux** — Landlock (`landlock-lsm/go-landlock`) + seccomp for filesystem/syscall scoping; containers for stronger isolation
- **macOS** — Seatbelt (`sandbox-exec`) is deprecated-but-functional; containerization via Apple's `container` framework or Docker
- **Windows** — AppContainer / Job Objects
- **Strongest tier** — microVM (Firecracker / libkrun) or fully remote execution in our hosted runtime

Do not build this early. Do make the IR's `capabilities` field expressive enough that it can be added without a format break.

---

## 5. Scanner

Split by where it runs, because the constraints differ:

**Local (in the binary, offline, free):** Go. Regex + heuristics + unicode normalization (homoglyph and zero-width detection via `golang.org/x/text/unicode`), entropy checks for embedded blobs, capability inference from `mcp.json`, typosquat distance against a bundled corpus. Must work with no network — that's what makes it deployable in regulated environments.

**Cloud (control plane, paid):** LLM-based classification of skill content for injection patterns, plus deeper static analysis of MCP server source. Python is defensible here for the ML tooling; Go is defensible for keeping one language. Decide when the classifier exists, not now. Use Claude (Opus/Sonnet 5) for classification — this is an adversarial-text problem where model quality directly determines false-negative rate.

**Third-party engines:** Semgrep for MCP server source analysis (note: OSS engine is LGPL-2.1 — invoke as a separate binary, never link).

**Output format: SARIF.** Non-negotiable. It drops straight into GitHub code scanning, GitLab, and every enterprise security dashboard, which removes an integration objection from every sale.

---

## 6. Control plane

| Layer | Choice | Why |
|---|---|---|
| API | **Go** — `chi` + `sqlc` + `pgx` | Shares the IR, scanner, and policy code with the CLI. `sqlc` gives typed queries without an ORM |
| Database | **Postgres** | One database until proven otherwise. Partition the audit tables early |
| Audit/telemetry at volume | **ClickHouse** (Phase 3+) | Only when tool-call volume makes Postgres hurt. Not before |
| Queue / background jobs | **River** (Postgres-backed, Go) | Avoids adding Redis. Fewer moving parts is a feature when you're 6 people |
| Similarity search | `pgvector` | For typosquat and skill-similarity detection — an extension, not a new datastore |
| Policy engine | **CEL** (`google/cel-go`) | Embeddable, fast, auditable, no separate runtime, and it evaluates identically in the CLI and the server. Consider OPA/Rego only if customers demand complex policy authoring |
| Auth | WorkOS behind an interface; Zitadel or Keycloak for self-host | **Never hard-depend on a SaaS auth vendor** — self-hostability is a Phase 3 requirement and retrofitting it is painful |
| Web UI | **Next.js + TypeScript + Tailwind + shadcn/ui** | Only place TypeScript belongs. API-first: the UI is a client of the public API, nothing more |
| Observability | OpenTelemetry → Grafana/Prometheus or Datadog | OTel keeps the backend swappable |
| Deployment | Containers on ECS/Cloud Run/Fly early; Kubernetes only when a customer's self-host demands it | Kubernetes at seed stage is a self-inflicted wound |

---

## 7. Conformance harness

The hardest infrastructure in the project, and the most strategically valuable ([03 §7](03-architecture.md)).

- **CLI-based clients** (Codex, Claude Code, Gemini CLI) — Docker, straightforward.
- **GUI/editor clients** (VS Code, Cursor, Windsurf, Zed) — harder. VS Code and its forks support headless extension-host test runs; use that where possible. Otherwise GitHub Actions runners with a virtual display (Xvfb on Linux), or macOS/Windows runners for platform-specific behavior.
- **Orchestration** — GitHub Actions matrix: N clients × M versions × K canonical test plugins. Nightly.
- **Output** — JSON published to a static site; the web matrix is generated from it.

Budget real engineering time for this. It will be flaky, and it is worth fixing anyway.

---

## 8. Docs & web

Astro Starlight or Fumadocs for docs (static, fast, good DX). The compatibility matrix is a statically generated page fed by the harness's JSON output. Marketing site can be the same Astro project — no reason for two.

---

## 9. License hygiene (enforce in CI from week one)

Per [06](06-business-model-and-acquisition.md), AGPL/SSPL contamination in anything commercial is a deal-killer that is trivially avoidable now and expensive to unwind later.

- ✅ Apache-2.0 / MIT / BSD: cobra, cel-go, OPA, cosign, oras-go, pgx, chi, River, Zitadel, Keycloak
- ⚠️ Deploy-only, never linked: Grafana (AGPL)
- ⚠️ Separate-binary only, never linked: Semgrep OSS (LGPL-2.1)
- ❌ Avoid entirely in commercial code: anything AGPL or SSPL

Run an automated license scanner in CI and fail the build on policy violation. Require DCO sign-off or a CLA on the very first external contribution.

---

## 10. Explicitly not using (and why)

| Not using | Reason |
|---|---|
| Kubernetes (early) | Ops burden that buys nothing at this stage |
| Microservices | One Go binary per role is plenty until ~30 engineers |
| A custom policy DSL | CEL exists, is auditable, and nobody wants to learn our DSL |
| An ORM | `sqlc` gives type safety without the abstraction tax |
| Redis (early) | River on Postgres removes a whole component |
| Electron / a desktop GUI | The CLI *is* the UI. A GUI would be a distraction and an attack surface |
| A custom registry | OCI already solved this, and enterprises already run one |
| Blockchain-anything for provenance | Rekor is the answer; this is not a hypothetical concern in this space |

---

## 11. Skills to hire for

| Role | When | Must have |
|---|---|---|
| Go systems engineer | #1 | CLI/distribution experience, cross-platform, filesystem edge cases |
| Security researcher | Phase 2 | Supply chain + prompt injection; this person generates the marketing |
| Full-stack (Go + Next.js) | Phase 3 | Control plane and UI |
| DevRel / technical writer | Phase 2 | Owns the compatibility matrix and the ecosystem relationships |

A small team can carry this: the OSS core is achievable by 1–2 strong Go engineers, and the control plane is a conventional SaaS backend. The scarce hire is the security researcher — that's the one worth overpaying for.
