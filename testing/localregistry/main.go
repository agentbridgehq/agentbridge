// Command localregistry serves one plugin directory as an OCI artifact.
//
// It exists so `agentbridge install oci://…` can be exercised without a
// registry account, a Docker daemon, or a network. It implements only the three
// endpoints a pull touches — manifest by tag, manifest by digest, and blob — and
// nothing else: no push, no auth, no catalogue. It is a test fixture, not a
// registry.
//
//	go run ./testing/localregistry ./path/to/plugin
//
// It prints the reference to install, and stays running until interrupted.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./testing/localregistry <plugin-dir>")
		os.Exit(2)
	}
	pluginDir := os.Args[1]

	layer, err := tarGzip(pluginDir)
	if err != nil {
		log.Fatalf("packing %s: %v", pluginDir, err)
	}
	layerDigest := digestOf(layer)

	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"artifactType":  "application/vnd.agentbridge.plugin.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.empty.v1+json",
			"digest":    digestOf([]byte("{}")),
			"size":      2,
		},
		"layers": []map[string]any{{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
			"digest":    layerDigest,
			"size":      len(layer),
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	manifestDigest := digestOf(manifest)

	// Port zero, so running two of these does not collide.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	addr := listener.Addr().String()

	fmt.Printf("serving %s\n\n", pluginDir)
	fmt.Printf("  agentbridge install oci://%s/acme/demo:v1.0.0\n", addr)
	fmt.Printf("  agentbridge scan    oci://%s/acme/demo@%s\n\n", addr, manifestDigest)
	fmt.Printf("plain HTTP is accepted only because this is loopback.\nctrl-c to stop.\n\n")

	log.Fatal(http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/v1.0.0"),
			strings.HasSuffix(r.URL.Path, "/manifests/"+manifestDigest):
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			writeOrLog(w, manifest)
		case strings.HasSuffix(r.URL.Path, "/blobs/"+layerDigest):
			w.Header().Set("Content-Type", "application/octet-stream")
			writeOrLog(w, layer)
		default:
			http.NotFound(w, r)
		}
	})))
}

// tarGzip packs a directory, skipping version control metadata and anything
// that is not a regular file — the same shape agentbridge will unpack.
func tarGzip(root string) ([]byte, error) {
	var buf strings.Builder
	zw := gzip.NewWriter(&stringWriter{&buf})
	tw := tar.NewWriter(zw)

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     filepath.ToSlash(rel),
			Mode:     int64(info.Mode().Perm()),
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		_, err = tw.Write(body)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

type stringWriter struct{ b *strings.Builder }

func (w *stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// writeOrLog reports a failed response body rather than dropping it, so a
// broken pull is visible in this server's log instead of only at the client.
func writeOrLog(w http.ResponseWriter, b []byte) {
	if _, err := w.Write(b); err != nil {
		log.Printf("writing response: %v", err)
	}
}
