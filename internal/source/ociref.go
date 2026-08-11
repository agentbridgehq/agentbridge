package source

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// OCI references.
//
// Why a container registry at all, for a thing that is not a container: every
// organization that has adopted containers already runs one, already mirrors it
// into air-gapped networks, already scans and signs what is in it, and already
// has an answer to who may push. A plugin distributed this way inherits all of
// that on the day it is published, which is a great deal more supply chain than
// a git URL provides — and none of it is ours to operate.
//
// The registry is also content-addressed by construction. A tag resolves to a
// manifest digest before anything is downloaded, and that digest is what gets
// recorded — the same pinning discipline as resolving a git tag to a commit,
// except the protocol enforces it rather than us.

// ociScheme is required rather than inferred. `ghcr.io/org/plugin` is a
// perfectly good *git* shorthand under the existing rules, and quietly choosing
// a different protocol based on the hostname would be the kind of surprise that
// costs trust once.
const ociScheme = "oci://"

// digestPattern is the subset of the OCI digest grammar we accept. Only sha256
// and sha512 are permitted: the spec allows a registry to offer others, and an
// algorithm we cannot verify is worse than no digest at all, because it looks
// like a pin.
var digestPattern = regexp.MustCompile(`^(sha256:[a-f0-9]{64}|sha512:[a-f0-9]{128})$`)

// repositoryPattern is the OCI distribution spec's repository name grammar.
var repositoryPattern = regexp.MustCompile(`^[a-z0-9]+([._-]|__)?([a-z0-9]+([._-]|__)?)*(/[a-z0-9]+([._-]|__)?([a-z0-9]+([._-]|__)?)*)*$`)

// tagPattern is the spec's tag grammar.
var tagPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)

// parseOCIRef reads `oci://registry/repository[:tag][@digest][#subdir]`.
func parseOCIRef(raw, trimmed string) (Ref, error) {
	body, subdir := splitSubdir(strings.TrimPrefix(trimmed, ociScheme))
	if body == "" {
		return Ref{}, fmt.Errorf("invalid reference %q: no registry or repository", raw)
	}

	var digest string
	if at := strings.LastIndex(body, "@"); at >= 0 {
		digest = body[at+1:]
		body = body[:at]
		if !digestPattern.MatchString(digest) {
			return Ref{}, fmt.Errorf("invalid digest %q in %q: expected sha256:<64 hex> or sha512:<128 hex>", digest, raw)
		}
	}

	// The tag separator is only recognized after the last `/`, so the port in
	// `localhost:5000/org/plugin` is not read as one.
	tag := ""
	if colon := strings.LastIndex(body, ":"); colon > strings.LastIndex(body, "/") {
		tag = body[colon+1:]
		body = body[:colon]
		if !tagPattern.MatchString(tag) {
			return Ref{}, fmt.Errorf("invalid tag %q in %q", tag, raw)
		}
	}

	slash := strings.Index(body, "/")
	if slash < 0 {
		return Ref{}, fmt.Errorf("invalid reference %q: expected a registry host and a repository, as oci://ghcr.io/org/plugin", raw)
	}
	registry, repository := body[:slash], body[slash+1:]
	if registry == "" || repository == "" {
		return Ref{}, fmt.Errorf("invalid reference %q: expected a registry host and a repository, as oci://ghcr.io/org/plugin", raw)
	}
	if !repositoryPattern.MatchString(repository) {
		return Ref{}, fmt.Errorf("invalid repository %q in %q: lowercase alphanumerics separated by . _ - or /", repository, raw)
	}
	if err := checkRegistryHost(registry, raw); err != nil {
		return Ref{}, err
	}

	// A reference with neither tag nor digest means `latest`, matching every
	// other registry client. It is still resolved to a digest before anything
	// is fetched, so it is no less pinned than a named tag — only less
	// informative about what was intended.
	if tag == "" && digest == "" {
		tag = "latest"
	}

	if subdir != "" {
		clean := path.Clean(subdir)
		if strings.HasPrefix(clean, "..") || path.IsAbs(clean) {
			return Ref{}, fmt.Errorf("invalid subdirectory %q in %q: must stay within the artifact", subdir, raw)
		}
		subdir = clean
	}

	return Ref{
		Raw:    raw,
		Kind:   KindOCI,
		URL:    ociScheme + registry + "/" + repository,
		Rev:    tag,
		Digest: digest,
		Subdir: subdir,
	}, nil
}

// checkRegistryHost rejects a host that cannot be reached safely.
//
// Everything but loopback must be HTTPS, and that is not configurable. A plugin
// is instruction text handed to an agent holding the user's credentials; the
// one thing worse than fetching it from a registry is fetching it over a
// connection anyone on the path can rewrite. Loopback is exempt because a
// registry on this machine has no path to be on.
func checkRegistryHost(registry, raw string) error {
	host := registry
	if h, _, ok := strings.Cut(registry, ":"); ok {
		host = h
	}
	if host == "" {
		return fmt.Errorf("invalid reference %q: no registry host", raw)
	}
	if strings.ContainsAny(registry, "/\\ ") {
		return fmt.Errorf("invalid registry %q in %q", registry, raw)
	}
	return nil
}

// isLoopback reports whether a registry host is on this machine, which is the
// only case where plain HTTP is allowed.
func isLoopback(registry string) bool {
	host := registry
	if h, _, ok := strings.Cut(registry, ":"); ok {
		host = h
	}
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}

// registryHost returns the host:port part of an OCI URL.
func registryHost(ociURL string) string {
	body := strings.TrimPrefix(ociURL, ociScheme)
	if slash := strings.Index(body, "/"); slash >= 0 {
		return body[:slash]
	}
	return body
}

// repositoryOf returns the repository part of an OCI URL.
func repositoryOf(ociURL string) string {
	body := strings.TrimPrefix(ociURL, ociScheme)
	if slash := strings.Index(body, "/"); slash >= 0 {
		return body[slash+1:]
	}
	return ""
}

// registryScheme returns the transport for a registry host.
func registryScheme(registry string) string {
	if isLoopback(registry) {
		return "http"
	}
	return "https"
}
