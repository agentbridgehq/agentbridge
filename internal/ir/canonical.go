package ir

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// DigestPrefix labels the hash algorithm, matching OCI convention so digests
// can be used directly as content addresses when M3-5 lands.
const DigestPrefix = "sha256:"

// Canonical returns a deterministic JSON encoding of v.
//
// Determinism is required because digests computed on different machines, in
// different processes, and across releases must agree — that is what makes the
// lockfile meaningful. Two things are normalized:
//
//   - Object key order. Go's encoder already sorts map keys; round-tripping
//     through a generic value applies the same ordering to json.RawMessage
//     content, which would otherwise contribute its original byte layout,
//     including whitespace, to the hash.
//   - HTML escaping. Disabled, so a URL in a manifest hashes the same whether
//     it came through the encoder once or twice.
//
// Numbers are decoded as json.Number, so their original textual form survives
// rather than being widened to float64.
func Canonical(v any) ([]byte, error) {
	raw, err := marshalNoEscape(v)
	if err != nil {
		return nil, err
	}
	var generic any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, fmt.Errorf("canonicalizing: %w", err)
	}
	return marshalNoEscape(generic)
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// digestView is the subset of a Plugin that contributes to its digest.
//
// Origin.Root is excluded: it is an absolute path that differs on every
// machine, and including it would make the same plugin hash differently for
// each developer, defeating the purpose. Everything else about the origin is
// included, because importing the same bytes through a different dialect is a
// genuinely different result.
type digestView struct {
	IRVersion    string                     `json:"irVersion"`
	Name         string                     `json:"name"`
	Version      string                     `json:"version,omitempty"`
	Description  string                     `json:"description,omitempty"`
	Author       *Author                    `json:"author,omitempty"`
	Homepage     string                     `json:"homepage,omitempty"`
	Repository   string                     `json:"repository,omitempty"`
	License      string                     `json:"license,omitempty"`
	Keywords     []string                   `json:"keywords,omitempty"`
	Skills       []Skill                    `json:"skills,omitempty"`
	MCPServers   []MCPServer                `json:"mcpServers,omitempty"`
	Extensions   map[string]json.RawMessage `json:"extensions,omitempty"`
	Native       map[string]json.RawMessage `json:"native,omitempty"`
	Capabilities Capabilities               `json:"capabilities"`
	Dialect      Dialect                    `json:"dialect"`
	SchemaID     string                     `json:"schemaId,omitempty"`
	ManifestPath string                     `json:"manifestPath,omitempty"`
}

// Digest returns the content address of the plugin: sha256 over its canonical
// form, excluding machine-specific fields.
func (p *Plugin) Digest() (string, error) {
	view := digestView{
		IRVersion:    p.IRVersion,
		Name:         p.Name,
		Version:      p.Version,
		Description:  p.Description,
		Author:       p.Author,
		Homepage:     p.Homepage,
		Repository:   p.Repository,
		License:      p.License,
		Keywords:     p.Keywords,
		Skills:       p.Skills,
		MCPServers:   p.MCPServers,
		Extensions:   p.Extensions,
		Native:       p.Native,
		Capabilities: p.Capabilities,
		Dialect:      p.Origin.Dialect,
		SchemaID:     p.Origin.SchemaID,
		ManifestPath: p.Origin.ManifestPath,
	}
	canonical, err := Canonical(view)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return DigestPrefix + hex.EncodeToString(sum[:]), nil
}

// serverHashView is the subset of an MCPServer that contributes to its content
// hash. ContentHash itself is excluded, since it is the output.
type serverHashView struct {
	Name      string            `json:"name"`
	Transport Transport         `json:"transport"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// ComputeContentHash sets and returns the server's content hash.
func (s *MCPServer) ComputeContentHash() (string, error) {
	canonical, err := Canonical(serverHashView{
		Name:      s.Name,
		Transport: s.Transport,
		Command:   s.Command,
		Args:      s.Args,
		Env:       s.Env,
		Cwd:       s.Cwd,
		URL:       s.URL,
		Headers:   s.Headers,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	s.ContentHash = DigestPrefix + hex.EncodeToString(sum[:])
	return s.ContentHash, nil
}

// HashBytes returns the digest of arbitrary bytes in the same format, used for
// skill file content hashes.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return DigestPrefix + hex.EncodeToString(sum[:])
}
