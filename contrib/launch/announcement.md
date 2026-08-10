# Announcement draft

*For Hacker News, Lobsters, or a blog post. Trim hard before posting; the
version that works is shorter than this.*

---

**Title:** AgentBridge — install any agent plugin into any agent client

Agent Plugins 1.0 standardised the *shape of the folder* that extends an AI
agent: skills plus MCP servers in one portable directory, backed by OpenAI,
Microsoft, Amazon, Cursor and Vercel.

It deliberately defines nothing about where a plugin comes from, how it gets
installed, whether it is safe, or what it did afterwards. So the ecosystem now
has a package format with no supply chain — npm's format without npmjs.com, a
lockfile, or a scanner.

AgentBridge is a CLI that fills that in:

```bash
agentbridge install github.com/org/repo@v1.2.0
```

Installs into every agent client on the machine — including Claude Code, which
the standard does not cover — pinned to an immutable commit, content-addressed,
with secrets kept out of config files.

**The part I would want to know about as a user.** Every install prints what
each client actually received, and what it did not:

```
  ok claude-code    skills 2/2   mcp 2/2
  !! cursor         skills 0/2   mcp 1/2
       - Cursor may support skills, but its vendor has not documented where
         they are installed. We will not write to an unverified path
       ! env DEPLOY_TOKEN was not written: name suggests a credential
```

Silent degradation is this ecosystem's default failure mode. A conformant client
may support *neither* skills nor MCP servers, so "it installed fine, nothing
happens" is the most common experience there is, and nothing else tells you why.
`agentbridge doctor` exists entirely to answer it.

**Some things I found building it**, which may be more interesting than the tool:

- There is **no conformant way to give an MCP server a credential** in 1.0.0. The
  spec says env values and headers are visible package data and MUST NOT hold
  secrets, then defines no alternative. Every plugin needing one is either
  violating the spec or relying on client-specific behaviour.
- The `name` pattern in the published schema uses a regex lookahead, so it
  **cannot be compiled by Go's regexp engine at all** — the whole schema fails to
  load, not just that field.
- In two places, **following the published JSON Schema literally makes a client
  non-conformant**: the schema rejects manifests the text requires clients to
  accept.
- The one client that does *not* implement the standard carries the most, because
  it is the only one whose plugin layout its vendor documents. Conformance is not
  documentation.

I have filed the first three upstream.

**What it does not do yet.** No client has been measured against the conformance
corpus — the compatibility table says what we *write*, based on vendor docs, not
what clients do with it. The 18-case corpus is published for anyone who wants to
check their own client. Contributions of results welcome.

Apache-2.0. No telemetry, and there is a test that fails the build if anything
in the binary makes a network call.

<links>
