// Command specwatch detects changes in the sources this project is built on.
//
// Running our own test suite nightly would find nothing: CI already runs it on
// every commit, and nothing changes in between. What *does* change without
// anyone here noticing is the ground the implementation stands on — the
// specification, its schemas, and the vendor documentation each adapter's paths
// were taken from. Those move on other people's schedules, and the first signal
// today would be a user reporting that installs stopped working.
//
// Three checks, each chosen because it is a reliable signal rather than a noisy
// one:
//
//   - **Embedded schemas versus canonical.** A byte difference means the
//     specification republished. §10.1 guarantees both schemas ship with every
//     release even when unchanged, so this is an exact, low-noise trigger.
//   - **New specification versions.** A file in spec/ other than the one we
//     implement means a release happened.
//   - **Source documentation still resolves.** Every adapter cites the vendor
//     page its paths came from. A 404 means that page moved or was withdrawn,
//     which is the leading indicator of an adapter quietly becoming wrong.
//
// Deliberately not checked: the *content* of vendor documentation pages. Those
// are marketing sites that change daily for unrelated reasons, and a check that
// fires constantly is one people learn to ignore.
//
// This is a development tool. It is not compiled into the agentbridge binary,
// which makes no network calls at all — see internal/privacy.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agentbridge/agentbridge/internal/adapter"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	"github.com/agentbridge/agentbridge/internal/schema"
)

const (
	specRepo    = "agentplugins/agent-plugins-spec"
	treeAPI     = "https://api.github.com/repos/" + specRepo + "/git/trees/main?recursive=1"
	rawBase     = "https://raw.githubusercontent.com/" + specRepo + "/main/"
	httpTimeout = 30 * time.Second
)

// Status grades a check.
type Status string

const (
	OK    Status = "ok"
	Drift Status = "drift"
	Error Status = "error"
)

// Check is one observation about an upstream source.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Action is what to do about it. A drift report without one is just an
	// interruption.
	Action string `json:"action,omitempty"`
}

// Report is a full run.
type Report struct {
	CheckedAt string  `json:"checkedAt"`
	Checks    []Check `json:"checks"`
}

func (r *Report) add(status Status, name, detail, action string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail, Action: action})
}

func (r *Report) count(s Status) int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == s {
			n++
		}
	}
	return n
}

func main() {
	asJSON := flag.Bool("json", false, "machine-readable output")
	flag.Parse()

	report := run()

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	} else {
		print(report)
	}

	// Drift is not a failure of this repository, but it does need a human, so
	// it exits non-zero to make the nightly visible.
	if report.count(Drift) > 0 || report.count(Error) > 0 {
		os.Exit(1)
	}
}

func run() *Report {
	report := &Report{CheckedAt: time.Now().UTC().Format(time.RFC3339)}

	checkSchemas(report)
	checkSpecVersions(report)
	checkSourceDocs(report)

	return report
}

// checkSchemas compares our embedded copies against the canonical ones.
func checkSchemas(r *Report) {
	for _, s := range []struct {
		path     string
		embedded []byte
	}{
		{"schemas/1.0.0/plugin.schema.json", schema.PluginManifestJSON()},
		{"schemas/1.0.0/mcp.schema.json", schema.MCPConfigJSON()},
	} {
		upstream, err := fetch(rawBase + s.path)
		if err != nil {
			r.add(Error, s.path, err.Error(), "check network access, or whether the file moved")
			continue
		}
		if !bytes.Equal(bytes.TrimSpace(upstream), bytes.TrimSpace(s.embedded)) {
			r.add(Drift, s.path,
				"the canonical schema differs from the copy embedded in this build",
				"re-download it into internal/schema, re-run the conformance corpus, and update docs/10-spec-compliance.md")
			continue
		}
		r.add(OK, s.path, "identical to the embedded copy", "")
	}
}

// checkSpecVersions looks for a specification release we do not implement.
func checkSpecVersions(r *Report) {
	raw, err := fetch(treeAPI)
	if err != nil {
		r.add(Error, "spec versions", err.Error(), "check network access or the API rate limit")
		return
	}

	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(raw, &tree); err != nil {
		r.add(Error, "spec versions", "could not parse the repository tree: "+err.Error(), "")
		return
	}

	var found []string
	for _, e := range tree.Tree {
		if e.Type != "blob" || !strings.HasPrefix(e.Path, "spec/") || !strings.HasSuffix(e.Path, ".md") {
			continue
		}
		version := strings.TrimSuffix(strings.TrimPrefix(e.Path, "spec/"), ".md")
		found = append(found, version)
	}

	if len(found) == 0 {
		r.add(Error, "spec versions", "no specification documents found in the repository", "")
		return
	}

	for _, version := range found {
		if version == schema.SpecVersion {
			continue
		}
		r.add(Drift, "spec versions",
			fmt.Sprintf("a specification version this build does not implement exists upstream: %s (we implement %s)",
				version, schema.SpecVersion),
			"read the new specification, extend internal/schema, and add conformance cases for whatever changed")
	}

	if r.count(Drift) == 0 {
		r.add(OK, "spec versions", "only "+schema.SpecVersion+" is published", "")
	}
}

// checkSourceDocs verifies the vendor pages each adapter's paths came from.
//
// Only reachability is checked. Hashing the content of a vendor documentation
// site would fire on every unrelated edit, and a check that fires constantly is
// one people learn to ignore.
func checkSourceDocs(r *Report) {
	seen := map[string]bool{}

	for _, a := range adapterreg.Adapters(adapter.Env{}) {
		c := a.Client()
		if c.ConfigDoc == "" || seen[c.ConfigDoc] {
			continue
		}
		seen[c.ConfigDoc] = true

		status, err := head(c.ConfigDoc)
		switch {
		case err != nil:
			r.add(Error, c.ID, "could not reach "+c.ConfigDoc+": "+err.Error(), "")
		case status >= 400:
			r.add(Drift, c.ID,
				fmt.Sprintf("%s returns %d — the documentation this adapter's paths came from has moved or been withdrawn", c.ConfigDoc, status),
				"find the new page, re-verify the configuration paths against it, and update the adapter's ConfigDoc")
		default:
			r.add(OK, c.ID, c.ConfigDoc+" still resolves", "")
		}
	}
}

func print(r *Report) {
	marker := map[Status]string{OK: "ok", Drift: "!!", Error: "xx"}

	fmt.Printf("Upstream sources, checked %s\n\n", r.CheckedAt)
	for _, c := range r.Checks {
		fmt.Printf("  %s %-34s %s\n", marker[c.Status], c.Name, c.Detail)
		if c.Action != "" {
			fmt.Printf("       → %s\n", c.Action)
		}
	}

	fmt.Printf("\n  %d ok, %d drifted, %d could not be checked\n",
		r.count(OK), r.count(Drift), r.count(Error))
	if r.count(Drift) == 0 && r.count(Error) == 0 {
		fmt.Println("  Nothing upstream has moved.")
	}
}

func client() *http.Client { return &http.Client{Timeout: httpTimeout} }

func fetch(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// An unauthenticated API call is rate-limited to a level a nightly run
	// exceeds only if something is wrong; CI supplies a token anyway.
	if token := os.Getenv("GITHUB_TOKEN"); token != "" && strings.Contains(url, "api.github.com") {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func head(url string) (int, error) {
	// Some documentation sites reject HEAD, so a GET is used and the body
	// discarded: a false "moved" report is worse than a wasted request.
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "agentbridge-specwatch")

	resp, err := client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	return resp.StatusCode, nil
}
