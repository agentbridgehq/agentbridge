// Command probe is an MCP server that reports the environment it was given.
//
// Section 9.1 requires a client to provide PLUGIN_ROOT and PLUGIN_DATA to every
// plugin subprocess. Until now that could only be checked by reading
// configuration, which answers a different question — whether the variables were
// *written*, not whether the process *received* them. A client that expands
// placeholders lazily, or drops an env map it does not recognise, looks correct
// in the file and wrong at run time.
//
// So the probe writes its own environment to $AGENTBRIDGE_PROBE_OUT and then
// speaks just enough of the protocol to stay connected. It has to stay
// connected: a server that exits immediately is reported as failed, and a client
// that never launched it would be indistinguishable from one that launched it
// and got the environment wrong.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

func main() {
	if out := os.Getenv("AGENTBRIDGE_PROBE_OUT"); out != "" {
		env := map[string]string{}
		for _, kv := range os.Environ() {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[k] = v
			}
		}
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// Written before the first read, so the file exists even if the client
		// hangs up without completing a handshake.
		if f, err := os.Create(out); err == nil {
			enc := json.NewEncoder(f)
			enc.SetIndent("", "  ")
			_ = enc.Encode(map[string]any{"cwd": mustCwd(), "env": env, "keys": keys})
			_ = f.Close()
		}
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(os.Stdout)

	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal([]byte(line), &req) != nil || req.ID == nil {
			continue // a notification, or something we do not model
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "agentbridge-probe", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{}}
		default:
			result = map[string]any{}
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}

func mustCwd() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}
