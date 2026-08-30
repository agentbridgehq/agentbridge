package adapter

import (
	"bytes"

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

	// Recorded before anything is written: once the first server is set, the
	// container exists and there is no longer any way to tell whether the
	// client shipped it or we made it.
	createdContainer := missingPrefix(doc, spec.ServersKey)

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

	// A plugin with no servers for this client has nothing to put in a config,
	// so bringing one into existence would leave the user a file they never
	// had, holding "{}", for a plugin that never touched it. Installing a
	// skills-only plugin did exactly that in every JSON-configured client.
	if len(writtenKeys) == 0 && !doc.Existed() {
		return plan, nil
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
	if len(createdContainer) > 0 && len(writtenKeys) > 0 {
		plan.CreatedContainers = [][]string{createdContainer}
	}
	return plan, nil
}

// missingPrefix returns the shortest prefix of keys that the document does not
// have, which is the outermost object a write would bring into existence.
// Removing that one key reclaims everything created beneath it.
func missingPrefix(doc *configedit.JSONDoc, keys []string) []string {
	for i := 1; i <= len(keys); i++ {
		prefix := keys[:i]
		if ok, err := doc.Has(prefix); err != nil {
			return nil
		} else if !ok {
			return append([]string(nil), prefix...)
		}
	}
	return nil
}

// reclaimable reports whether the container an install created still holds
// nothing of the user's. The servers object must be empty, and every level
// between the created key and it must hold only the link to the next — so a
// config that gained "mcp" solely to carry our entries loses it again, while
// one where the user has since put something of their own keeps everything.
//
// Emptiness is asked of the object itself rather than judged recursively: a
// server entry like {"command": "mine"} has no keys *below* its fields, and an
// earlier version of this that recursed treated that as empty and deleted the
// user's server. The narrow question is the safe one.
func reclaimable(doc *configedit.JSONDoc, created, serversKey []string) bool {
	present, err := doc.Has(serversKey)
	if err != nil {
		return false
	}
	if present {
		entries, err := doc.Keys(serversKey)
		if err != nil || len(entries) != 0 {
			return false
		}
	}
	for i := len(created); i < len(serversKey); i++ {
		children, err := doc.Keys(serversKey[:i])
		if err != nil || len(children) != 1 {
			return false
		}
	}
	return true
}

// PlanRemoveJSONMCP builds a removal plan from the exact keys recorded at
// install time.
func PlanRemoveJSONMCP(spec JSONMCPSpec, inst Installation, pluginName string, keys, created [][]string) (*Plan, error) {
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

	// Take back the containers this install created, so a config that had no
	// "mcp" key before does not keep an empty one forever. Only if they are
	// empty: a user who added a server of their own to the same object keeps
	// it, which is the whole reason the container is checked rather than
	// deleted outright.
	reclaimed := false
	for _, c := range created {
		if reclaimable(doc, c, spec.ServersKey) {
			if err := doc.Delete(c); err != nil {
				return nil, err
			}
			reclaimed = true
		}
	}

	// Deleting the last entry from an object leaves the whitespace it sat in
	// behind, so a config that read "mcpServers": {} before the install reads
	// "mcpServers": {\n  } after the removal. Nothing is broken by that, but
	// it is a diff the user did not make, in a file they may well have
	// committed. Rewriting the emptied object collapses it again.
	if !reclaimed {
		if present, err := doc.Has(spec.ServersKey); err == nil && present {
			if entries, err := doc.Keys(spec.ServersKey); err == nil && len(entries) == 0 {
				if err := doc.Set(spec.ServersKey, map[string]any{}); err != nil {
					return nil, err
				}
			}
		}
	}

	after, err := doc.Bytes()
	if err != nil {
		return nil, err
	}

	// Reclaiming the container can empty the document itself, and the same
	// leftover whitespace applies one level up: a config the install created
	// from nothing is left reading "{\n}" rather than "{}". Collapsing it
	// keeps a file we wrote from carrying a shape nobody chose.
	if root, err := doc.Keys(nil); err == nil && len(root) == 0 {
		after = []byte("{}")
		if bytes.HasSuffix(doc.Original(), []byte("\n")) || !doc.Existed() {
			after = append(after, '\n')
		}
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
	// Emptiness is checked first. Ordering it the other way announced
	// "create config with managed server entries" for a plugin that had none,
	// which is both untrue and the opposite of reassuring in a dry run.
	if n == 0 {
		return "no server entries to write"
	}
	if !existed {
		return "create config with managed server entries"
	}
	return "add or update managed server entries"
}
