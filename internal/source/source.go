package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolved is a plugin reference turned into a directory on disk, together with
// everything needed to reproduce it exactly.
type Resolved struct {
	Ref Ref `json:"ref"`
	// Dir is the package directory. For a git source this is inside the cache;
	// for a local source it is the user's own directory, used in place.
	Dir string `json:"dir"`
	// Commit is the immutable object id a git reference resolved to.
	Commit string `json:"commit,omitempty"`
	// TreeDigest is the content address of the package as installed.
	TreeDigest string `json:"treeDigest"`
	// FromCache records that no network access was needed.
	FromCache bool `json:"fromCache"`
}

// Pinned returns a reference string that resolves to exactly these bytes,
// suitable for recording in a lockfile. A branch or tag in the original
// reference is replaced by the commit it resolved to.
func (r Resolved) Pinned() string {
	if r.Ref.Kind != KindGit {
		return r.Ref.Raw
	}
	pinned := r.Ref.URL + "@" + r.Commit
	if r.Ref.Subdir != "" {
		pinned += "#" + r.Ref.Subdir
	}
	return pinned
}

// Options control resolution.
type Options struct {
	// Cache stores fetched packages. Required for remote references.
	Cache *Cache
	// ExpectedDigest, when set, must match the resolved package's tree digest.
	// This is what a lockfile supplies, and it is the point at which a
	// substituted artifact stops being installable.
	ExpectedDigest string
	// Offline forbids network access. A pinned reference already in the cache
	// still resolves; anything else fails rather than silently reaching out.
	Offline bool
	// Refresh ignores any cached copy and fetches again.
	Refresh bool
}

// Resolve turns a reference into a package directory.
func Resolve(ctx context.Context, ref Ref, opts Options) (*Resolved, error) {
	switch ref.Kind {
	case KindLocal:
		return resolveLocal(ref, opts)
	case KindGit:
		return resolveGit(ctx, ref, opts)
	default:
		return nil, fmt.Errorf("unsupported source kind %q", ref.Kind)
	}
}

// ResolveString parses and resolves in one step.
func ResolveString(ctx context.Context, raw string, opts Options) (*Resolved, error) {
	ref, err := ParseRef(raw)
	if err != nil {
		return nil, err
	}
	return Resolve(ctx, ref, opts)
}

// resolveLocal uses a directory in place.
//
// It is deliberately not copied into the cache. A local reference is almost
// always a plugin being developed, and the expected workflow is edit,
// reinstall, repeat — which a cached copy would silently defeat by installing
// yesterday's bytes.
func resolveLocal(ref Ref, opts Options) (*Resolved, error) {
	dir, err := filepath.Abs(expandHome(ref.Path))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref.Raw, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", ref.Raw)
	}

	digest, err := TreeDigest(dir)
	if err != nil {
		return nil, err
	}
	if err := verifyExpected(dir, digest, opts.ExpectedDigest); err != nil {
		return nil, err
	}

	return &Resolved{Ref: ref, Dir: dir, TreeDigest: digest, FromCache: true}, nil
}

// resolveGit pins, fetches and caches a repository reference.
func resolveGit(ctx context.Context, ref Ref, opts Options) (*Resolved, error) {
	if opts.Cache == nil {
		return nil, fmt.Errorf("a cache is required to resolve %s", ref.Raw)
	}

	commit := ref.Rev
	if !fullSHA.MatchString(commit) {
		if opts.Offline {
			// Resolving a branch or tag needs the remote. Offline mode falls
			// back to what this reference resolved to last time, which is what
			// makes re-installing a previously seen tag work without network
			// access. If the tag has since moved, this deliberately returns
			// what it meant when it was last seen.
			cached := opts.Cache.LookupRevision(ref.URL, ref.Rev)
			if cached == "" {
				return nil, fmt.Errorf("offline: %q has never been resolved on this machine; "+
					"pin it to a full commit id, or run once with network access", ref.Raw)
			}
			commit = cached
		} else {
			resolved, err := ResolveRevision(ctx, ref.URL, ref.Rev)
			if err != nil {
				return nil, err
			}
			commit = resolved
			if err := opts.Cache.PutRevision(ref.URL, ref.Rev, commit); err != nil {
				return nil, err
			}
		}
	}

	stamp := Stamp{Kind: KindGit, URL: ref.URL, Commit: commit, Subdir: ref.Subdir}

	if !opts.Refresh {
		dir, found, err := opts.Cache.Lookup(stamp)
		if err != nil {
			return nil, err
		}
		if found != nil {
			if err := verifyExpected(dir, found.TreeDigest, opts.ExpectedDigest); err != nil {
				return nil, err
			}
			return &Resolved{
				Ref: ref, Dir: dir, Commit: commit,
				TreeDigest: found.TreeDigest, FromCache: true,
			}, nil
		}
	}

	if opts.Offline {
		return nil, fmt.Errorf("offline: %s@%s is not in the cache at %s", ref.URL, commit[:12], opts.Cache.Root())
	}

	checkout, err := os.MkdirTemp("", "agentbridge-fetch-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(checkout)

	if err := fetchCommit(ctx, ref.URL, commit, checkout); err != nil {
		return nil, err
	}
	pkg, err := gitSubdir(checkout, ref.Subdir)
	if err != nil {
		return nil, err
	}

	dir, stored, err := opts.Cache.Put(pkg, stamp)
	if err != nil {
		return nil, err
	}
	if err := verifyExpected(dir, stored.TreeDigest, opts.ExpectedDigest); err != nil {
		return nil, err
	}

	return &Resolved{
		Ref: ref, Dir: dir, Commit: commit,
		TreeDigest: stored.TreeDigest, FromCache: false,
	}, nil
}

func verifyExpected(dir, actual, expected string) error {
	if expected == "" || actual == expected {
		return nil
	}
	return fmt.Errorf("integrity check failed for %s:\n  expected %s\n  actual   %s\n"+
		"the package does not match the digest that was recorded for this reference", dir, expected, actual)
}

func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
