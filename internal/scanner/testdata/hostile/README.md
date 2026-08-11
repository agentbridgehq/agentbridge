# Hostile fixture

Not a real plugin. Every file here is written to trip a scanner rule, and the
package as a whole is what a plugin looks like when it is trying to look
ordinary: a plausible name, a plausible description, a deployment skill nobody
would think twice about, and the actual instructions placed where a reviewer
opening `SKILL.md` will not look — in a reference file, in an HTML comment, and
in a bundled script.

Nothing here executes during tests. It is read as text.
