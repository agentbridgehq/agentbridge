package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Comparing a scan against what was reviewed before.
//
// A scan on its own answers "what is in this plugin?". At update time the
// useful question is different: **what is in it that was not in the version I
// approved?** Threat T5 — a plugin that was clean when it was reviewed and
// gains an injected instruction three commits later — is only visible in the
// second question, and it is invisible to a lockfile alone: the digest changes
// honestly, because the author really did change the file.
//
// So a locked plugin carries the findings that were accepted when it was
// locked. On the next sync the scan is compared against that record and split
// into new, unchanged, and resolved. Only what is new can block, which is what
// makes a blocking gate survivable: a plugin with one permanently awkward
// sentence does not demand an override flag every week, and the override
// therefore keeps meaning something when a finding really is new.
//
// The acceptance lives in the lock rather than in a local state file on
// purpose. The lock is committed and reviewed, so "we decided this finding was
// fine" is a reviewable line in a pull request rather than a decision one
// person made once on their laptop.

// Fingerprint identifies a finding across versions of a plugin.
//
// It is deliberately built from the rule, the file and the text — **not** the
// line number, which moves whenever anything above it is edited, and not the
// message, which would invalidate every stored baseline the moment a rule's
// wording is improved in a release.
//
// The consequence is that editing a flagged line produces a resolved finding
// and a new one rather than an unchanged one. That is the safe direction: an
// approval should not carry silently across text that has changed.
//
// Findings identical in rule, file and text are one finding. Two servers
// reaching the same endpoint collapse to a single entry, which matches how a
// reviewer would treat them — it is one fact to decide about.
func (f Finding) Fingerprint() string {
	h := sha256.New()
	h.Write([]byte(f.RuleID))
	h.Write([]byte{0})
	h.Write([]byte(f.File))
	h.Write([]byte{0})
	h.Write([]byte(normalizeForFingerprint(f.Excerpt)))
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// normalizeForFingerprint makes the identity insensitive to reflowing and to
// case, so re-wrapping a paragraph does not present every finding in it as new.
func normalizeForFingerprint(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// Accepted is one finding recorded as reviewed.
//
// The rule, severity and file are stored alongside the fingerprint even though
// only the fingerprint is compared. A lock full of bare hashes would be
// unreadable, and the point of recording this in a reviewed file is that the
// diff says something: `+ rule: skill.instruction_override` in a pull request
// is the whole feature.
type Accepted struct {
	ID       string   `yaml:"id" json:"id"`
	Rule     string   `yaml:"rule" json:"rule"`
	Severity Severity `yaml:"severity" json:"severity"`
	File     string   `yaml:"file,omitempty" json:"file,omitempty"`
}

// Baseline is the set of findings accepted when a plugin was locked.
type Baseline []Accepted

// NewBaseline records a scan's findings as reviewed.
func NewBaseline(findings []Finding) Baseline {
	seen := map[string]bool{}
	out := make(Baseline, 0, len(findings))
	for _, f := range findings {
		id := f.Fingerprint()
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Accepted{ID: id, Rule: f.RuleID, Severity: f.Severity, File: f.File})
	}
	// Sorted so that re-locking an unchanged plugin produces an unchanged file.
	// A lock that reorders itself between runs makes every diff unreadable and
	// trains reviewers to skip it.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rule != out[j].Rule {
			return out[i].Rule < out[j].Rule
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Has reports whether a finding was already accepted.
func (b Baseline) Has(f Finding) bool {
	id := f.Fingerprint()
	for _, a := range b {
		if a.ID == id {
			return true
		}
	}
	return false
}

// Delta is a scan compared against what was accepted before.
type Delta struct {
	// New appeared since the baseline. This is the set that matters, and the
	// only one that blocks.
	New []Finding
	// Unchanged was present and accepted before.
	Unchanged []Finding
	// Resolved was accepted before and is no longer reported. Worth showing:
	// it is the evidence that a maintainer fixed something.
	Resolved []Accepted
}

// Empty reports whether nothing moved in either direction.
func (d Delta) Empty() bool { return len(d.New) == 0 && len(d.Resolved) == 0 }

// NewAtLeast returns new findings at or above a severity.
func (d Delta) NewAtLeast(min Severity) []Finding {
	var out []Finding
	for _, f := range d.New {
		if f.Severity.AtLeast(min) {
			out = append(out, f)
		}
	}
	return out
}

// Against compares this report to a baseline.
//
// An empty baseline — a first install, or a lock written before findings were
// recorded — makes every finding new, which is the correct reading in both
// cases and preserves the behaviour of a gate with no history to consult.
func (r *Report) Against(b Baseline) Delta {
	var d Delta

	present := map[string]bool{}
	for _, f := range r.Findings {
		present[f.Fingerprint()] = true
		if b.Has(f) {
			d.Unchanged = append(d.Unchanged, f)
			continue
		}
		d.New = append(d.New, f)
	}

	for _, a := range b {
		if !present[a.ID] {
			d.Resolved = append(d.Resolved, a)
		}
	}
	return d
}
