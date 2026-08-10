# Issue draft — the `name` pattern cannot be compiled by RE2-based validators

*Post to: https://github.com/agentplugins/agent-plugins-spec/issues*

---

**Title:** `plugin.schema.json` `name` pattern uses a lookahead, blocking Go and other RE2-based validators

The `name` pattern in `schemas/1.0.0/plugin.schema.json` is:

```
^(?!.*(?:--|\.\.))[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$
```

The leading negative lookahead is valid ECMA-262, which is what JSON Schema
specifies. It cannot be compiled by [RE2](https://github.com/google/re2), which
does not support lookaround by design and is the engine behind Go's `regexp`
package.

## Why this is more than an inconvenience

The failure is at **schema compile time**, not validation time. A Go validator
does not fall back or skip the constraint — it refuses to load
`plugin.schema.json` at all, and every other rule in the file goes with it. The
first symptom for an implementer is that manifest validation does not work, with
an error naming a regex rather than anything about plugins.

Go is a common choice for CLI tooling in this space, so this is likely to be
encountered repeatedly and independently. Each implementer then has to work out
that the fix is to strip the pattern and reimplement §5.5 in code — which is
what we did, but the constraint is now enforced somewhere a reader of the schema
cannot see.

## Reproducing

```go
import "github.com/santhosh-tekuri/jsonschema/v6"
// Compiling schemas/1.0.0/plugin.schema.json fails with:
//   invalid regex: error parsing regexp: invalid or unsupported Perl syntax: `(?!`
```

Any RE2-based validator behaves the same way.

## Possible resolutions

The rule itself is fine and worth keeping. Only its expression is the problem.

1. **Express it without lookaround.** The constraint — lowercase alphanumerics,
   hyphens and periods, alphanumeric at both ends, no `--` or `..` — is
   expressible as an alternation, at some cost to readability. This changes the
   schema and so would need a specification release under §10.1.

2. **Leave the pattern and note the limitation.** A sentence in §5.5 saying the
   pattern is ECMA-262 and that implementers on RE2 engines must enforce the
   repetition rule in code would turn a confusing failure into an expected one.
   This is editorial and needs no schema change.

3. **Drop `pattern` and make §5.5 the sole normative statement.** The
   specification text already governs where the two conflict (§5.2), so the
   pattern is a convenience rather than the source of truth.

We have no strong preference between these and are happy to send a patch for
whichever the maintainers favour. (2) seems cheapest and loses nothing, given
§5.2 already makes the prose authoritative.

## Context

Found while implementing a loader against 1.0.0. Worth noting for anyone else
hitting it: the rule forbids only the doubled sequences `--` and `..`. The mixed
forms `-.` and `.-` are permitted, which surprised us in both directions and is
easy to get wrong when reimplementing the pattern by hand.
