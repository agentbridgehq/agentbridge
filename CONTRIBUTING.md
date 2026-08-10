# Contributing

## Sign your commits (required)

Every commit must carry a Developer Certificate of Origin sign-off:

```bash
git commit -s -m "your message"
```

That adds a `Signed-off-by:` trailer certifying you have the right to submit the
work under this project's license ([DCO 1.1](https://developercertificate.org/)).
CI enforces it on every commit in a pull request.

This is not bureaucracy. Provenance of contributions cannot be reconstructed
later, and a repository without it becomes a problem during any acquisition or
license change. It costs one flag now.

## License

The core is Apache-2.0. By contributing you agree your contribution is licensed
under it.

**Dependencies:** no AGPL, GPL, SSPL, or BSL code may enter this repository. CI
fails the build on it (`make licenses`). If you need functionality from such a
project, invoke it as a separate binary rather than linking it, and raise it in
the PR so the boundary is reviewed.

## Development

```bash
make            # vet + test + build
make test       # go test -race -cover ./...
make cross      # build every supported platform
make licenses   # dependency license policy check
```

Requires Go 1.26 or later.

## What good looks like here

- **Adapters must never drop a component silently.** Every element that does not
  survive a translation gets a diagnostic with a stable reason code. Silent
  degradation is the failure mode this project exists to fix.
- **No network calls in the CLI's normal operation.** Embedded schemas, local
  cache. The spec also forbids fetching `$schema` at load time.
- **Path containment is not optional.** Anything that resolves a path from a
  manifest goes through `internal/safepath`.
- **Tests use real fixtures.** `testdata/` holds actual plugin directories,
  including malformed ones. Table tests over hand-built structs miss the bugs
  that matter here.

## Reporting a security issue

See [SECURITY.md](SECURITY.md). Do not open a public issue.
