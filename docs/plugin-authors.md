# Writing a plugin that actually works everywhere

A guide for plugin authors, built from the conformance traps this project hit
while implementing Agent Plugins v1.0.0. Every rule below is one somebody gets
wrong, and most of them fail *silently* — the manifest validates, the client
starts, and nothing happens.

The specification is at
[agent-plugins.org](https://agent-plugins.org/specification); section numbers
here refer to it. Our per-requirement audit is in
[10 — Spec compliance](10-spec-compliance.md).

## Check your work

```bash
agentbridge validate ./my-plugin
agentbridge validate ./my-plugin --strict   # advisories become failures
```

`validate` is the mirror of the loader. The loader is deliberately forgiving,
because the specification requires a client to load what it can and report the
rest — a user should not lose a working plugin over one bad server entry. As an
author you want the opposite: everything a conformant client is obliged to
tolerate, plus the requirements that bind *you* and which therefore no client
will ever report.

It separates three things, because the specification does:

- **Violations** — a MUST. Your plugin is not conformant.
- **Advisories** — a SHOULD, or a MUST that binds the author rather than the
  client. §5.4 is emphatic that a client MUST NOT reject a plugin for a
  non-SemVer version, so that is never a violation here.
- **Notes** — tolerated, but worth knowing.

Every finding cites its clause, so you can check the claim rather than take our
word for it.

## The layout

```
my-plugin/
├── plugin.json                 required, at the root
├── skills/
│   └── summarize/
│       └── SKILL.md            one directory per skill
├── mcp.json                    optional
└── com.example.client/         optional, client-owned files
```

Component locations are **fixed** (§6.1). `plugin.json` cannot point somewhere
else, and there is no inline component configuration. A missing location is
fine; a location that exists but is the wrong kind of thing — `skills` as a
file, `mcp.json` as a directory — makes that component type invalid (§6.2).

## The traps

### Your plugin name is stricter than you think

1–64 characters, lowercase letters, digits, `-` and `.` only, starting and
ending alphanumeric, **no `--` and no `..`** (§5.5).

`acme--tools` is invalid. `acme-.tools` is valid: only the doubled forms are
forbidden, which surprises people in both directions.

### `command` is a token, not a command line

It must be a **bare executable name** or a **`./`-relative path** (§7.2.1).
Nothing else:

```json
"command": "npx"                      ✓ resolved by the platform's search rules
"command": "./bin/server"             ✓ resolved against the plugin root
"command": "/usr/local/bin/server"    ✗ absolute
"command": "bin/server"               ✗ relative but not ./-prefixed
"command": "../tools/server"          ✗ escapes the plugin root
"command": "node server.js"           ✗ one token, no shell parsing
```

**Placeholders are never expanded in `command`** (§9.2). If you bundle an
executable, use the `./` form — that is what it is for.

### Placeholders work in exactly three places

`${PLUGIN_ROOT}` and `${PLUGIN_DATA}` expand in `args`, `env` *values*, and
`cwd`. Nowhere else — not in `command`, not in `url`, not in header names or
values (§9.2, §7.2.1).

Anything else that looks like a placeholder is passed through literally, which
means a template your client happens to support will be sent to the server as
the text you typed.

### You cannot ship a credential

This one catches everyone, and the specification is unusually direct:

> Configured `env` values are visible package data, not a portable secret
> mechanism. Plugins MUST NOT embed credentials or other secrets in `env`.
> — §9.2

The same applies to headers, and `url` must contain no user information
(§7.2.1). And then:

> Agent Plugins v1 defines no OAuth configuration or portable
> credential-reference fields. — §7.2.1

**There is no conformant way to give an MCP server a credential in v1.0.0.**
Read the environment variable inside your server and document which one you
need. `agentbridge secret set` and `${secret:...}` will supply it without the
value touching a config file, but that is our extension and not portable —
depend on the environment variable, not on us.

### Reserved environment names

`env` must not contain `PLUGIN_ROOT` or `PLUGIN_DATA` (§9.2). The client
supplies both; setting them makes your server entry invalid.

### Remote servers must be HTTPS

Non-loopback endpoints require HTTPS. Plain HTTP is permitted only to
`localhost` or a loopback IP (§7.2.1). Two header entries differing only in
case make the entry invalid, since header names are case-insensitive.

### `skills/` is not searched recursively

Each *immediate* child directory containing a `SKILL.md` regular file is one
skill (§7.1). A skill nested two levels deep is not discovered, and nothing will
tell you at load time except a plugin that quietly has fewer skills than you
wrote.

Give every skill a `name` in its frontmatter. Without one, the name falls back
to the directory, which for a marketplace install can be a version string that
changes on every update.

### Unknown manifest fields are ignored, not rejected

The manifest schema is closed (§5.2): ten permitted top-level fields. An unknown
one is reported and ignored rather than fatal — which means a **typo silently
does nothing**. `agentbridge validate` surfaces these as advisories precisely
because a client will not.

Client-specific data belongs under `extensions`, keyed by a reverse-domain
namespace you control (§8).

### One bad entry does not sink the plugin

A malformed MCP server is skipped and the rest load (§7.2.2). A skill that does
not conform is skipped and the rest load (§7.1). This is deliberate and good —
but it means partial breakage looks like success. Check the diagnostics.

## Know where your plugin will be diminished

```bash
agentbridge losses
```

Some things no plugin can fix, because clients genuinely differ: Gemini CLI has
no skills mechanism, and three clients that *do* implement Agent Plugins have no
documented location for a portable package, so their users will not receive your
skills. See [clients.md](clients.md).

Design for that. A plugin whose value is entirely in its skills will be inert in
several popular clients through no fault of yours, and it is better to know
before you publish.

## Publishing

Nothing in v1.0.0 defines distribution — no registry, no naming authority, no
integrity mechanism. In practice that means a git repository:

```bash
agentbridge install github.com/you/your-plugin@v1.0.0
agentbridge install github.com/you/monorepo@v1.0.0#plugins/db
```

Tag your releases. A tag is resolved to an immutable commit before anything is
fetched, and that commit is what gets recorded — so `@v1.0.0` means the same
bytes next month, and a user's lockfile can prove it.

Use SemVer for `version` (§10.2, RECOMMENDED). And bump the **major** when your
plugin gains a capability: a version that starts executing processes or reaching
the network is a different proposition from the one before it, and the people
installing it deserve to see that in the diff.
