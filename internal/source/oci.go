package source

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A minimal OCI distribution client.
//
// Written against the specification rather than pulled from a library, for two
// reasons that pull the same way. The subset needed here is small — resolve a
// tag, fetch a manifest, fetch some blobs — while the registry libraries carry
// build tooling, credential helpers and cloud SDKs that would multiply this
// project's dependency surface many times over. And this code sits in the
// supply chain: it is the component that decides which bytes become instruction
// text for an agent. That is the last place to inherit a dependency tree nobody
// has read.
//
// What it does not do, deliberately: push, list, mount, authenticate with
// stored credentials, or negotiate platforms. Pulling a public artifact is the
// whole job.

// Media types. Docker's are accepted alongside OCI's because registries still
// serve them and a user does not care which spelling their registry chose.
const (
	mediaOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	mediaOCIIndex       = "application/vnd.oci.image.index.v1+json"
	mediaDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	mediaDockerIndex    = "application/vnd.docker.distribution.manifest.list.v2+json"
)

// Limits.
//
// Every one of these exists because the bytes are chosen by whoever published
// the artifact, and an unbounded read from a remote is a denial of service
// waiting for a bad day. They are also what stops `agentbridge install` from
// cheerfully unpacking a two-gigabyte container image that was never a plugin:
// the failure is a clear message about size rather than a full disk.
const (
	maxManifestBytes  = 4 << 20   // 4 MiB
	maxBlobBytes      = 128 << 20 // 128 MiB compressed, per blob
	maxPackageBytes   = 512 << 20 // 512 MiB uncompressed, whole package
	maxPackageEntries = 50_000
	maxLayers         = 64
	registryTimeout   = 2 * time.Minute
)

// descriptor is the OCI content descriptor.
type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// manifest covers both the OCI image manifest and the Docker v2 manifest, which
// are structurally identical for our purposes.
type manifest struct {
	MediaType    string       `json:"mediaType"`
	ArtifactType string       `json:"artifactType,omitempty"`
	Config       descriptor   `json:"config"`
	Layers       []descriptor `json:"layers"`
	Manifests    []descriptor `json:"manifests,omitempty"` // an index
}

// registryClient talks to one registry.
type registryClient struct {
	http *http.Client
	// tokens caches a bearer token per scope for the life of one resolution, so
	// fetching a manifest and four blobs does not mean five token exchanges.
	tokens map[string]string
}

func newRegistryClient() *registryClient {
	return &registryClient{
		// A timeout on the client rather than per request: an install that
		// hangs forever against an unresponsive registry is indistinguishable
		// from a broken tool.
		http:   &http.Client{Timeout: registryTimeout},
		tokens: map[string]string{},
	}
}

// do issues a request, obtaining a token if the registry asks for one.
//
// Anonymous pull is the only flow supported. A registry that requires
// credentials produces a clear error rather than a silent search for them:
// reading a developer's Docker config would mean this tool starts using
// credentials the user never mentioned, against a host they did not name in the
// reference.
func (c *registryClient) do(ctx context.Context, method, url string, accept []string) (*http.Response, error) {
	resp, err := c.attempt(ctx, method, url, accept, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	resp.Body.Close()

	token, err := c.token(ctx, challenge)
	if err != nil {
		return nil, err
	}
	return c.attempt(ctx, method, url, accept, token)
}

func (c *registryClient) attempt(ctx context.Context, method, url string, accept []string, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	if len(accept) > 0 {
		req.Header.Set("Accept", strings.Join(accept, ", "))
	}
	req.Header.Set("User-Agent", "agentbridge")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return c.http.Do(req)
}

// token performs the anonymous half of the Docker token flow.
func (c *registryClient) token(ctx context.Context, challenge string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", fmt.Errorf("registry requires authentication that agentbridge does not perform (%q); "+
			"only public artifacts can be pulled", firstWord(challenge))
	}
	params := parseChallenge(challenge[len("bearer "):])
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("registry sent an authentication challenge with no realm")
	}

	// The realm is chosen by the registry, not by the user, and it routinely
	// names a different host — Docker Hub answers for registry-1.docker.io from
	// auth.docker.io. So this is the one request whose destination is not taken
	// from the reference, and it is worth being precise about what that costs:
	// the request carries no credentials and no identity, but it does tell that
	// host which repository this machine is pulling. Documented in
	// docs/telemetry.md rather than left as a surprise.
	//
	// What is enforced is the transport. An anonymous token fetched over plain
	// HTTP can be swapped by anyone on the path, and the reply decides which
	// bytes become instruction text for an agent.
	realmURL, err := url.Parse(realm)
	if err != nil || realmURL.Host == "" {
		return "", fmt.Errorf("registry sent an unusable authentication realm %q", realm)
	}
	if realmURL.Scheme != "https" && !isLoopback(realmURL.Host) {
		return "", fmt.Errorf("registry directed authentication to %q, which is not https", realm)
	}

	url := realm
	var query []string
	if s := params["service"]; s != "" {
		query = append(query, "service="+urlQueryEscape(s))
	}
	if s := params["scope"]; s != "" {
		query = append(query, "scope="+urlQueryEscape(s))
	}
	if len(query) > 0 {
		url += "?" + strings.Join(query, "&")
	}
	if cached, ok := c.tokens[url]; ok {
		return cached, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "agentbridge")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("could not obtain an anonymous pull token: %s", resp.Status)
	}

	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifestBytes)).Decode(&body); err != nil {
		return "", fmt.Errorf("could not read the token response: %w", err)
	}
	token := body.Token
	if token == "" {
		token = body.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("registry returned an empty pull token")
	}
	c.tokens[url] = token
	return token, nil
}

// fetchManifest retrieves a manifest by tag or digest and returns it with the
// digest it actually hashes to.
//
// The digest is computed here rather than taken from the Docker-Content-Digest
// header. A header is a claim by the server; the hash is the fact. Since this
// digest becomes the pin recorded in the lockfile, trusting the claim would
// make the pin exactly as trustworthy as the registry on the day it was read.
func (c *registryClient) fetchManifest(ctx context.Context, ref Ref, reference string) ([]byte, string, error) {
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s",
		registryScheme(registryHost(ref.URL)), registryHost(ref.URL), repositoryOf(ref.URL), reference)

	resp, err := c.do(ctx, http.MethodGet, url, []string{
		mediaOCIManifest, mediaOCIIndex, mediaDockerManifest, mediaDockerIndex,
	})
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", registryError(resp, ref, reference)
	}

	raw, err := readLimited(resp.Body, maxManifestBytes, "manifest")
	if err != nil {
		return nil, "", err
	}
	return raw, "sha256:" + hex.EncodeToString(sha256Sum(raw)), nil
}

// fetchBlob retrieves a blob and verifies it against its digest as it is read.
//
// Verification is streaming rather than after the fact: a layer is extracted as
// it arrives, so checking the digest only at the end would mean the bytes have
// already been written to disk by the time we discover they were wrong.
func (c *registryClient) fetchBlob(ctx context.Context, ref Ref, d descriptor) (io.ReadCloser, error) {
	if d.Size > maxBlobBytes {
		return nil, fmt.Errorf("blob %s is %d bytes, over the %d-byte limit", d.Digest, d.Size, int64(maxBlobBytes))
	}
	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s",
		registryScheme(registryHost(ref.URL)), registryHost(ref.URL), repositoryOf(ref.URL), d.Digest)

	resp, err := c.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, registryError(resp, ref, d.Digest)
	}

	verifier, err := newDigestVerifier(d.Digest)
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	return &verifiedReader{
		body:     resp.Body,
		limited:  io.LimitReader(resp.Body, maxBlobBytes+1),
		verifier: verifier,
		expected: d.Digest,
	}, nil
}

// verifiedReader fails the read if the bytes do not hash to the expected
// digest, at the point the stream ends.
type verifiedReader struct {
	body     io.ReadCloser
	limited  io.Reader
	verifier hash.Hash
	expected string
	read     int64
}

func (v *verifiedReader) Read(p []byte) (int, error) {
	n, err := v.limited.Read(p)
	if n > 0 {
		v.verifier.Write(p[:n])
		v.read += int64(n)
		if v.read > maxBlobBytes {
			return n, fmt.Errorf("blob %s exceeds the %d-byte limit", v.expected, int64(maxBlobBytes))
		}
	}
	if err == io.EOF {
		algorithm, _, _ := strings.Cut(v.expected, ":")
		actual := algorithm + ":" + hex.EncodeToString(v.verifier.Sum(nil))
		if actual != v.expected {
			return n, fmt.Errorf("blob does not match its digest:\n  expected %s\n  actual   %s\n"+
				"the registry served different bytes from the ones the manifest names", v.expected, actual)
		}
	}
	return n, err
}

func (v *verifiedReader) Close() error { return v.body.Close() }

func newDigestVerifier(digest string) (hash.Hash, error) {
	algorithm, _, ok := strings.Cut(digest, ":")
	if !ok {
		return nil, fmt.Errorf("malformed digest %q", digest)
	}
	switch algorithm {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	}
	return nil, fmt.Errorf("unsupported digest algorithm %q: a digest we cannot verify is worse than none, "+
		"because it looks like a pin", algorithm)
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// registryError turns an HTTP status into something a person can act on.
func registryError(resp *http.Response, ref Ref, reference string) error {
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%s: not found in %s (checked %s)", reference, ref.URL, resp.Request.URL.Host)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s: %s requires credentials; agentbridge pulls anonymously and does not read "+
			"credentials you have not named", reference, ref.URL)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s: the registry is rate limiting this machine", ref.URL)
	}
	return fmt.Errorf("%s: registry returned %s", ref.URL, resp.Status)
}

// readLimited reads at most limit bytes and reports an overrun rather than
// silently truncating, which would leave malformed JSON to be blamed instead.
func readLimited(r io.Reader, limit int64, what string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s is larger than the %d-byte limit", what, limit)
	}
	return raw, nil
}

// parseChallenge reads the comma-separated key="value" parameters of a
// WWW-Authenticate header.
func parseChallenge(s string) map[string]string {
	out := map[string]string{}
	for _, part := range splitOutsideQuotes(s) {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return out
}

// splitOutsideQuotes splits on commas that are not inside a quoted value. A
// scope parameter routinely contains commas, so a naive split loses half of it
// and the token request comes back with the wrong permissions.
func splitOutsideQuotes(s string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			current.WriteRune(r)
		case r == ',' && !inQuotes:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// urlQueryEscape percent-encodes a query parameter value. Written out rather
// than pulled from net/url so that the scope's colons and slashes — which
// registries expect verbatim — survive.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == ':', c == '/':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}
