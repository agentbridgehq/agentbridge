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

| Client | Location | How it was established |
|---|---|---|
| **Claude Code** | any directory under a skills directory containing `.claude-plugin/plugin.json` | Vendor documentation |
| **opencode** | `~/.config/opencode/skills/`, scanned recursively for `**/SKILL.md`; extra roots via `skills.paths` | Vendor documentation, then confirmed against the binary — `opencode debug skill` lists what it loaded |
| **VS Code / Copilot** | User-level `~/.agents/skills`, `~/.copilot/skills`, `~/.claude/skills`; workspace-level `.agents/skills`, `.github/skills`, `.claude/skills`. Extra roots via the `chat.agentSkillsLocations` setting, whose default is that same table. **Scanned one level deep**: each immediate child directory is checked for a `SKILL.md` and nothing below that is looked at | **Confirmed.** The scan depth was read off the scanner, which iterates the root's children and joins `SKILL.md` onto each; a package registered through `chat.agentSkillsLocations` was then listed by VS Code when it was asked. `agent-plugins` was a wrong turn — `agentPluginsHome` sits beside `mcp.json` and looks right, but it is the *plugin* home and holds no skills |
| **Cursor** | `~/.cursor/plugins/`, with `local/` for local installs and `cache/<publisher>/<name>/<sha>/` for fetched ones. A package is marked by `.cursor-plugin/plugin.json`, which points at its own components: `"skills": "./skills/"`, `"mcpServers": "./.mcp.json"` | **Confirmed.** Read off a real installed plugin, then a package was placed there and Cursor was asked what it had loaded — it listed the skill and named `~/.cursor/plugins/local` itself |
| **Codex** | `~/.codex/skills/`, scanned recursively; a package dropped in whole is found. `.codex-plugin/plugin.json` marks a Codex plugin and declares `"skills": "./skills/"`. MCP via `~/.codex/config.toml` | **Confirmed.** `codex debug prompt-input` renders the model-visible skill list with a source locator for each — it named the installed file back. No model call needed |
| **Gemini CLI** | n/a | No skills mechanism. MCP-only cases still apply, via `~/.gemini/settings.json` |

Cursor also scans other ecosystems' directories — `~/.claude/skills`, `~/.codex/skills`, `~/.grok/skills`, `~/.agents/skills`, `~/.claude/plugins` — so a plugin installed for Claude Code may already be visible there. opencode does the same. That is worth knowing before concluding a client "supports" something: it may be reading someone else's directory.

### VS Code needed a different install shape

Every other client that takes skills scans recursively, so a plugin package is
dropped in whole and each `SKILL.md` beneath it is found. VS Code looks exactly
one level down, so a package installed the way the others take it is invisible
to it.

Flattening each skill into `~/.copilot/skills/<skill>/SKILL.md` would have
matched the scanner, but the namespace is flat: two plugins shipping a `deploy`
skill collide, and the package stops being a unit that can be removed as one.

Registering the package's own `skills/` directory through
`chat.agentSkillsLocations` inverts the problem instead. Its immediate children
are exactly the skills, which is the layout the one-level scan wants, and the
package stays intact. That is what the adapter does, and it mirrors what
opencode's `skills.paths` already does.

Two constraints worth carrying to any client with a similar setting: the key
pattern **rejects absolute paths** while permitting a leading `~/`, and a
rejected key is dropped in silence — the skills simply never appear. And the
setting lives in `settings.json` while MCP lives in `mcp.json`, so a receipt has
to record two configuration files or one of them becomes unremovable.

### Verifying a path you have found

A probe plugin can be placed in each location; if the client lists a skill
called `agentbridge-probe`, the path is confirmed and that client stops being
`undocumented`.

```bash
# VS Code
mkdir -p ~/Library/Application\ Support/Code/User/agent-plugins/agentbridge-probe/skills/agentbridge-probe
# Cursor
mkdir -p ~/.cursor/plugins/local/agentbridge-probe/{.cursor-plugin,skills/agentbridge-probe}
```

Each needs a manifest (`plugin.json` for VS Code, `.cursor-plugin/plugin.json`
for Cursor, the latter with `"skills": "./skills/"`) and a `SKILL.md` whose
frontmatter names the skill. Restart the client, open its chat surface, and ask
what skills are available.

Removing them is `rm -rf` on the two directories above; nothing else is touched.

### Why the VS Code path above is still marked unverified

Finding a path is not the same as watching a client use it, and this project does
not write to a location it has not seen work. Cursor's was established by reading and then confirmed by asking the client
directly, which is the standard. VS Code's has only been read: it is a desktop
application with no way to ask it what it loaded, so the check is a person
opening it.

A cautionary note from the attempt. The Copilot CLI bundled inside VS Code
*does* have that read-back — `copilot plugin install`, `copilot plugin list`,
`copilot skill list`, `copilot mcp list` — and all 18 cases were run against it.
It produced a clean-looking 8 pass, 10 fail.

**That table was discarded, because it was measuring the wrong thing.** The
Copilot CLI's plugin system is not VS Code's Agent Plugins support: it reads
`skills/` and `mcp.json` but contains no reference to `plugin.json` anywhere in
its bundle, and none to the specification. Most of its "failures" were simply a
system that never claimed to implement the manifest being judged against one.

Publishing it would have produced exactly the artefact this corpus exists to
prevent — a precise-looking compatibility claim about software nobody had
actually measured. If you run the corpus, check first that the thing answering
you is the thing you meant to ask.

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
