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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	report()

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

// report writes what this process was given, before any protocol traffic, so
// the file exists even if the client hangs up without completing a handshake.
//
// It writes twice on purpose. The requested path is the useful one, but it can
// fail for reasons that are themselves the answer: a client that does not
// expand ${PLUGIN_DATA} leaves a literal placeholder, and creating a file
// inside a directory named "${PLUGIN_DATA}" fails. An earlier version skipped
// silently in that case, which made "the variable was never set" and "the
// variable was set to something unusable" look identical — and both look like
// "the client never launched me". The fallback in the temporary directory
// always succeeds, so there is always something to read.
func report() {
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

	requested := os.Getenv("AGENTBRIDGE_PROBE_OUT")
	doc := map[string]any{
		"cwd":       mustCwd(),
		"pid":       os.Getpid(),
		"requested": requested,
		"env":       env,
		"keys":      keys,
	}

	write := func(path string) bool {
		f, err := os.Create(path)
		if err != nil {
			return false
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		err = enc.Encode(doc)
		_ = f.Close()
		return err == nil
	}

	wroteRequested := requested != "" && write(requested)
	doc["wroteRequested"] = wroteRequested

	dir := filepath.Join(os.TempDir(), "agentbridge-probe")
	if os.MkdirAll(dir, 0o755) == nil {
		write(filepath.Join(dir, fmt.Sprintf("probe-%d.json", os.Getpid())))
	}
}

func mustCwd() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}
