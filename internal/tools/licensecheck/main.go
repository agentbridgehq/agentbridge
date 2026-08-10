// Command licensecheck fails the build when any module in the dependency graph
// ships a copyleft license that would contaminate commercial code.
//
// Rationale is in docs/06-business-model-and-acquisition.md section 4: AGPL or
// SSPL contamination is trivially avoidable now and expensive-to-impossible to
// unwind during acquisition diligence. This runs in CI on every change (M0-3).
//
// It is deliberately dependency-free and deliberately blunt: it reads the
// license text of every module in the build list and looks for the identifying
// title lines of denied licenses. False positives are resolved by adding an
// entry to allowedModules with a written justification, not by weakening the
// matcher.
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// deniedMarkers are matched case-insensitively against license file contents.
var deniedMarkers = []string{
	"GNU AFFERO GENERAL PUBLIC LICENSE",
	"GNU GENERAL PUBLIC LICENSE",
	"SERVER SIDE PUBLIC LICENSE",
	"BUSINESS SOURCE LICENSE",
}

// allowedModules are exceptions, each with a justification. Keep this list
// short and reviewed; every entry is a liability someone must re-verify at
// diligence time.
var allowedModules = map[string]string{}

var licenseFileNames = []string{
	"LICENSE", "LICENCE", "COPYING", "LICENSE.txt", "LICENCE.txt",
	"LICENSE.md", "LICENCE.md", "COPYING.txt", "LICENSE-APACHE", "LICENSE-MIT",
}

type module struct {
	Path    string
	Dir     string
	Main    bool
	Version string
}

func main() {
	mods, err := buildList()
	if err != nil {
		fatal("listing modules: %v", err)
	}

	var violations []string
	checked := 0

	for _, m := range mods {
		if m.Main || m.Dir == "" {
			continue
		}
		if reason, ok := allowedModules[m.Path]; ok {
			fmt.Printf("  allow  %s (%s)\n", m.Path, reason)
			continue
		}
		checked++
		hits, err := scanModule(m.Dir)
		if err != nil {
			fatal("scanning %s: %v", m.Path, err)
		}
		for _, h := range hits {
			violations = append(violations,
				fmt.Sprintf("%s@%s: %s", m.Path, m.Version, h))
		}
	}

	fmt.Printf("licensecheck: %d module(s) scanned\n", checked)

	if len(violations) > 0 {
		fmt.Fprintln(os.Stderr, "\nDENIED LICENSES FOUND:")
		for _, v := range violations {
			fmt.Fprintf(os.Stderr, "  - %s\n", v)
		}
		fmt.Fprintln(os.Stderr, "\nSee docs/08-tech-stack.md section 9. Remove the dependency,")
		fmt.Fprintln(os.Stderr, "or add a justified exception to allowedModules if the license")
		fmt.Fprintln(os.Stderr, "genuinely does not apply to how we consume it.")
		os.Exit(1)
	}

	fmt.Println("licensecheck: OK")
}

func buildList() ([]module, error) {
	out, err := exec.Command("go", "list", "-m", "-json", "all").Output()
	if err != nil {
		// A module with no dependencies makes `go list -m all` succeed with
		// only the main module, so a hard failure here is a real error.
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%w: %s", err, ee.Stderr)
		}
		return nil, err
	}
	var mods []module
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var m module
		if err := dec.Decode(&m); err != nil {
			return nil, err
		}
		mods = append(mods, m)
	}
	return mods, nil
}

// scanModule reads license files at the module root and one level down (many
// modules put licenses in subpackages) and returns any denied markers found.
func scanModule(dir string) ([]string, error) {
	var hits []string
	seen := map[string]bool{}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable paths in the module cache are not our problem
		}
		if d.IsDir() {
			// Only the module root and its immediate children.
			rel, rerr := filepath.Rel(dir, path)
			if rerr == nil && strings.Count(rel, string(filepath.Separator)) >= 1 && rel != "." {
				return fs.SkipDir
			}
			return nil
		}
		if !isLicenseFile(d.Name()) {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		upper := strings.ToUpper(string(data))
		for _, marker := range deniedMarkers {
			if strings.Contains(upper, marker) && !seen[marker] {
				seen[marker] = true
				rel, _ := filepath.Rel(dir, path)
				hits = append(hits, fmt.Sprintf("%s in %s", marker, rel))
			}
		}
		return nil
	})
	return hits, err
}

func isLicenseFile(name string) bool {
	for _, want := range licenseFileNames {
		if strings.EqualFold(name, want) {
			return true
		}
	}
	return false
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "licensecheck: "+format+"\n", args...)
	os.Exit(1)
}
