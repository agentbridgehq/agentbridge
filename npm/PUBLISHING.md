# Publishing to npm

This is the real package: `npm i -g agentbridge` installs a tiny shim, and a
postinstall downloads the released binary for your platform and **verifies its
checksum before writing it**.

The placeholder in [`contrib/npm-name-claim`](../contrib/npm-name-claim) existed
only to hold the name while there was no release to point at. There is one now,
so publish this instead — `0.1.0` is a real version and needs no placeholder
beneath it.

---

## The one-time problem: npm cannot bootstrap itself

Publishing from CI uses **trusted publishing** — OIDC, no token, no 2FA prompt.
But a trusted publisher is configured on a package's settings page, and there is
no settings page until the package exists.

So **the first publish must come from a laptop, with a 2FA code.** Everything
after it can be tokenless. This is a known npm limitation, not a
misconfiguration ([npm/cli#8544](https://github.com/npm/cli/issues/8544)).

---

## 1. First publish, by hand

```bash
npm install -g npm@latest        # trusted publishing later needs >= 11.5.1
npm login                        # opens a browser; the session expires, so expect to redo this
npm whoami                       # confirms it took
```

Check the version matches the release you are publishing against — `install.js`
builds the download URL from it, so a mismatch produces a package whose
postinstall 404s:

```bash
node -p "require('./package.json').version"    # must equal the release tag without the v
```

Then, from this directory:

```bash
npm publish --access public --otp=123456
```

`--otp` is the six-digit code from your authenticator app. It is valid for about
30 seconds, so read it when you are ready to press enter rather than before.

**Verify what landed**, rather than trusting the success message:

```bash
npm view agentbridge version
cd $(mktemp -d) && npm init -y >/dev/null && npm i agentbridge --foreground-scripts
./node_modules/.bin/agentbridge version
```

`--foreground-scripts` matters: npm hides postinstall output by default, and the
postinstall is the part that can fail.

---

## 2. Switch to trusted publishing, so there is never a token

On <https://www.npmjs.com/package/agentbridge/access>, find **Trusted Publisher**
and choose GitHub Actions:

| Field | Value |
|---|---|
| Organization or user | `agentbridgehq` |
| Repository | `agentbridge` |
| Workflow filename | `npm-publish.yml` |
| Environment | leave empty |

The workflow filename must match
[`.github/workflows/npm-publish.yml`](../.github/workflows/npm-publish.yml)
exactly. **npm does not validate any of this when you save it** — a typo here
surfaces as an authentication failure on the next publish, pointing at
credentials rather than at the name you mistyped.

While you are on that page, set **Require two-factor authentication and disallow
tokens**. With OIDC doing the publishing there is no reason for a long-lived
token to exist, and npm is
[restricting token-based 2FA bypass](https://gh.io/npm-gat-bypass2fa-deprecation)
anyway.

---

## 3. Every publish after that

Bump `version` in [`package.json`](package.json) to match the release tag,
commit, then publish a GitHub release — the workflow runs on
`release: published`, or on demand from the Actions tab.

It refuses to publish if the package version and the release tag disagree, or if
any artifact the postinstall would download is missing. Both checks exist
because an npm version number cannot be reused once published: a broken `0.1.1`
is spent, and the fix has to be `0.1.2`.

Trusted publishing also attaches **provenance** automatically — an attestation,
signed by npm, that these bytes were built from this commit by this workflow.
That is the same claim this project asks people to want from their plugins, so
it should hold for the package itself.

---

## Why the binary is in `vendor/` and not `bin/`

`bin/agentbridge` is the shim npm links onto your PATH. The downloaded binary
used to land on that exact name, which meant the installer's "already present?"
check saw the shim and skipped the download, and the shim then found "the
binary" — itself — and spawned it, recursing until it was killed. `npm i -g`
reported success and produced a command that hung the first time it ran.

Both the shim and the installer now ask `platform.js` where the binary lives, so
they cannot disagree again, and two tests in
[`platform.test.js`](platform.test.js) keep it that way.

---

## A note if you use nvm

Two things behave differently, and both bite quietly:

- **Your npm login is global.** `npm login` writes the auth token to
  `~/.npmrc`, not into a Node version, so it survives `nvm use` and you do not
  re-authenticate when switching.
- **npm itself is per-version.** `npm install -g npm@latest` upgrades npm only
  for the Node you had active. Switch versions and you are on whatever npm that
  one shipped with — which for Node 20 is npm 10.

There is an [`.nvmrc`](.nvmrc) here so `nvm use` in this directory selects a
Node new enough to publish. Check before publishing rather than after:

```bash
node --version && npm --version
```

This only matters for a **local** publish. Trusted publishing runs on the CI
runner, whose Node and npm come from the workflow, so nothing about your
machine affects it.
