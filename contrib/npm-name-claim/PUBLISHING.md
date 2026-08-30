# Claiming the npm name

This directory is **not** the shipped npm package — that is [`npm/`](../../npm),
which downloads and verifies a released binary. This is a deliberate
placeholder whose only job is to hold the name `agentbridge` on the registry
until there is a real release to publish.

## Why it is separate

The real package runs a `postinstall` that downloads a binary from a GitHub
release. Publishing it before a release exists would give anyone who typed
`npm i -g agentbridge` a failed install. This placeholder has **no `bin` and no
`postinstall`**, so it cannot fail — it installs two files that explain where
the project is and how to build it.

## Abandoned — the unscoped name cannot be had

This placeholder existed to hold `agentbridge` on the registry. **npm will not
issue that name to anyone.** A publish is rejected with 403: the name is too
similar to `agent-bridge`, an existing package, and npm compares names with
punctuation stripped. That is typosquatting protection working as intended, and
it is not appealable from the CLI.

The natural scope was not available either: `@agentbridge` belongs to an
unrelated "AgentBridge framework" publishing React and Angular SDKs.

So the shipped package is **`@agentbridgehq/agentbridge`**, matching the GitHub
organisation — see [`npm/PUBLISHING.md`](../../npm/PUBLISHING.md). This
directory is kept only as the record of why, and nothing here should be
published.

Worth knowing for [D-02](../../MVP.md): five published packages carry this name,
several of them building adapters for the same clients this project targets.
The name is crowded, and that is a discoverability problem rather than a
registry one.
