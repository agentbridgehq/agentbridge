# agentbridge

**The supply chain for agent extensions.** One command installs an agent plugin
into every AI coding assistant you use — with a lockfile, secrets kept off disk,
and a scanner that reads what the plugin tells your agent to do.

## This version is a placeholder

`0.0.1` reserves the name. **It installs no binary and provides no command.**
The first working release will be `0.1.0`, and this package will then download
a signed, checksum-verified binary for your platform.

Until then, build from source — Go 1.26, no runtime dependencies:

```bash
git clone https://github.com/agentbridgehq/agentbridge
cd agentbridge
make
sudo install -m 0755 ./agentbridge /usr/local/bin/
```

## What it does

- **Installs once, everywhere.** Claude Code wants a directory, Cursor wants
  JSON, Codex wants TOML. One command handles all of them, and reports exactly
  what each client took *and what it dropped, with a reason*.
- **Pins what you installed.** A tag resolves to an immutable commit or content
  digest before anything downloads, recorded in a lockfile you commit.
- **Reads the instruction text.** A `SKILL.md` is loaded into a model that holds
  your credentials and does what the text says. No SCA tool, SAST scanner or EDR
  agent inspects it. This one does.
- **Keeps credentials off disk.** Tokens live in your OS keychain and are
  injected when the server launches.

Apache-2.0 · https://github.com/agentbridgehq/agentbridge
