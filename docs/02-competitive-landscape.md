# 02 — Competitive Landscape

*Researched 2026-08-10. Treat vendor claims as claims; re-verify before any GTM doc quotes them.*

## 1. Map of the territory

```
                      governs INSTRUCTIONS (skills)
                                  ▲
                                  │
                    ┌─────────────┼─────────────┐
                    │  AGENTBRIDGE (target)     │
                    │  cross-client, endpoint   │
                    └─────────────┼─────────────┘
                                  │
 per-client ◄──────────────────────┼──────────────────────► cross-client
                                  │
   Cursor / VS Code / Kiro /      │      MCP gateways: Obot, Arcade,
   ChatGPT native plugin UIs      │      TrueFoundry, AWS OSS gw+registry,
                                  │      Docker MCP
                                  ▼
                      governs TRAFFIC (tool calls)
```

Two axes that matter:
- **Per-client vs. cross-client** — who can see all of a developer's agents at once.
- **Traffic vs. instructions** — whether the product ever inspects a `SKILL.md`.

**Nobody currently occupies the top-right quadrant.** That is the position to take.

## 2. Category by category

### 2.1 Public registries / discovery

| Player | What it is | Overlap | Read |
|---|---|---|---|
| **Smithery** | The de-facto public discovery engine for MCP servers | Discovery only | Partner, not competitor. Index them. |
| **Official MCP Registry** (Anthropic/GitHub/Microsoft/PulseMCP) | Metaregistry of *metadata* — no code, no binaries | Naming + discovery | Critical: it explicitly holds no artifacts. Verification, hosting, policy remain open. |
| **PulseMCP** | Catalog/directory | Discovery | Data source. |

**Implication:** discovery for MCP is solved-ish and low-margin. Discovery for *plugins* (skills + MCP bundled) is unsolved but will likely commoditize too. Do not build the company on a catalog.

### 2.2 Enterprise MCP gateways

| Player | Shape | Strength | Blind spot |
|---|---|---|---|
| **Obot** | OSS + commercial gateway | Broad enterprise feature set | MCP traffic only |
| **Arcade.dev** | Gateway + auth-heavy runtime | Best-in-class tool auth/OAuth | MCP traffic only |
| **TrueFoundry** | Platform play, registry + gateway | Existing ML-platform accounts | Platform-bundled, not agent-native |
| **AWS `mcp-gateway-registry`** (agentic-community, AWS-blogged) | OSS gateway + registry, Keycloak/Entra | Free, credible, AWS-adjacent | OSS; AWS-shaped; MCP traffic only |
| **Docker MCP catalog/toolkit** | Containerized MCP distribution | Distribution + isolation primitives | Container-centric; not skills; not cross-client config |

The category consensus, in their own framing: MCP is the wire format; the gateway adds authn/authz, audit, rate limits, credential brokerage, and discovery of approved servers.

**Where they are all identical, and all wrong for this problem:** they sit *between agent and tool*. They see a tool call. They never see:
- which skills are loaded into the agent's context,
- what is installed on a given developer's machine,
- clients that don't route through them (every local stdio server, every skill, every agent someone configured by hand).

An MCP gateway is a network control. What's missing is an **endpoint control**.

### 2.3 Client-native plugin management

Cursor, VS Code/Copilot, ChatGPT/Codex, Kiro each ship their own install UX — and each is a launch client for Agent Plugins. Claude Code has its own plugin + marketplace mechanism and is *not* an Agent Plugins launch client.

- These are the **distribution partners and the ceiling**: each will manage its own client well and never manage a rival's.
- Their existence validates the format and drives plugin supply. Good for us.
- The risk isn't that they compete on cross-client. It's that one of them **acquires** the cross-client layer. Which is the plan.

### 2.4 Adjacent, will arrive eventually

- **Snyk / Socket / Chainguard-style supply-chain vendors** — will extend to agent artifacts. Fastest credible threat on the security wedge. Their weakness: no endpoint presence in agent clients, no skill semantics.
- **JFrog / Artifactory** — will add a plugin repo type. Slow, but owns the enterprise artifact relationship.
- **Datadog / observability** — will add agent tool-call telemetry. Complementary; integrate rather than fight.
- **MDM vendors (Jamf, Kandji, Intune)** — theoretically the right shape, practically will not understand skills for years.

## 3. Differentiation, in one table

| Capability | Client-native | MCP gateways | Public registries | **AgentBridge** |
|---|---|---|---|---|
| Install a plugin | ✅ own client | ❌ | ❌ | ✅ every client |
| Works with non-conformant clients (Claude Code, Zed, Gemini CLI…) | ❌ | partial | ❌ | ✅ adapters |
| Lockfile / reproducible install | ❌ | ❌ | ❌ | ✅ |
| Fleet inventory across clients | ❌ | ❌ | ❌ | ✅ |
| Skill (prompt) inspection | ❌ | ❌ | ❌ | ✅ |
| MCP tool-call audit | ❌ | ✅ | ❌ | ✅ |
| Secret brokerage | partial | ✅ | ❌ | ✅ |
| Signing / provenance | ❌ | ❌ | partial | ✅ |
| Compliance evidence export | ❌ | partial | ❌ | ✅ |

## 4. Strategic reading

1. **Do not enter as "another MCP gateway."** That fight is crowded, feature-parity-driven, and the AWS OSS option is free.
2. **Enter as endpoint + instruction governance**, then absorb gateway functionality as a feature. Being a gateway is a checkbox we can add; being on the endpoint is a position others cannot easily take.
3. **Integrate loudly with the incumbents.** Import from Smithery and the official registry; export audit to Datadog; sit in front of Obot/Arcade rather than replacing them at first. Partnerships buy time and make us an obvious tuck-in rather than a threat.
4. **Own the compatibility matrix as public good.** It is cheap to produce, impossible for any single client vendor to publish credibly, and it makes us the neutral referee of the ecosystem — which is exactly the reputation that makes an acquirer want us.

## Sources

- [Agent Plugins](https://agent-plugins.org/)
- [Introducing Agent Plugins — Vercel](https://vercel.com/blog/introducing-agent-plugins)
- [AWS Supports Agent Plugins — AWS Open Source Blog](https://aws.amazon.com/blogs/opensource/aws-supports-agent-plugins-an-open-standard-for-portable-agent-extensions/)
- [Techmeme roundup, 2026-08-06](https://www.techmeme.com/260806/p34)
- [Governing AI Assets at Scale with MCP Gateway and Registry — AWS](https://aws.amazon.com/blogs/opensource/governing-ai-assets-at-scale-with-mcp-gateway-and-registry/)
- [agentic-community/mcp-gateway-registry](https://github.com/agentic-community/mcp-gateway-registry)
- [The 13 Best MCP Gateways for Enterprise Teams — Obot](https://obot.ai/blog/the-13-best-mcp-gateways-for-enterprise-teams/)
- [Best MCP Gateways, Runtimes & Registries — Arcade](https://www.arcade.dev/blog/mcp-gateways-runtimes-registries-guide/)
- [Best MCP Registries in 2026 — TrueFoundry](https://www.truefoundry.com/blog/best-mcp-registries)
