package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// resolveOCI pins, fetches and caches an artifact reference.
//
// It follows exactly the shape of resolveGit, because the discipline is the
// same one: turn whatever the user named into an immutable identifier *before*
// fetching, record that identifier, and never let a mutable name reach the
// cache key. A tag is to a registry what a branch is to a repository — a label
// somebody can move tomorrow.
func resolveOCI(ctx context.Context, ref Ref, opts Options) (*Resolved, error) {
	if opts.Cache == nil {
		return nil, fmt.Errorf("a cache is required to resolve %s", ref.Raw)
	}

	digest := ref.Digest
	if digest == "" {
		resolved, err := resolveOCITag(ctx, ref, opts)
		if err != nil {
			return nil, err
		}
		digest = resolved
	}

	stamp := Stamp{Kind: KindOCI, URL: ref.URL, Commit: digest, Subdir: ref.Subdir}

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
				Ref: ref, Dir: dir, Commit: digest,
				TreeDigest: found.TreeDigest, FromCache: true,
			}, nil
		}
	}

	if opts.Offline {
		return nil, fmt.Errorf("offline: %s@%s is not in the cache at %s", ref.URL, shortDigest(digest), opts.Cache.Root())
	}

	unpacked, err := os.MkdirTemp("", "agentbridge-oci-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(unpacked)

	if err := pullArtifact(ctx, ref, digest, unpacked); err != nil {
		return nil, err
	}
	pkg, err := packageSubdir(unpacked, ref.Subdir)
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
		Ref: ref, Dir: dir, Commit: digest,
		TreeDigest: stored.TreeDigest, FromCache: false,
	}, nil
}

// resolveOCITag turns a tag into a manifest digest.
//
// Offline falls back to what this tag resolved to last time, exactly as the git
// path does, and for the same reason: a user reinstalling the tag they
// installed yesterday should not be told to go and find its digest by hand. A
// tag that has since moved therefore resolves to what it meant when it was last
// seen, which is the honest meaning of offline.
func resolveOCITag(ctx context.Context, ref Ref, opts Options) (string, error) {
	if opts.Offline {
		cached := opts.Cache.LookupRevision(ref.URL, ref.Rev)
		if cached == "" {
			return "", fmt.Errorf("offline: %q has never been resolved on this machine; "+
				"pin it to a digest, or run once with network access", ref.Raw)
		}
		return cached, nil
	}

	client := newRegistryClient()
	raw, digest, err := client.fetchManifest(ctx, ref, ref.Rev)
	if err != nil {
		return "", err
	}

	// An index names other manifests rather than layers, so it has to be
	// followed before there is anything to unpack.
	if resolvedDigest, ok, err := followIndex(ctx, client, ref, raw); err != nil {
		return "", err
	} else if ok {
		digest = resolvedDigest
	}

	if err := opts.Cache.PutRevision(ref.URL, ref.Rev, digest); err != nil {
		return "", err
	}
	return digest, nil
}

// followIndex resolves a multi-manifest index down to a single manifest.
//
// Platform selection is deliberately not performed. A plugin is text and
// configuration, not a compiled binary, so an artifact that offers several
// platform variants is either not a plugin or is making a distinction we would
// have to guess at — and guessing which variant of an agent's instructions to
// install is not a decision to take silently. One entry is followed; more than
// one is an error that says what to do.
func followIndex(ctx context.Context, client *registryClient, ref Ref, raw []byte) (string, bool, error) {
	var index manifest
	if err := json.Unmarshal(raw, &index); err != nil {
		return "", false, fmt.Errorf("registry returned an unreadable manifest: %w", err)
	}
	if len(index.Manifests) == 0 {
		return "", false, nil
	}

	if len(index.Manifests) > 1 {
		return "", false, fmt.Errorf("%s is a multi-platform index with %d manifests; "+
			"a plugin should be a single artifact. Pin the one you want with oci://…@sha256:…",
			ref.URL, len(index.Manifests))
	}
	return index.Manifests[0].Digest, true, nil
}

// pullArtifact downloads and unpacks the layers of one manifest.
func pullArtifact(ctx context.Context, ref Ref, digest, dst string) error {
	client := newRegistryClient()

	raw, actual, err := client.fetchManifest(ctx, ref, digest)
	if err != nil {
		return err
	}
	// The manifest was requested *by* digest, so a mismatch means the registry
	// served something other than what was asked for. This is the check that
	// makes the recorded pin mean anything at all.
	if actual != digest {
		return fmt.Errorf("registry served the wrong manifest:\n  requested %s\n  received  %s", digest, actual)
	}

	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("unreadable manifest %s: %w", digest, err)
	}
	if len(m.Manifests) > 0 {
		return fmt.Errorf("%s resolved to an index rather than a manifest", digest)
	}
	if len(m.Layers) == 0 {
		return fmt.Errorf("%s has no layers; there is nothing to install", digest)
	}
	if len(m.Layers) > maxLayers {
		return fmt.Errorf("%s has %d layers, over the limit of %d; this does not look like a plugin",
			digest, len(m.Layers), maxLayers)
	}

	limits := newLayerLimits()
	for _, layer := range m.Layers {
		if !isUnpackableLayer(layer.MediaType) {
			return fmt.Errorf("layer %s has media type %q, which agentbridge does not unpack. "+
				"A plugin artifact's layers must be tar or tar+gzip", shortDigest(layer.Digest), layer.MediaType)
		}
		if err := pullLayer(ctx, client, ref, layer, dst, limits); err != nil {
			return err
		}
	}
	return nil
}

func pullLayer(ctx context.Context, client *registryClient, ref Ref, layer descriptor, dst string, limits *layerLimits) error {
	body, err := client.fetchBlob(ctx, ref, layer)
	if err != nil {
		return err
	}
	defer body.Close()

	if err := unpackLayer(body, layer.MediaType, dst, limits); err != nil {
		return fmt.Errorf("layer %s: %w", shortDigest(layer.Digest), err)
	}

	// The digest is only proven once the stream has been read to its end, and
	// an unpack that stops early — because the tar ended before the blob did —
	// would leave it unproven. Draining forces the verification to run.
	if _, err := io.Copy(discard{}, body); err != nil {
		return fmt.Errorf("layer %s: %w", shortDigest(layer.Digest), err)
	}
	return nil
}

// isUnpackableLayer reports whether a media type names a tar archive.
//
// Both the OCI and Docker spellings, plus the artifact-specific types a plugin
// publisher may reasonably mint. Anything else — a config blob, a signature, a
// squashfs image — is refused rather than guessed at.
func isUnpackableLayer(mediaType string) bool {
	base, _, _ := strings.Cut(mediaType, ";")
	base = strings.TrimSpace(base)
	switch base {
	case "application/vnd.oci.image.layer.v1.tar",
		"application/vnd.oci.image.layer.v1.tar+gzip",
		"application/vnd.docker.image.rootfs.diff.tar",
		"application/vnd.docker.image.rootfs.diff.tar.gzip",
		"application/tar",
		"application/x-tar",
		"application/gzip",
		"application/tar+gzip",
		"application/x-tgz":
		return true
	}
	return strings.HasSuffix(base, ".tar") || strings.HasSuffix(base, ".tar+gzip") ||
		strings.HasSuffix(base, ".tar.gzip")
}

func shortDigest(d string) string {
	if _, hex, ok := strings.Cut(d, ":"); ok && len(hex) > 12 {
		return hex[:12]
	}
	return d
}

// discard is io.Discard without importing it for one use, kept local so the
// drain above reads as deliberate rather than as a stray copy.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
