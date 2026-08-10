# Plugin author outreach

*Target: 15 third-party plugin READMEs recommending installation via
agentbridge (MVP §9 exit criteria). Personal, one at a time. A template sent to
fifty people gets fifty non-replies.*

## Who to approach, in order

1. Authors whose plugin ships **skills and MCP servers** — they are the ones
   whose users hit partial installs, so they feel the problem already.
2. Authors publishing for **more than one client** and maintaining separate
   install instructions per client.
3. Authors of plugins that need a **credential**, who currently have to write
   "put your token in the config file" and know it is wrong.

## What to actually say

Lead with something you did for them, not something you want:

> I ran your plugin through a conformance check against Agent Plugins 1.0 and
> found two things: `<specific finding>`. Full output attached — no action
> needed, just thought it was worth passing on.
>
> The tool is `agentbridge validate`, which I built while implementing the spec.
> If it is useful, `agentbridge install <your-repo>` also installs your plugin
> into every agent client someone has, including Claude Code, and tells them
> which parts each client actually took.

Attach real `agentbridge validate` output for **their** plugin. Do not send this
without running it first — a generic pitch is worth nothing, and a specific
finding is worth a reply.

## What to offer

- A one-line install instruction for their README, if they want it.
- A fix for anything the validator found, as a pull request to their repo.
- Nothing else. No partnership, no listing, no exclusivity.

## What not to do

- Do not tell an author their plugin is broken. Say what the specification
  requires and what you observed; let them draw the conclusion.
- Do not ask for a README change in the first message.
- Do not approach client vendors this way. That conversation goes through the
  [upstream contributions](../upstream/), which are about the specification and
  not about us.
