# Benign fixture

This is the more important of the two fixtures.

A scanner that misses a hostile plugin has failed once. A scanner that fires on
an ordinary one gets muted, and a muted scanner produces the appearance of
coverage while checking nothing — so it fails on every plugin after that,
invisibly.

So this package is written to be exactly as awkward as real plugins are:

- It deletes things, because release tooling deletes things.
- It talks about credentials, because release tooling reads tokens.
- It contains a script that runs commands, because skills ship scripts.
- Its prose is written in more than one script, and uses the zero-width
  characters those scripts require.

None of that may produce a finding. The test asserts zero.
