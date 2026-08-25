# Releasing

Releases are cut by pushing a tag. Everything else is automated, and everything
automated is validated on every pull request — a release pipeline exercised for
the first time during a release is a pipeline that fails during a release.

## Cutting a release

```bash
git tag -s v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

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
gh attestation verify agentbridge_0.1.0_linux_amd64.tar.gz --repo agentbridge/agentbridge
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

## Secrets the workflow needs

| Secret | Used for |
|---|---|
| `GITHUB_TOKEN` | Provided automatically; publishes the release |
| `HOMEBREW_TAP_TOKEN` | Push access to `agentbridge/homebrew-tap` |
| `SCOOP_BUCKET_TOKEN` | Push access to `agentbridge/scoop-bucket` |

Signing needs no secret: Sigstore authenticates through the workflow's OIDC
identity, so there is no long-lived key for anyone to lose or leak.

## Before the first release

- [ ] Verify the `agentbridge` name and trademark ([D-02](MVP.md)) — the module
      path, npm name, Homebrew tap and Scoop bucket all assume it.
- [ ] Create the `homebrew-tap` and `scoop-bucket` repositories.
- [ ] Publish the npm package name.
- [ ] Run one release against a prerelease tag and verify it end to end as a
      user would, from a machine that has never built this repository.
