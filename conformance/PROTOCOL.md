# Measuring a client

How to run the corpus against a real agent client and record what you saw.

Budget roughly an hour per client the first time. Most of that is restarting
things.

```bash
agentbridge conformance --record cursor > conformance/results/cursor-1.x.yaml
```

That writes one entry per case, every outcome pre-set to `unmeasured`, with the
observation note inline as a comment. Fill in outcomes as you go. **Leave
anything you did not personally observe as `unmeasured`** — a half-finished run
left alone reports honestly; one optimistically marked `pass` poisons the table
for everyone who reads it later.

## The loop

For each case in `cases/`:

1. Install `cases/<id>/plugin` into the client, the way a user would.
2. Restart the client, or reload its plugins.
3. Check the case's `observe` note.
4. Record `pass`, `fail`, or `unmeasured`, and note anything surprising.
5. Remove the plugin before the next case.

Step 5 matters more than it looks. Several cases deliberately use the same
plugin name, and a client that does not fully unload the previous one will
produce results that look like failures and are not.

## What to look for

Three questions, in order:

1. **Did the plugin load at all?** Cases with `loads: false` require the client
   to reject it. A client that loads a plugin with no `name` is not conformant,
   however gracefully it copes afterwards.
2. **Are exactly the expected components present?** Count the skills and servers
   the client actually offers. `007` is the one people get wrong: a client that
   searches `skills/` recursively will find three skills where the specification
   defines two.
3. **Was anything reported?** Several cases require the client to *report* a
   problem while continuing. If nothing surfaces anywhere a user would see it,
   that is worth noting even when the load behaviour is right — a silent
   degradation is the failure this whole project is about.

## Where each client loads plugins from

| Client | Documented location | Notes |
|---|---|---|
| **Claude Code** | any directory under a skills directory containing `.claude-plugin/plugin.json` | Add `.claude-plugin/plugin.json` alongside the case's `plugin.json`, or use `agentbridge install` |
| **opencode** | `~/.config/opencode/skills/`, scanned recursively for `**/SKILL.md`; extra roots via `skills.paths` | Documented, and confirmed against the binary — `opencode debug skill` lists what it loaded. A whole package can be dropped in one directory |
| **Cursor** | *unknown* | Agent Plugins launch client; where a portable package is installed is not published. **Finding this is the single most valuable outcome of a measurement session** |
| **VS Code / Copilot** | *unknown* | As above |
| **Codex** | *unknown* | As above |
| **Gemini CLI** | n/a | No skills mechanism. MCP-only cases still apply, via `~/.gemini/settings.json` |

For the three marked *unknown*: try the client's own install UI or command first
and watch where it writes. If you find the path, that is a bigger result than the
conformance run itself — it turns `undocumented` into real skill support in
[docs/clients.md](../docs/clients.md), and it is worth opening an issue about
before finishing the rest of the cases.

**Prefer the client's own tooling to your own eyes where it exists.** Codex
reports MCP state with `codex mcp list`, and opencode reports both with
`opencode mcp list` and `opencode debug skill` — an answer from the vendor's
binary is worth more than a screenshot, and it is what separates a measured
result from an impression. Cursor and VS Code have no equivalent, which is
exactly why they are the two still unmeasured.

## MCP-only clients

A client that supports MCP servers and not skills can still be measured. Run the
cases that involve `mcp.json` — 009 through 018 — and record the skills-only
cases as `unmeasured` with a note saying why. That is a legitimate, complete
result for that client, not a partial one.

## Recording a failure

A failure is a bug report, not a verdict. The likeliest explanations, in order:

1. **The corpus is wrong.** Open an issue here first.
2. **The installation was wrong** — the plugin did not land where the client
   looks, or an earlier case was still loaded.
3. **The client is wrong.**

Only after ruling out the first two is it worth raising with the vendor, and it
is worth saying plainly what you observed rather than asserting non-conformance.
We would much rather withdraw a claim than defend one.

## Contributing the result

Open a pull request adding your filled-in file to `results/`. Include the client
version and the platform: behaviour changes between releases, and a result
without a version cannot be reproduced or retired later.
