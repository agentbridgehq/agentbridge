# Releasing

Releases are cut by pushing a tag. Everything else is automated, and everything
automated is validated on every pull request — a release pipeline exercised for
the first time during a release is a pipeline that fails during a release.

## Cutting a release

```bash
git tag -s v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

`-s` signs the tag with your GPG key. If you have none, `-a` makes an
annotated tag and the release is unaffected: what a consumer verifies is the
Sigstore signature and the SLSA attestation on the artifacts, and both
authenticate through the workflow's OIDC identity rather than through anything
on the releaser's machine.

The [release workflow](.github/workflows/release.yml) then runs the tests and
the licence check, builds six platforms, signs, publishes, and updates the
Homebrew tap and Scoop bucket.

## What a release publishes

| Artifact | Purpose |
|---|---|
| `agentbridge_<version>_<os>_<arch>.tar.gz` / `.zip` | The binary, plus LICENSE, NOTICE and README |
| `checksums.txt` | SHA-256 of every archive |
| `checksums.txt.sig`, `checksums.txt.pem` | Sigstore keyless signature and certificate |
| `*.sbom.json` | Software bill of materials per archive |
| Build attestation | SLSA provenance, stored with GitHub rather than in the release |

Only the checksum file is signed. Verifying it transitively covers every
artifact it lists, which keeps one signature to check rather than a dozen.

## Verifying a release

```bash
cosign verify-blob checksums.txt \
  --signature checksums.txt.sig \
  --certificate checksums.txt.pem \
  --certificate-identity-regexp 'https://github.com/agentbridgehq/agentbridge/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

```bash
gh attestation verify agentbridge_0.1.0_linux_amd64.tar.gz --repo agentbridgehq/agentbridge
```

The identity regexp matters. A signature verified without pinning the identity
only proves that somebody signed it.

## Why this is stricter than it needs to be

This project asks people to care where their plugins come from. Shipping
unsigned binaries, or an installer that downloads and executes without checking
anything, would not survive its own argument — and would not survive diligence
either (see [docs/06](docs/06-business-model-and-acquisition.md) §4).

So:

- **The installer's checksum verification cannot be turned off.** An artifact
  the checksums file does not list is refused rather than waved through.
- **`--ignore-missing` is deliberately not used.** It would let a checksums file
  that never mentions our artifact pass, which defeats the check entirely.
- **Signature verification is automatic when cosign is present**, and
  `AGENTBRIDGE_REQUIRE_SIGNATURE=1` makes its absence an error — the correct
  setting for CI and for any managed fleet.
- **The npm postinstall verifies before writing anything.** Postinstall scripts
  are a well-worn supply-chain vector.
- **Builds are reproducible-ish**: `-trimpath` and a commit-derived timestamp,
  so the same source produces the same bytes on any machine. Provenance means
  little without it.

## Turning on Homebrew and Scoop

Both publishers are commented out in [.goreleaser.yaml](.goreleaser.yaml).
GoReleaser fails the *whole* release when a publisher is configured and its
token is missing, so leaving them on would have meant the first tag — the one
release that must not fail — dying after building every artifact.

### Why a tap rather than homebrew-core

`brew install agentbridge` (no tap prefix) means being in homebrew-core, and
that has a notability bar this project cannot clear yet: **at least 30 forks, 30
watchers or 75 stars — and 90/90/225 for a self-submission**, which is what a
submission by this repository's owner would be. Repositories younger than 30
days are ineligible regardless.

So the tap is not a consolation prize, it is the only route for a while:
`brew install agentbridgehq/tap/agentbridge`. Nothing is lost by starting there —
moving to core later changes the incantation, not the formula.

### Where this stands

[agentbridgehq/homebrew-tap](https://github.com/agentbridgehq/homebrew-tap)
exists and `brew install agentbridgehq/tap/agentbridge` works today. Both
formulae so far were written by hand: GoReleaser does not backfill a tag
released before the tap existed, and it still cannot write to the tap at all
without a token. The consequence showed up immediately — v0.2.0 shipped to
GitHub and npm while the tap kept serving 0.1.0 to anyone following the README.

What is left is the automation, and it needs one credential.

### Finishing it

1. **Create a token GoReleaser can push with.** The workflow's own
   `GITHUB_TOKEN` is scoped to *this* repository and cannot write to another
   one, so this is the one place a real credential is needed. Use a
   [fine-grained personal access token](https://github.com/settings/personal-access-tokens/new)
   limited to the tap repository, with **Contents: Read and write** and nothing
   else. Give it an expiry and a calendar reminder.

2. **Add it as a repository secret** on *this* repository, named
   `HOMEBREW_TAP_TOKEN` (and `SCOOP_BUCKET_TOKEN` if you are doing Scoop):

   ```bash
   gh secret set HOMEBREW_TAP_TOKEN --repo agentbridgehq/agentbridge
   ```

3. **Uncomment the `brews:` block** in `.goreleaser.yaml` — written already,
   pointing at `agentbridgehq` — then check it before relying on it. Do this
   *after* the secret exists: GoReleaser fails the whole release when a
   configured publisher has no token, so enabling it early trades a working
   release for a broken one.

   ```bash
   goreleaser check
   goreleaser release --snapshot --clean --skip=publish,sign,sbom
   ```

4. **Cut the next release.** GoReleaser then writes `Formula/agentbridge.rb`
   into the tap on every tag, replacing the hand-written one.

### Checking it actually works

A formula that installs but does not run is the usual failure, and `brew audit`
is what catches it before a user does:

```bash
brew tap agentbridgehq/tap
brew install agentbridgehq/tap/agentbridge
brew test agentbridgehq/tap/agentbridge
brew audit --strict --online agentbridgehq/tap/agentbridge
```

`brew test` runs the formula's `test do` block, which executes
`agentbridge version` — so it exercises the binary rather than merely asserting
that a file landed.

`brew audit --strict` reports five remaining style problems, and they are worth
recognising rather than chasing: a redundant `version` line and four
`def install` definitions inside blocks. All five are inherent to the formula
GoReleaser generates, and every GoReleaser-published tap has them. They would
need addressing for a homebrew-core submission; they do not affect a tap. The
two that *were* fixable — a description opening with an article, and string
interpolation in the test block — were fixed in `.goreleaser.yaml`, so the
pipeline does not reintroduce them.

Note that Homebrew caches the tap's clone: `brew untap` followed by `brew tap`
can reuse it and audit a stale formula. `rm -rf $(brew --repository)/Library/Taps/agentbridgehq`
forces a fresh one.

## Secrets the workflow needs

| Secret | Used for |
|---|---|
| `GITHUB_TOKEN` | Provided automatically; publishes the release |

That is the whole list today, and it is deliberately short.

**Signing needs no secret.** Sigstore authenticates through the workflow's OIDC
identity, so there is no long-lived key for anyone to lose or leak.

**Publishing to npm needs no secret either.** `npm-publish.yml` uses trusted
publishing, which mints a short-lived credential from the same OIDC identity —
see [npm/PUBLISHING.md](npm/PUBLISHING.md).

**The taps are the exception, and unavoidably so.** `HOMEBREW_TAP_TOKEN` and
`SCOOP_BUCKET_TOKEN` are not read while the publishers are commented out.
Turning them on makes both required, because the workflow's own `GITHUB_TOKEN`
cannot write to a different repository. That is the only real credential in the
release path; scope it to the tap repository alone.

## Still outstanding

v0.2.0 is out on GitHub, Homebrew and npm. What has not happened yet:

- [ ] Verify the `agentbridge` name and trademark ([D-02](MVP.md)) — the module
      path, npm name, Homebrew tap and Scoop bucket all assume it.
- [ ] **Give GoReleaser `HOMEBREW_TAP_TOKEN`.** The tap exists and works, but
      cannot update itself, so both formulae so far were written by hand — and
      the tap sat a release behind until someone noticed. That is precisely the
      failure the token removes.
- [ ] Create `scoop-bucket` and uncomment the Scoop publisher.

### What the first release taught

Both were found by *running* things that had only ever been read, and neither
would have been caught by any amount of review:

- **`install.sh` named the wrong organisation**, so every install would have
  failed — on the path the README leads with. Earlier testing had used a fake
  release via `AGENTBRIDGE_BASE_URL`, which bypasses that constant entirely: the
  one variable the fake could not exercise was the one that was wrong.
- **The npm package installed a command that hung**, because the downloaded
  binary and the shim npm links onto the PATH claimed the same path — so the
  installer skipped the download and the shim spawned itself. The install
  printed success.

So: verify a release the way a user would, from a machine that has never built
this repository, and prefer the real artifact over a stand-in. A stand-in
cannot fail in the way the real thing does.
