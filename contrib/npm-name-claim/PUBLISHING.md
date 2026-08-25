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

## Publishing it

npm requires a one-time code for publishing, so this is a manual step:

```bash
cd contrib/npm-name-claim
npm publish --otp=XXXXXX
```

Verify:

```bash
npm view agentbridge
```

## Replacing it with the real package

When the first release is tagged and its artifacts exist:

1. Set `version` in [`npm/package.json`](../../npm/package.json) to match the
   release tag — `install.js` derives the download URL from it, so a mismatch
   downloads a URL that does not exist.
2. `cd npm && npm publish --otp=XXXXXX`

`0.1.0` supersedes this `0.0.1` automatically; nothing here needs deleting.
