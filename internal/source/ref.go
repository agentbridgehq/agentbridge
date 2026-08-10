// Package source resolves a plugin reference into a directory on disk.
//
// The specification defines a plugin as "a directory rooted at a single
// filesystem location" and says nothing whatever about how that directory
// arrives — no registry, no fetch protocol, no integrity mechanism. That
// absence is the supply chain this project exists to supply, so everything here
// is ours to define, and the properties worth insisting on are:
//
//   - **Pinned.** A branch or tag is resolved to an immutable commit before
//     anything is fetched, and the commit is what gets recorded. "Install what
//     main pointed at last Tuesday" is not reproducible and is not a supply
//     chain.
//   - **Content-addressed.** Every materialized package carries a tree digest
//     over its bytes, so the same reference either produces the same bytes or
//     produces a loud error.
//   - **Cacheable offline.** A pinned reference already in the cache never
//     touches the network. That is what makes the CLI usable in the air-gapped
//     environments the enterprise story depends on.
package source

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
)

// Kind identifies how a reference is fetched.
type Kind string

const (
	// KindLocal is a directory already on this machine.
	KindLocal Kind = "local"
	// KindGit is a git repository.
	KindGit Kind = "git"
)

// Ref is a parsed plugin reference.
type Ref struct {
	// Raw is the reference exactly as the user wrote it.
	Raw  string `json:"raw"`
	Kind Kind   `json:"kind"`

	// Path is the directory, for a local reference.
	Path string `json:"path,omitempty"`

	// URL is the normalized remote, for a git reference.
	URL string `json:"url,omitempty"`
	// Rev is the requested branch, tag or commit. Empty means the remote's
	// default branch.
	Rev string `json:"rev,omitempty"`
	// Subdir is the plugin's location within the repository, for the
	// monorepo-of-plugins layout that the fixed component locations make
	// otherwise impossible to express.
	Subdir string `json:"subdir,omitempty"`
}

func (r Ref) String() string { return r.Raw }

// hostPrefixed matches a shorthand like github.com/org/repo — a first segment
// that looks like a hostname, followed by a path.
var hostPrefixed = regexp.MustCompile(`^[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)+/.+`)

// ParseRef parses a plugin reference.
//
// Accepted forms:
//
//	./plugins/db                          local directory
//	/abs/path/to/plugin                   local directory
//	file:///srv/git/repo@v1.0.0           git over a local or mounted path
//	https://github.com/org/repo           git, default branch
//	https://github.com/org/repo@v1.2.0    git, pinned to a tag
//	github.com/org/repo@main#plugins/db   git, branch and subdirectory
//	git@github.com:org/repo.git@v1.2.0    git over SSH
//
// A bare `org/repo` is deliberately *not* treated as a GitHub shorthand. It is
// a perfectly ordinary relative path, and silently turning a user's local
// directory into a network fetch is the kind of surprise that costs trust once.
func ParseRef(raw string) (Ref, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Ref{}, fmt.Errorf("empty plugin reference")
	}
	// A leading dash would be read as a flag by anything we hand this to.
	if strings.HasPrefix(trimmed, "-") {
		return Ref{}, fmt.Errorf("invalid plugin reference %q: must not begin with %q", raw, "-")
	}

	if isLocalPath(trimmed) {
		return Ref{Raw: raw, Kind: KindLocal, Path: trimmed}, nil
	}

	base, subdir := splitSubdir(trimmed)
	base, rev := splitRev(base)

	switch {
	case strings.HasPrefix(base, "git@"), strings.HasPrefix(base, "ssh://"):
		// SCP-style and ssh:// remotes are passed through unchanged; git
		// understands them and we should not try to normalize them.
	case strings.HasPrefix(base, "http://"), strings.HasPrefix(base, "https://"),
		strings.HasPrefix(base, "file://"):
		// file:// is a real git transport, not a test affordance: bare
		// repositories on network mounts are how a number of organizations
		// distribute internal code.
		if _, err := url.Parse(base); err != nil {
			return Ref{}, fmt.Errorf("invalid plugin reference %q: %w", raw, err)
		}
	case hostPrefixed.MatchString(base):
		base = "https://" + base
	default:
		return Ref{}, fmt.Errorf("unrecognized plugin reference %q: "+
			"use a local path (./plugin), an https URL, or a host-qualified repository (github.com/org/repo)", raw)
	}

	if subdir != "" {
		clean := path.Clean(subdir)
		if strings.HasPrefix(clean, "..") || path.IsAbs(clean) {
			return Ref{}, fmt.Errorf("invalid subdirectory %q in %q: must stay within the repository", subdir, raw)
		}
		subdir = clean
	}
	if strings.HasPrefix(rev, "-") {
		return Ref{}, fmt.Errorf("invalid revision %q in %q", rev, raw)
	}

	return Ref{Raw: raw, Kind: KindGit, URL: base, Rev: rev, Subdir: subdir}, nil
}

// isLocalPath reports whether a reference names a directory on this machine.
//
// Explicit path syntax wins outright. Beyond that, an existing directory is
// treated as local: if a user has a directory of that name, they meant it.
func isLocalPath(s string) bool {
	switch {
	case strings.HasPrefix(s, "./"), strings.HasPrefix(s, "../"),
		strings.HasPrefix(s, ".\\"), strings.HasPrefix(s, "..\\"),
		strings.HasPrefix(s, "/"), strings.HasPrefix(s, "~"),
		s == ".", s == "..":
		return true
	}
	// A Windows drive letter, which must not be mistaken for a URL scheme.
	if len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		return true
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git@") {
		return false
	}
	info, err := os.Stat(s)
	return err == nil && info.IsDir()
}

// splitSubdir separates a trailing #subdir fragment.
func splitSubdir(s string) (base, subdir string) {
	if i := strings.LastIndex(s, "#"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// splitRev separates a trailing @rev.
//
// The separator is only recognized after the last path separator, so the "@"
// in an SCP-style remote such as git@github.com:org/repo is not mistaken for a
// revision.
func splitRev(s string) (base, rev string) {
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return s, ""
	}
	lastSep := strings.LastIndexAny(s, "/:")
	if at < lastSep {
		return s, ""
	}
	return s[:at], s[at+1:]
}
