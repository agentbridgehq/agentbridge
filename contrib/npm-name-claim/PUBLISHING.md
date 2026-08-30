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

## Superseded

This placeholder existed only to hold the name while there was no release to
point at. **v0.1.0 is released**, so publish the real package instead — see
[`npm/PUBLISHING.md`](../../npm/PUBLISHING.md). A `0.1.0` needs no `0.0.1`
beneath it, and publishing one now would only put a version on the registry
that installs nothing.

Keep this directory until the name is actually claimed: if publishing the real
package is delayed and somebody else takes `agentbridge`, this is still the
fastest thing to put up.
