# agentbridge

The supply chain for agent extensions — install any agent plugin into any agent
client.

```bash
npm install -g agentbridge
agentbridge clients
```

**Node is a dependency of installing, never of running.** This package downloads
a static Go binary for your platform on install and verifies its SHA-256 against
the release's signed checksum file before writing anything. After that, Node is
not involved: the command you run is the binary.

That verification is not optional. `npm` postinstall scripts are a well-worn
supply-chain vector, and a tool whose whole argument is about knowing where your
plugins came from cannot have an installer that downloads a binary and trusts it.

Other ways to install, both of which also verify:

```bash
brew install agentbridge/tap/agentbridge
curl -fsSL https://raw.githubusercontent.com/agentbridge/agentbridge/main/install.sh | sh
```

Source, documentation and issues:
https://github.com/agentbridgehq/agentbridge
