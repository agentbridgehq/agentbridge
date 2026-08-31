# Issue draft — two cases where following the published schema makes a client non-conformant

*Post to: https://github.com/agentplugins/agent-plugins-spec/issues*

---

**Title:** `plugin.schema.json` rejects two manifests that §5.2 and §8.1 require clients to accept

The specification requires a client to tolerate two specific manifest problems.
The published schema treats both as validation failures. An implementer who
validates against `plugin.schema.json` and rejects on failure — the obvious
reading — produces a client that is non-conformant in both cases.

We are not proposing a change to the contract. The normative text is clear and,
we think, right. The issue is that the schema cannot express it, and nothing
currently says so.

## Case 1 — unknown top-level fields

§5.2:

> If `plugin.json` contains any other top-level field, it does not conform to
> the schema. Clients **MUST** report and ignore each unknown field and **MUST**
> continue loading the plugin if the manifest otherwise satisfies this section.

The schema sets `"additionalProperties": false`, so an unknown field is a
validation error indistinguishable from a missing `name`. The text anticipates
this — "it does not conform to the schema" — but a validator returns one result
for both, and the required behaviours are opposite: continue in one case, reject
the plugin in the other.

## Case 2 — a non-object `extensions`

§8.1:

> If `extensions` is not an object, the client **MUST** report and ignore the
> field and continue loading components.

The schema types `extensions` as an object, so a string there fails validation
like any other type violation — and §5.2 makes type violations fatal. Again the
two required behaviours are opposite and the validator cannot distinguish them.

## Why it is worth addressing

These are the only two non-fatal schema violations in the specification, and
both are invisible unless an implementer reads §5.2 and §8.1 closely enough to
notice they override the schema. The natural implementation — validate, reject
on failure — is wrong in exactly two cases and right everywhere else, which is
the kind of divergence that surfaces as "this plugin works in one client and not
another" long after it is introduced.

## What we did

We compile two variants of the manifest schema: the canonical one for
author-facing strict validation, and a relaxed one for loading, with
`additionalProperties` opened and the `extensions` type constraint removed. Both
violations are then detected separately and reported without rejecting the
plugin.

That works, but every implementer has to derive it independently from prose, and
the schema on disk actively suggests the wrong thing.

## Possible resolutions

1. **A note in §5.2 and §8.1** stating that the schema cannot express these
   exceptions and that validators must handle them outside schema validation.
   Editorial, no schema change, and probably sufficient.

2. **A note in the schema files themselves**, via `$comment`, pointing at the two
   sections. Schema change, so a specification release under §10.1 — but it puts
   the warning where the mistake is made.

3. **Publish a second "loader" schema** alongside the canonical one, with the two
   constraints relaxed. Most helpful to implementers, most maintenance.

We would suggest (1), possibly with (2). Happy to send a patch.

## A related ambiguity, weaker in kind

Not a contradiction, and we are not sure it needs anything — but it is the
mistake we actually made, so it may be worth a sentence somewhere.

`mcp.schema.json` sets `additionalProperties: false` on every server definition.
Writing a conformance case, we asserted that an unrecognised field inside a
server entry must be tolerated, reasoning from §5.2's forward-compatibility
rule. Our own validator rejected the case, correctly: §5.2 governs the
manifest's top level, and nothing extends it to the inside of a server.

We now think the schema is right and our reading was wrong. But §5.2 is the only
place the specification discusses tolerating unknown fields, it does not say
where that tolerance stops, and the two documents can each be read as
authoritative on the question. An implementer who generalises §5.2 one level too
far builds a client that accepts plugins the specification does not — the
opposite error to the two above, and quieter, because nothing rejects anything.

A clause in §5.2 saying the rule applies to the manifest's top-level fields and
not to nested objects would have prevented it.

## Related

The conformance checklist in Appendix A lists "Report and ignore unknown
`plugin.json` fields", which is where we eventually understood the requirement.
It might be worth cross-referencing that line from §5.2, since the checklist is
explicitly non-normative and easy to skip.
