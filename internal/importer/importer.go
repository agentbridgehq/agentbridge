// Package importer defines the contract every source dialect implements, plus
// the discovery helpers they share.
//
// An importer's job is to produce an ir.Plugin and an honest account of what
// happened on the way. It reports failure at two levels, and the distinction is
// load-bearing:
//
//   - A returned error means the plugin was rejected outright. Reserve this for
//     the cases the specification names: an unreadable or malformed manifest,
//     a missing required field, a type violation.
//   - An Error-severity diagnostic means one component was rejected while the
//     plugin loaded. The conformance rules require exactly this: "isolate
//     invalid entries at specified boundaries" and "continue loading when
//     individual MCP servers fail."
//
// Returning an error where a diagnostic belongs turns one typo in one server
// entry into a plugin that will not load anywhere — which is precisely the
// brittleness this project exists to remove.
package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentbridge/agentbridge/internal/diag"
	"github.com/agentbridge/agentbridge/internal/ir"
	"github.com/agentbridge/agentbridge/internal/safepath"
	"github.com/agentbridge/agentbridge/internal/skill"
)

// Result is a completed import.
type Result struct {
	Plugin *ir.Plugin
	// Diagnostics records everything lost, rewritten, or skipped. A successful
	// import routinely carries warnings; that is the normal case, not a
	// failure.
	Diagnostics diag.Diagnostics
	// SkillBodies holds skill Markdown bodies keyed by skill name, for
	// capability inference and, later, scanning. Deliberately not part of the
	// IR — see ir.Skill.
	SkillBodies map[string]string
}

// Importer reads one source dialect.
type Importer interface {
	// Dialect identifies the format this importer reads.
	Dialect() ir.Dialect
	// Detect reports whether the directory looks like this dialect. It must be
	// cheap and must not read component files.
	Detect(root *safepath.Root) bool
	// Import reads the plugin. A non-nil error means the plugin is rejected.
	Import(root *safepath.Root) (*Result, error)
}

// ErrNotRecognized is returned when no importer claims a directory.
var ErrNotRecognized = errors.New("not a recognized plugin directory")

// ReadJSON reads and decodes a plugin-relative JSON file.
//
// It returns the raw bytes alongside the decoded value: the decoded form is
// what schema validation needs, and the raw bytes are what preservation of
// opaque data needs.
func ReadJSON(root *safepath.Root, rel string) (raw []byte, decoded any, err error) {
	abs, err := root.Resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	raw, err = os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	// UseNumber keeps numeric literals in their original textual form, so a
	// value that is preserved and re-emitted is byte-identical rather than
	// widened through float64.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return raw, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return raw, decoded, nil
}

// Exists reports whether a plugin-relative path exists inside the root.
// A path that escapes the root is reported as non-existent: from the loader's
// point of view there is nothing there it is permitted to see.
func Exists(root *safepath.Root, rel string) bool {
	abs, err := root.Resolve(rel)
	if err != nil {
		return false
	}
	_, err = os.Stat(abs)
	return err == nil
}

// IsDir reports whether a plugin-relative path is an existing directory.
func IsDir(root *safepath.Root, rel string) bool {
	abs, err := root.Resolve(rel)
	if err != nil {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && info.IsDir()
}

// IsRegularFile reports whether a plugin-relative path resolves to a regular
// file.
//
// Spec 6.2 and 7.1 both turn on this distinction: a fixed component location
// that exists but is the wrong filesystem kind makes that component type
// invalid rather than merely absent, and a SKILL.md that is not a regular file
// is not a skill. Checking the kind explicitly gives a diagnostic that says so,
// instead of a read error two layers down.
func IsRegularFile(root *safepath.Root, rel string) bool {
	abs, err := root.Resolve(rel)
	if err != nil {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && info.Mode().IsRegular()
}

// DiscoverDirSkills reads skills laid out as immediate child directories of
// skillsDir, each containing SKILL.md.
//
// This is the portable layout: Agent Plugins fixes it at `skills/`, and Claude
// Code uses the same shape, which is the one place the two dialects agree
// exactly. Entries that are not directories are ignored — the specification
// defines discovery in terms of child directories, so a stray README at that
// level is not an error. A directory without SKILL.md produces a warning
// rather than an error, because the common cause is a work-in-progress skill
// and refusing to load the plugin over it would be disproportionate.
func DiscoverDirSkills(root *safepath.Root, skillsDir string) ([]ir.Skill, map[string]string, diag.Diagnostics) {
	var ds diag.Diagnostics
	bodies := map[string]string{}

	abs, err := root.Resolve(skillsDir)
	if err != nil {
		ds.Add(diag.Error, diag.CodePathEscape, skillsDir,
			"skills directory is not inside the plugin root: %v", err)
		return nil, bodies, ds
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			// Spec 6.2: a missing component location is not an error.
			return nil, bodies, ds
		}
		ds.Add(diag.Error, diag.CodePathUnreadable, skillsDir,
			"skills were not loaded: %v", err)
		return nil, bodies, ds
	}
	if !info.IsDir() {
		// Spec 6.2: present but the wrong filesystem kind makes the component
		// type invalid, while other component types continue loading.
		ds.Add(diag.Error, diag.CodeSkillsNotDirectory, skillsDir,
			"skills were not loaded: %s exists but is not a directory", skillsDir)
		return nil, bodies, ds
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		ds.Add(diag.Error, diag.CodePathUnreadable, skillsDir,
			"cannot read skills directory: %v", err)
		return nil, bodies, ds
	}

	// Sort so discovery order — and therefore the digest — does not depend on
	// filesystem iteration order.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var skills []ir.Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirRel := path.Join(skillsDir, e.Name())
		entryRel := path.Join(dirRel, "SKILL.md")

		if !Exists(root, entryRel) {
			ds.Add(diag.Warning, diag.CodeSkillMissingFile, dirRel,
				"directory has no SKILL.md and was not loaded as a skill")
			continue
		}
		// Spec 7.1: the skill is the directory containing a path named exactly
		// SKILL.md "that resolves to a regular file".
		if !IsRegularFile(root, entryRel) {
			ds.Add(diag.Error, diag.CodeSkillNotRegularFile, entryRel,
				"skill was skipped: SKILL.md is not a regular file")
			continue
		}
		s, body, sds := loadSkill(root, entryRel, ir.SkillDirectory, dirRel, e.Name())
		ds.Extend(sds)
		if s == nil {
			continue
		}
		skills = append(skills, *s)
		bodies[s.Name] = body
	}

	return skills, bodies, ds
}

// LoadFlatSkill reads a skill that is a single Markdown file rather than a
// directory. Agent Plugins has no such layout; Claude Code's commands/ does.
func LoadFlatSkill(root *safepath.Root, rel string) (*ir.Skill, string, diag.Diagnostics) {
	base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	return loadSkill(root, rel, ir.SkillFlatFile, "", base)
}

// LoadDirSkill reads one directory skill at an explicit location, used when a
// manifest names skill directories outside the default scan.
func LoadDirSkill(root *safepath.Root, dirRel string) (*ir.Skill, string, diag.Diagnostics) {
	entryRel := path.Join(dirRel, "SKILL.md")
	return loadSkill(root, entryRel, ir.SkillDirectory, dirRel, skill.DirName(dirRel))
}

func loadSkill(root *safepath.Root, entryRel string, kind ir.SkillKind, dirRel, fallbackName string) (*ir.Skill, string, diag.Diagnostics) {
	var ds diag.Diagnostics

	abs, err := root.Resolve(entryRel)
	if err != nil {
		ds.Add(diag.Error, diag.CodePathEscape, entryRel,
			"skill path is not inside the plugin root: %v", err)
		return nil, "", ds
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		ds.Add(diag.Error, diag.CodeSkillUnreadable, entryRel,
			"cannot read skill: %v", err)
		return nil, "", ds
	}

	parsed, pds := skill.Parse(raw)
	for i := range pds {
		pds[i].Path = entryRel
	}
	ds.Extend(pds)

	name := parsed.Name
	if name == "" {
		// Both dialects fall back to the directory or file basename. It is
		// worth a warning: for a marketplace install the directory name can be
		// a version string that changes on every update, so a skill without a
		// frontmatter name has an unstable identity.
		name = fallbackName
		ds.Add(diag.Warning, diag.CodeSkillNoName, entryRel,
			"skill has no frontmatter name; falling back to %q, which is tied to the directory layout", name)
	}

	return &ir.Skill{
		Name:        name,
		Description: parsed.Description,
		Kind:        kind,
		Dir:         dirRel,
		Entrypoint:  entryRel,
		Frontmatter: parsed.Frontmatter,
		ContentHash: parsed.ContentHash,
	}, parsed.Body, ds
}

// DedupeSkills removes skills whose names collide, keeping the first and
// reporting the rest. Name collisions are a real hazard rather than a
// formality: two skills with one name means the agent's behavior depends on
// load order, which no user can see.
func DedupeSkills(skills []ir.Skill, ds *diag.Diagnostics) []ir.Skill {
	seen := map[string]string{}
	out := make([]ir.Skill, 0, len(skills))
	for _, s := range skills {
		if first, dup := seen[s.Name]; dup {
			ds.AddComponent(diag.Warning, diag.CodeSkillDuplicate, s.Entrypoint, s.Name,
				"duplicate skill name; already defined by %s, this one was not loaded", first)
			continue
		}
		seen[s.Name] = s.Entrypoint
		out = append(out, s)
	}
	return out
}

// SortedKeys returns a map's keys in sorted order, so that iteration — and
// therefore the resulting digest — is deterministic.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
