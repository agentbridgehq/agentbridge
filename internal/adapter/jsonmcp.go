package adapter

import (
	"github.com/agentbridgehq/agentbridge/internal/configedit"
	"github.com/agentbridgehq/agentbridge/internal/ir"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// JSONMCPSpec describes a client whose MCP configuration is a JSON object of
// named servers.
//
// Cursor, VS Code and Gemini CLI all have this shape and differ only in the
// container key and the per-server field names. Sharing the mechanism keeps the
// interesting per-client detail — which is the encoding, and what each client
// cannot represent — in one small function per adapter rather than buried in
// three copies of the same file-editing logic.
type JSONMCPSpec struct {
	Client Client
	// ServersKey is the path to the servers object, e.g. ["mcpServers"].
	ServersKey []string
	// Encode converts a materialized server into the client's own shape.
	// Returning ok=false means this client cannot represent the server; the
	// reason is recorded as a loss by the caller.
	Encode func(s ir.MCPServer) (value map[string]any, reason string, ok bool)
	// PluginDataDir returns the persistent data directory for a plugin, used
	// to resolve ${PLUGIN_DATA}.
	PluginDataDir func(pluginName string) string
}

// PlanJSONMCP builds an install plan for a JSON-configured client.
func PlanJSONMCP(spec JSONMCPSpec, inst Installation, p *ir.Plugin, src *safepath.Root, opts PlanOptions) (*Plan, error) {
	plan := &Plan{Installation: inst, PluginName: p.Name}
	plan.Fidelity.Skills = Coverage{Carried: 0, Total: len(p.Skills)}
	NoteSkillsUnsupported(&plan.Fidelity, spec.Client, p.Skills)

	doc, err := configedit.LoadJSON(inst.ConfigPath)
	if err != nil {
		return nil, err
	}

	pluginRoot := ""
	if src != nil {
		pluginRoot = src.Path()
	}
	pluginData := spec.PluginDataDir(p.Name)

	servers := SortServers(p.MCPServers)
	plan.Fidelity.MCPServers.Total = len(servers)

	var writtenKeys [][]string
	for _, s := range servers {
		materialized := Materialize(s, pluginRoot, pluginData)

		prepared, notes, allowed := PrepareSecrets(materialized, opts, &plan.Fidelity, inst.ConfigPath)
		if !allowed {
			continue
		}
		plan.SecretNotes = append(plan.SecretNotes, notes...)
		materialized = prepared

		value, reason, ok := spec.Encode(materialized)
		if !ok {
			plan.Fidelity.AddLoss(LossTransportUnsupported, s.Name,
				"%s: %s", spec.Client.Name, reason)
			continue
		}

		keyPath := append(append([]string(nil), spec.ServersKey...), ManagedKey(p.Name, s.Name))
		if err := doc.Set(keyPath, value); err != nil {
			return nil, err
		}
		writtenKeys = append(writtenKeys, keyPath)
		plan.Fidelity.MCPServers.Carried++
	}

	if len(p.Extensions) > 0 {
		for ns := range p.Extensions {
			plan.Fidelity.AddLoss(LossExtensionsDropped, "",
				"extension namespace %q is not carried into %s, which has no place to put it", ns, spec.Client.Name)
		}
	}

	after, err := doc.Bytes()
	if err != nil {
		return nil, err
	}
	plan.Ops = []Op{{
		Kind:   OpWriteFile,
		Path:   inst.ConfigPath,
		Before: doc.Original(),
		After:  after,
		Note:   describeWrite(doc.Existed(), len(writtenKeys)),
	}}
	plan.ConfigKeys = writtenKeys
	return plan, nil
}

// PlanRemoveJSONMCP builds a removal plan from the exact keys recorded at
// install time.
func PlanRemoveJSONMCP(spec JSONMCPSpec, inst Installation, pluginName string, keys [][]string) (*Plan, error) {
	plan := &Plan{Installation: inst, PluginName: pluginName}

	doc, err := configedit.LoadJSON(inst.ConfigPath)
	if err != nil {
		return nil, err
	}
	if !doc.Existed() {
		return plan, nil
	}

	for _, k := range keys {
		if err := doc.Delete(k); err != nil {
			return nil, err
		}
	}

	after, err := doc.Bytes()
	if err != nil {
		return nil, err
	}
	plan.Ops = []Op{{
		Kind:   OpWriteFile,
		Path:   inst.ConfigPath,
		Before: doc.Original(),
		After:  after,
		Note:   "remove managed server entries",
	}}
	return plan, nil
}

func describeWrite(existed bool, n int) string {
	if !existed {
		return "create config with managed server entries"
	}
	if n == 0 {
		return "no server entries to write"
	}
	return "add or update managed server entries"
}
