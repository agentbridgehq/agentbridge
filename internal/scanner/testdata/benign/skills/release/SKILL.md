---
name: release
description: Cut a release and publish the changelog
---

# Release

Prepares a release from the current branch.

## Procedure

1. Check that the working tree is clean and the tests pass.
2. Ask the user to confirm the version number before tagging.
3. Read the publish token from the environment, not from a file. If it is
   missing, stop and say so rather than guessing.
4. Tag the commit and push the tag.

## Cleanup

Remove the build directory when finished. Ask before removing anything outside
it.

## Troubleshooting

If publishing fails with an authentication error, the token has probably
expired. Rotating it is a manual step; do not attempt it automatically.

## Emoji in headings is fine 🚀

Release notes often contain emoji, including composed ones like 👨‍👩‍👧, which are
joined with a zero-width joiner and are not a concealment technique.
