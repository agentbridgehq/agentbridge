# MCP configuration corpus

Ten cases about MCP servers as clients actually receive them: through their own
configuration files, not inside a plugin package.

## Why this is separate from the plugin corpus

The [plugin corpus](../README.md) points a client at a plugin directory and asks
what it made of it. Running it against four clients established that **no client
reads a package's `mcp.json`**. Servers reach Cursor through `~/.cursor/mcp.json`,
Codex through `config.toml`, opencode through `opencode.json`, VS Code through
its own `mcp.json`. Nine cases were therefore recorded `unmeasured` — the
question was never delivered to the thing being asked.

This corpus delivers it. Each case is a portable server declaration; the harness
translates it into the client's own dialect and writes it where that client
looks. What is measured is the path a real plugin takes.

## What a result means, and the two halves it has

Every case has two answers, and conflating them would waste the corpus:

- **Did the translation carry it?** A portable declaration has to become
  `servers` for VS Code, `mcpServers` for Cursor, `mcp_servers` for Codex, `mcp`
  with `type: local` for opencode. A case that never reaches the file is a
  translation result.
- **Did the client accept it?** Given a correctly written entry, does the client
  load it, refuse it, or take it and quietly never connect?

Some cases are expected to stop at the first half. `M07-insecure-remote-url` is
one: AgentBridge refuses to write a plain-HTTP remote server at all, so the
client is never given the chance to get it wrong. That is the tool working, and
it is recorded as such rather than as a client pass.

## The cases

| | Asks |
|---|---|
| M01 | a stdio server reaches the client at all — the baseline |
| M02 | a streamable-http server survives four different spellings of the same transport |
| M03 | an sse server is either supported or refused clearly, not accepted and left dead |
| M04 | one malformed entry does not take the rest of the file with it |
| M05 | an unrecognised field inside a server entry is ignored rather than fatal |
| M06 | an empty server set is not an error |
| M07 | plain HTTP to a remote host is refused (§7.2.1) |
| M08 | plain HTTP to loopback is allowed — the counterpart to M07 |
| M09 | a server may not set `PLUGIN_ROOT` or `PLUGIN_DATA` (§9.2) |
| M10 | the launched subprocess actually receives them (§9.1) |

## M10 is the one that needed a new tool

Every other case can be answered by reading configuration. §9.1 cannot: it
requires the *process* to receive `PLUGIN_ROOT` and `PLUGIN_DATA`, and a client
that writes them into a file it then fails to expand looks correct and is not.

So [`probe/`](probe) is an MCP server that records the environment it was
started with to `$AGENTBRIDGE_PROBE_OUT`, then speaks enough of the protocol to
stay connected — it has to stay connected, because a server that exits
immediately is indistinguishable from one that was never launched.

Build it with `go build ./conformance/mcp/probe`.

## Results so far (2026-08-30)

| Client | pass | fail | unmeasured |
|---|---|---|---|
| opencode 1.18.3 | 10 | 0 | 0 — [results](../results/mcp-opencode.yaml) |
| Codex 0.144.5 | 8 | 0 | 2 — [results](../results/mcp-codex.yaml) |

Every case that could be delivered was handled correctly by both clients. The
two unmeasured are Codex: it declines the legacy sse transport, so M03 never
reached it, and `codex mcp list` reports configured servers without launching
them, so M10's question about the process environment could not be asked.

**M10 passed on opencode, which is the first time §9.1 has been checked against a
running process** rather than against a file. opencode launched `./probe`, and
the probe recorded `PLUGIN_ROOT` and `PLUGIN_DATA` in its own environment, both
absolute and correct, with `cwd` defaulted to the plugin root as §7.2.1
requires.

### Writing the corpus found two bugs in the corpus

Both caught by `agentbridge validate` before any client saw them, and both worth
recording because they are the errors a first draft of this kind makes:

- **M05 asserted the wrong thing.** It expected an unrecognised field inside a
  server entry to be tolerated, on the strength of §5.2. But §5.2 governs the
  manifest's top level; the published MCP schema sets `additionalProperties:
  false` on every server definition, so an unknown key there makes that entry
  invalid. The case now tests isolation instead — a different kind of
  invalidity from M04's missing command.
- **M10 used `${PLUGIN_ROOT}/probe` as a command.** A stdio command must be a
  bare executable name or a plugin-relative path beginning with `./`. The
  placeholder form is neither, and the entry was refused.

A corpus is a claim about what the specification requires. Getting two of ten
wrong on the first pass is the argument for running it against an implementation
that already enforces the rules, rather than shipping it and waiting.

## Running it

Point each case at a client the way that client takes servers, then ask the
client what it has. The clients that can answer without a person watching:

```bash
codex mcp list          # and `codex debug prompt-input` for skills
opencode mcp list       # reports `connected`, meaning it actually launched
```

Cursor and VS Code have no equivalent; there the answer is a person opening the
client. Record results as YAML beside the plugin corpus results, in
[`../results/`](../results), with `mcp-` prefixed to the client name.
