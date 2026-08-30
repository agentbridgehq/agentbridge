// Package discover finds the plugins inside a directory tree.
//
// Every other command in this tool takes one plugin. That is the right shape
// for `install` — you install a thing, not a folder of things — but it is the
// wrong shape for the place a company actually meets this tool: a repository
// holding several internal plugins, scanned in CI on every pull request.
//
// Without discovery, that team writes a shell loop over directories they have
// to enumerate themselves, and a plugin added later is silently not scanned
// until somebody remembers to update the loop. A scanner that quietly stops
// covering new code is the failure mode this project cares most about, so
// finding the plugins is the tool's job rather than the user's.
package discover

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentbridgehq/agentbridge/internal/importer/registry"
	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// skipDirs are never descended into.
//
// Dependency and build directories routinely contain vendored packages that
// look exactly like plugins, and reporting findings from somebody else's
// vendored fixture is how a scan becomes noise.
var skipDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".venv":        true,
	"__pycache__":  true,
}

// maxDepth bounds how far below the root a plugin is looked for.
//
// Deep enough for the layouts people actually use — `plugins/<name>`,
// `tools/agent/<name>`, `packages/<team>/<name>` — and shallow enough that
// pointing this at a home directory by mistake finishes.
const maxDepth = 6

// Found is one plugin located in a tree.
type Found struct {
	// Dir is the absolute path to the plugin root.
	Dir string
	// Rel is the path relative to the search root, in slash form. This is what
	// a report shows and what a SARIF location is built from, so a finding
	// points at the file as the repository sees it rather than as the plugin
	// does.
	Rel string
}

// Plugins returns every plugin in the tree rooted at dir, ordered by path.
//
// If dir is itself a plugin, that is the only result — pointing at a plugin
// and pointing at a repository that contains one are the same operation, and
// the caller should not have to know which it has.
//
// Descent stops at each plugin found: a plugin's own subdirectories are its
// components, not further plugins, and recursing into them would report
// `references/` as a package of its own.
func Plugins(dir string) ([]Found, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	var found []Found
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is skipped rather than fatal: one
			// permission-denied corner of a repository should not stop the
			// rest of it being scanned.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && skipDirs[d.Name()] {
			return fs.SkipDir
		}

		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		if depth(rel) > maxDepth {
			return fs.SkipDir
		}

		if !isPlugin(p) {
			return nil
		}
		found = append(found, Found{Dir: p, Rel: filepath.ToSlash(rel)})
		return fs.SkipDir
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Rel < found[j].Rel })
	return found, nil
}

// isPlugin reports whether a directory is a plugin root in any dialect.
//
// It asks the importers rather than looking for filenames, so a dialect added
// later is discovered without touching this package.
func isPlugin(dir string) bool {
	root, err := safepath.NewRoot(dir)
	if err != nil {
		return false
	}
	_, ok := registry.Detect(root)
	return ok
}

func depth(rel string) int {
	if rel == "." {
		return 0
	}
	return strings.Count(filepath.ToSlash(rel), "/") + 1
}
