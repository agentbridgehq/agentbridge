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

### Setting it up

1. **Create the tap repository.** It must be named `homebrew-tap`; Homebrew
   derives `agentbridgehq/tap` from that name, and any other name will not
   resolve.

   ```bash
   gh repo create agentbridgehq/homebrew-tap --public \
     --description "Homebrew formulae for agentbridge"
   ```

   For Scoop, the same again with `scoop-bucket`.

2. **Create a token GoReleaser can push with.** The workflow's own
   `GITHUB_TOKEN` is scoped to *this* repository and cannot write to another
   one, so this is the one place a real credential is needed. Use a
   [fine-grained personal access token](https://github.com/settings/personal-access-tokens/new)
   limited to the tap repository, with **Contents: Read and write** and nothing
   else. Give it an expiry and a calendar reminder.

3. **Add it as a repository secret** on *this* repository, named
   `HOMEBREW_TAP_TOKEN` (and `SCOOP_BUCKET_TOKEN` if you are doing Scoop):

   ```bash
   gh secret set HOMEBREW_TAP_TOKEN --repo agentbridgehq/agentbridge
   ```

4. **Uncomment the `brews:` block** in `.goreleaser.yaml` — it is written and
   already points at `agentbridgehq` — then check it before relying on it:

   ```bash
   goreleaser check
   goreleaser release --snapshot --clean --skip=publish,sign,sbom
   ```

5. **Cut the next release.** GoReleaser writes `Formula/agentbridge.rb` into the
   tap on every subsequent tag. It does *not* backfill: v0.1.0 stays absent from
   the tap unless you re-run the release for that tag.

### Checking it actually works

A formula that installs but does not run is the usual failure, and `brew audit`
is what catches it before a user does:

```bash
brew tap agentbridgehq/tap
brew install agentbridgehq/tap/agentbridge
agentbridge version
brew audit --strict --online agentbridgehq/tap/agentbridge
```

The `test do` block in the formula runs `agentbridge version`, so
`brew test agentbridge` exercises the binary rather than merely asserting a file
landed.

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

v0.1.0 shipped on 2026-08-30. What has not happened yet:

- [ ] Verify the `agentbridge` name and trademark ([D-02](MVP.md)) — the module
      path, npm name, Homebrew tap and Scoop bucket all assume it.
- [ ] Publish the npm package: [npm/PUBLISHING.md](npm/PUBLISHING.md). The first
      version has to go up by hand, approved with 2FA; CI takes over after that.
- [ ] Create `homebrew-tap` and `scoop-bucket`, then uncomment the publishers.

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
