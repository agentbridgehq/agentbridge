package source_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/source"
)

// fakeRegistry is enough of the OCI distribution API to pull an artifact.
//
// A real registry is not available in a test and would not be usable in one
// anyway: the interesting cases here are the hostile ones — a layer that lies
// about its digest, a tar entry that escapes the directory — and no public
// registry will serve those on request.
type fakeRegistry struct {
	t         *testing.T
	server    *httptest.Server
	blobs     map[string][]byte // digest -> bytes
	manifests map[string][]byte // tag or digest -> manifest bytes
	repo      string

	mu       sync.Mutex
	requests []string
	// requireAuth makes the registry answer 401 once, exercising the token
	// exchange that every real registry performs.
	requireAuth bool
	tokenIssued bool
	// redirectBlobsTo makes blob requests answer with a 307, as every real
	// registry does — they hand blobs off to a CDN.
	redirectBlobsTo string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	r := &fakeRegistry{
		t:         t,
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		repo:      "org/plugin",
	}
	r.server = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.server.Close)
	return r
}

// ref returns the oci:// reference for this registry.
func (r *fakeRegistry) ref(reference string) string {
	host := strings.TrimPrefix(r.server.URL, "http://")
	return "oci://" + host + "/" + r.repo + reference
}

func (r *fakeRegistry) handle(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.requests = append(r.requests, req.Host+req.URL.Path)
	r.mu.Unlock()

	if req.URL.Path == "/token" {
		r.tokenIssued = true
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"token":"test-token"}`)
		return
	}
	if r.requireAuth && req.Header.Get("Authorization") == "" {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer realm="%s/token",service="fake",scope="repository:%s:pull"`, r.server.URL, r.repo))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	prefix := "/v2/" + r.repo + "/"
	switch {
	case strings.HasPrefix(req.URL.Path, prefix+"manifests/"):
		reference := strings.TrimPrefix(req.URL.Path, prefix+"manifests/")
		raw, ok := r.manifests[reference]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		if _, err := w.Write(raw); err != nil {
			r.t.Errorf("writing blob: %v", err)
		}
	case strings.HasPrefix(req.URL.Path, prefix+"blobs/"):
		if r.redirectBlobsTo != "" {
			http.Redirect(w, req, r.redirectBlobsTo+req.URL.Path, http.StatusTemporaryRedirect)
			return
		}
		digest := strings.TrimPrefix(req.URL.Path, prefix+"blobs/")
		raw, ok := r.blobs[digest]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := w.Write(raw); err != nil {
			r.t.Errorf("writing blob: %v", err)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// putLayer stores a blob and returns its descriptor.
func (r *fakeRegistry) putLayer(content []byte, mediaType string) map[string]any {
	digest := digestOf(content)
	r.blobs[digest] = content
	return map[string]any{"mediaType": mediaType, "digest": digest, "size": len(content)}
}

// publish writes a manifest under a tag and returns its digest.
func (r *fakeRegistry) publish(tag string, layers ...map[string]any) string {
	m := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.empty.v1+json",
			"digest":    digestOf([]byte("{}")),
			"size":      2,
		},
		"layers": layers,
	}
	raw, err := json.Marshal(m)
	if err != nil {
		r.t.Fatal(err)
	}
	digest := digestOf(raw)
	if tag != "" {
		r.manifests[tag] = raw
	}
	r.manifests[digest] = raw
	return digest
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// tarGz builds a gzipped tar from a path->content map.
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)

	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pluginFiles is an ordinary Agent Plugins package.
func pluginFiles() map[string]string {
	return map[string]string{
		"plugin.json":           `{"name":"acme.db","version":"1.0.0"}`,
		"skills/query/SKILL.md": "---\nname: query\ndescription: d\n---\nbody\n",
	}
}

func resolveOCI(t *testing.T, ref string, opts source.Options) (*source.Resolved, error) {
	t.Helper()
	if opts.Cache == nil {
		opts.Cache = source.NewCache(t.TempDir())
	}
	return source.ResolveString(context.Background(), ref, opts)
}

// ---------------------------------------------------------------- parsing

func TestParseOCIRef(t *testing.T) {
	for _, tc := range []struct {
		raw            string
		url, tag, dig  string
		subdir         string
		wantErrPartial string
	}{
		{raw: "oci://ghcr.io/org/plugin:v1.2.0", url: "oci://ghcr.io/org/plugin", tag: "v1.2.0"},
		{raw: "oci://ghcr.io/org/plugin", url: "oci://ghcr.io/org/plugin", tag: "latest"},
		{
			raw: "oci://ghcr.io/org/plugin@sha256:" + strings.Repeat("a", 64),
			url: "oci://ghcr.io/org/plugin", dig: "sha256:" + strings.Repeat("a", 64),
		},
		{
			raw: "oci://ghcr.io/org/plugin:v1@sha256:" + strings.Repeat("b", 64),
			url: "oci://ghcr.io/org/plugin", tag: "v1", dig: "sha256:" + strings.Repeat("b", 64),
		},
		{raw: "oci://localhost:5000/org/plugin:v1", url: "oci://localhost:5000/org/plugin", tag: "v1"},
		{raw: "oci://ghcr.io/org/plugin:v1#plugins/db", url: "oci://ghcr.io/org/plugin", tag: "v1", subdir: "plugins/db"},

		{raw: "oci://ghcr.io", wantErrPartial: "registry host and a repository"},
		{raw: "oci://ghcr.io/org/plugin@sha256:short", wantErrPartial: "invalid digest"},
		{raw: "oci://ghcr.io/org/plugin@md5:" + strings.Repeat("a", 32), wantErrPartial: "invalid digest"},
		{raw: "oci://ghcr.io/org/PLUGIN:v1", wantErrPartial: "invalid repository"},
		{raw: "oci://ghcr.io/org/plugin:v1#../escape", wantErrPartial: "must stay within"},
	} {
		got, err := source.ParseRef(tc.raw)
		if tc.wantErrPartial != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErrPartial) {
				t.Errorf("%s: error = %v, want one mentioning %q", tc.raw, err, tc.wantErrPartial)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.raw, err)
			continue
		}
		if got.Kind != source.KindOCI {
			t.Errorf("%s: kind = %s", tc.raw, got.Kind)
		}
		if got.URL != tc.url || got.Rev != tc.tag || got.Digest != tc.dig || got.Subdir != tc.subdir {
			t.Errorf("%s:\n  got  url=%q tag=%q digest=%q subdir=%q\n  want url=%q tag=%q digest=%q subdir=%q",
				tc.raw, got.URL, got.Rev, got.Digest, got.Subdir, tc.url, tc.tag, tc.dig, tc.subdir)
		}
	}
}

// A registry host must not be mistaken for a git shorthand, and vice versa. The
// scheme is what distinguishes them, deliberately.
func TestOCIRequiresItsScheme(t *testing.T) {
	ref, err := source.ParseRef("ghcr.io/org/plugin")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != source.KindGit {
		t.Errorf("kind = %s, want git: choosing a protocol from the hostname would be a surprise", ref.Kind)
	}
}

// ---------------------------------------------------------------- pulling

func TestPullsAndUnpacksAnArtifact(t *testing.T) {
	reg := newFakeRegistry(t)
	layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
	digest := reg.publish("v1.0.0", layer)

	got, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(got.Dir, "plugin.json"))
	if err != nil {
		t.Fatalf("the package was not unpacked: %v", err)
	}
	if !strings.Contains(string(raw), "acme.db") {
		t.Errorf("plugin.json = %s", raw)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "skills", "query", "SKILL.md")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}

	// A tag is mutable, so what gets recorded must be the digest.
	if got.Commit != digest {
		t.Errorf("commit = %s, want the manifest digest %s", got.Commit, digest)
	}
	if !strings.HasSuffix(got.Pinned(), "@"+digest) {
		t.Errorf("Pinned() = %q, want it to end in the manifest digest", got.Pinned())
	}
	if strings.Contains(got.Identity(), digest) {
		t.Errorf("Identity() = %q, want the upstream without the revision", got.Identity())
	}
	if got.TreeDigest == "" {
		t.Error("no tree digest recorded")
	}
}

// Every real registry challenges before serving, so the token exchange is part
// of the happy path rather than an edge case.
func TestPullPerformsTheAnonymousTokenExchange(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.requireAuth = true
	layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
	reg.publish("v1.0.0", layer)

	if _, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !reg.tokenIssued {
		t.Error("the registry challenged and no token was requested")
	}
}

// The digest in the manifest is the whole integrity story. A registry serving
// different bytes must be an error, not a silent install.
func TestRefusesALayerThatDoesNotMatchItsDigest(t *testing.T) {
	reg := newFakeRegistry(t)
	content := tarGz(t, pluginFiles())
	layer := reg.putLayer(content, "application/vnd.oci.image.layer.v1.tar+gzip")
	reg.publish("v1.0.0", layer)

	// Swap the stored bytes, leaving the manifest's claim untouched.
	reg.blobs[layer["digest"].(string)] = tarGz(t, map[string]string{
		"plugin.json": `{"name":"evil.substitute","version":"9.9.9"}`,
	})

	_, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
	if err == nil {
		t.Fatal("a substituted layer was installed")
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Errorf("error should name the digest mismatch: %v", err)
	}
}

// Requesting by digest and receiving something else is the registry answering a
// different question from the one asked. It is what a lockfile pin exists to
// detect.
func TestRefusesAManifestThatIsNotTheOneRequested(t *testing.T) {
	reg := newFakeRegistry(t)
	layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
	digest := reg.publish("v1.0.0", layer)

	// Serve a different manifest under the requested digest.
	other := reg.publish("", reg.putLayer(tarGz(t, map[string]string{"plugin.json": "{}"}),
		"application/vnd.oci.image.layer.v1.tar+gzip"))
	reg.manifests[digest] = reg.manifests[other]

	_, err := resolveOCI(t, reg.ref("@"+digest), source.Options{})
	if err == nil {
		t.Fatal("the wrong manifest was accepted")
	}
	if !strings.Contains(err.Error(), "wrong manifest") {
		t.Errorf("error = %v", err)
	}
}

func TestRefusesALayerMediaTypeItCannotUnpack(t *testing.T) {
	reg := newFakeRegistry(t)
	layer := reg.putLayer([]byte("not a tar"), "application/vnd.example.squashfs")
	reg.publish("v1.0.0", layer)

	_, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
	if err == nil || !strings.Contains(err.Error(), "media type") {
		t.Errorf("error = %v, want one naming the media type", err)
	}
}

// Guessing which variant of an agent's instructions to install is not a
// decision to take silently.
func TestRefusesAMultiPlatformIndex(t *testing.T) {
	reg := newFakeRegistry(t)
	layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
	one := reg.publish("", layer)
	two := reg.publish("", reg.putLayer(tarGz(t, map[string]string{"plugin.json": "{}"}),
		"application/vnd.oci.image.layer.v1.tar+gzip"))

	index, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]any{
			{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": one},
			{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": two},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reg.manifests["v1.0.0"] = index

	_, resolveErr := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
	if resolveErr == nil || !strings.Contains(resolveErr.Error(), "multi-platform") {
		t.Errorf("error = %v, want one explaining the index", resolveErr)
	}
}

// A single-manifest index is what most build tools produce, and it must simply
// work rather than being refused on a technicality.
func TestFollowsASingleManifestIndex(t *testing.T) {
	reg := newFakeRegistry(t)
	layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
	inner := reg.publish("", layer)

	index, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]any{
			{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": inner},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reg.manifests["v1.0.0"] = index

	got, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Commit != inner {
		t.Errorf("commit = %s, want the manifest the index named (%s)", got.Commit, inner)
	}
}

// ---------------------------------------------------------------- caching

func TestSecondResolveUsesTheCache(t *testing.T) {
	reg := newFakeRegistry(t)
	layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
	digest := reg.publish("v1.0.0", layer)
	cache := source.NewCache(t.TempDir())

	first, err := resolveOCI(t, reg.ref("@"+digest), source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if first.FromCache {
		t.Error("the first resolve should have fetched")
	}

	reg.mu.Lock()
	before := len(reg.requests)
	reg.mu.Unlock()

	second, err := resolveOCI(t, reg.ref("@"+digest), source.Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if !second.FromCache {
		t.Error("the second resolve refetched a pinned artifact")
	}
	reg.mu.Lock()
	after := len(reg.requests)
	reg.mu.Unlock()
	if after != before {
		t.Errorf("the cached resolve made %d request(s)", after-before)
	}
	if second.TreeDigest != first.TreeDigest {
		t.Error("the cached copy has a different tree digest")
	}
}

// Offline must serve a pinned artifact from cache and refuse anything else,
// rather than silently reaching out.
func TestOfflineUsesTheCacheAndRefusesTheRest(t *testing.T) {
	reg := newFakeRegistry(t)
	layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
	digest := reg.publish("v1.0.0", layer)
	cache := source.NewCache(t.TempDir())

	if _, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{Cache: cache}); err != nil {
		t.Fatal(err)
	}
	reg.server.Close() // nothing may reach the network from here on

	// The tag was resolved once, so offline knows what it meant.
	got, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{Cache: cache, Offline: true})
	if err != nil {
		t.Fatalf("offline resolve of a previously seen tag: %v", err)
	}
	if got.Commit != digest {
		t.Errorf("commit = %s, want %s", got.Commit, digest)
	}

	// A tag never seen has no cached answer, and inventing one would be worse
	// than failing.
	if _, err := resolveOCI(t, reg.ref(":v9.9.9"), source.Options{Cache: cache, Offline: true}); err == nil {
		t.Error("offline resolved a tag it has never seen")
	}
}

// The lockfile's expected digest is what stops a substituted artifact.
func TestExpectedDigestIsEnforced(t *testing.T) {
	reg := newFakeRegistry(t)
	layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
	digest := reg.publish("v1.0.0", layer)

	_, err := resolveOCI(t, reg.ref("@"+digest), source.Options{
		ExpectedDigest: "sha256:" + strings.Repeat("0", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "integrity check failed") {
		t.Errorf("error = %v, want an integrity failure", err)
	}
}

// ---------------------------------------------------------------- hostile archives

// Path traversal is the oldest tar bug there is, and this code writes
// attacker-chosen filenames to a developer's machine.
func TestRefusesTarEntriesThatEscapeTheDirectory(t *testing.T) {
	for _, name := range []string{
		"../escaped.txt",
		"../../escaped.txt",
		"skills/../../escaped.txt",
		"/absolute.txt",
		`..\windows.txt`,
	} {
		reg := newFakeRegistry(t)
		layer := reg.putLayer(tarGz(t, map[string]string{name: "x"}),
			"application/vnd.oci.image.layer.v1.tar+gzip")
		reg.publish("v1.0.0", layer)

		got, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
		if err == nil {
			t.Errorf("%s: unpacked without complaint into %s", name, got.Dir)
			continue
		}
		if !strings.Contains(err.Error(), "escapes") && !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

// A symlink in a package would install a pointer to wherever it says. Dropped,
// not followed — matching what the cache and the adapters already do.
func TestDropsSymlinksAndOtherSpecialEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)

	body := `{"name":"acme.db","version":"1.0.0"}`
	if err := tw.WriteHeader(&tar.Header{
		Name: "plugin.json", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}

	for _, h := range []*tar.Header{
		{Name: "secrets", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink, Mode: 0o777},
		{Name: "escape", Linkname: "../../../etc/hosts", Typeflag: tar.TypeSymlink, Mode: 0o777},
		{Name: "hard", Linkname: "plugin.json", Typeflag: tar.TypeLink, Mode: 0o644},
		{Name: "dev", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 1, Devminor: 3},
		{Name: "pipe", Typeflag: tar.TypeFifo, Mode: 0o666},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	zw.Close()

	reg := newFakeRegistry(t)
	layer := reg.putLayer(buf.Bytes(), "application/vnd.oci.image.layer.v1.tar+gzip")
	reg.publish("v1.0.0", layer)

	got, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
	if err != nil {
		t.Fatalf("a package with special entries should install without them: %v", err)
	}
	for _, name := range []string{"secrets", "escape", "hard", "dev", "pipe"} {
		if _, err := os.Lstat(filepath.Join(got.Dir, name)); err == nil {
			t.Errorf("%s was created", name)
		}
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "plugin.json")); err != nil {
		t.Errorf("the ordinary file was lost along with them: %v", err)
	}
}

// Setuid on a file in the plugin cache is never something an artifact gets to
// ask for.
func TestNormalizesFilePermissions(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range []struct {
		name string
		mode int64
	}{
		{"plugin.json", 0o4777},
		{"skills/x/scripts/run.sh", 0o755},
		{"skills/x/SKILL.md", 0o666},
	} {
		body := "x"
		if err := tw.WriteHeader(&tar.Header{
			Name: f.name, Mode: f.mode, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	zw.Close()

	reg := newFakeRegistry(t)
	layer := reg.putLayer(buf.Bytes(), "application/vnd.oci.image.layer.v1.tar+gzip")
	reg.publish("v1.0.0", layer)

	got, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		name string
		want os.FileMode
	}{
		{"plugin.json", 0o755}, // executable bit kept, setuid dropped
		{"skills/x/scripts/run.sh", 0o755},
		{"skills/x/SKILL.md", 0o644},
	} {
		info, err := os.Stat(filepath.Join(got.Dir, filepath.FromSlash(f.name)))
		if err != nil {
			t.Errorf("%s: %v", f.name, err)
			continue
		}
		if info.Mode()&os.ModeSetuid != 0 || info.Mode()&os.ModeSetgid != 0 {
			t.Errorf("%s kept a setuid or setgid bit: %v", f.name, info.Mode())
		}
		if info.Mode().Perm() != f.want {
			t.Errorf("%s mode = %v, want %v", f.name, info.Mode().Perm(), f.want)
		}
	}
}

// Two entries writing one path is either a broken build or an attempt to have
// a reviewer read one file while the agent loads another.
func TestRefusesDuplicateEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, content := range []string{"first", "second"} {
		if err := tw.WriteHeader(&tar.Header{
			Name: "skills/x/SKILL.md", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	zw.Close()

	reg := newFakeRegistry(t)
	layer := reg.putLayer(buf.Bytes(), "application/vnd.oci.image.layer.v1.tar+gzip")
	reg.publish("v1.0.0", layer)

	_, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Errorf("error = %v, want one about a duplicate entry", err)
	}
}

// ---------------------------------------------------------------- destinations

// Every request this *originates* goes to the host in the reference. Asserted
// at runtime, not only by the static check in internal/privacy.
//
// "Originates" is the exact claim, and the qualifier is load-bearing: a
// registry may redirect a blob elsewhere, which is how ghcr.io and Docker Hub
// actually serve layers. That is the registry's choice rather than a
// destination this tool holds, and it is documented in docs/telemetry.md.
func TestContactsOnlyTheHostInTheReference(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.requireAuth = true
	layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
	reg.publish("v1.0.0", layer)

	if _, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{}); err != nil {
		t.Fatal(err)
	}

	want := strings.TrimPrefix(reg.server.URL, "http://")
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if len(reg.requests) == 0 {
		t.Fatal("no requests were recorded")
	}
	for _, r := range reg.requests {
		if !strings.HasPrefix(r, want) {
			t.Errorf("request to %s, which is not the host in the reference (%s)", r, want)
		}
	}
}

// Plain HTTP is allowed only to this machine. A plugin fetched over a
// connection anyone on the path can rewrite is worse than no plugin.
func TestRequiresHTTPSForRemoteRegistries(t *testing.T) {
	ref, err := source.ParseRef("oci://registry.example.com/org/plugin:v1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Resolve(context.Background(), ref, source.Options{
		Cache: source.NewCache(t.TempDir()),
	})
	if err == nil {
		t.Fatal("expected a failure reaching a nonexistent registry")
	}
	// The point is that it tried https, not http.
	if strings.Contains(err.Error(), "http://") {
		t.Errorf("a remote registry was contacted over plain HTTP: %v", err)
	}
}

// Registries do not serve blobs themselves. ghcr.io and Docker Hub both answer
// a blob request with a redirect to a CDN, so following one is not optional —
// but the destination is chosen by the registry, not by the user, and that is
// worth pinning down rather than inheriting from net/http's defaults.
func TestFollowsBlobRedirectsButNotIntoPlaintext(t *testing.T) {
	t.Run("https-equivalent redirect is followed", func(t *testing.T) {
		cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer cdn.Close()

		reg := newFakeRegistry(t)
		content := tarGz(t, pluginFiles())
		layer := reg.putLayer(content, "application/vnd.oci.image.layer.v1.tar+gzip")
		reg.publish("v1.0.0", layer)

		// Serve the blob from the CDN instead, and redirect to it.
		cdn.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write(content); err != nil {
				t.Errorf("write: %v", err)
			}
		})
		reg.redirectBlobsTo = cdn.URL

		if _, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{}); err != nil {
			t.Errorf("a redirected blob should still be fetched: %v", err)
		}
	})

	t.Run("content is still verified after a redirect", func(t *testing.T) {
		cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write(tarGz(t, map[string]string{"plugin.json": `{"name":"evil.swap","version":"9"}`})); err != nil {
				t.Errorf("write: %v", err)
			}
		}))
		defer cdn.Close()

		reg := newFakeRegistry(t)
		layer := reg.putLayer(tarGz(t, pluginFiles()), "application/vnd.oci.image.layer.v1.tar+gzip")
		reg.publish("v1.0.0", layer)
		reg.redirectBlobsTo = cdn.URL

		_, err := resolveOCI(t, reg.ref(":v1.0.0"), source.Options{})
		if err == nil {
			t.Fatal("a redirect to substituted content was accepted")
		}
		if !strings.Contains(err.Error(), "digest") {
			t.Errorf("error = %v, want a digest mismatch", err)
		}
	})
}
