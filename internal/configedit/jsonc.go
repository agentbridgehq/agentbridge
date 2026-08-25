// Package configedit performs surgical, formatting-preserving edits to the
// configuration files agent clients own.
//
// This is the least glamorous package in the codebase and the one most likely
// to lose a user permanently. These files are hand-written: they carry
// comments explaining why a server exists, deliberate key ordering, and
// whatever indentation the author prefers. A tool that reads them, re-encodes
// them, and writes them back destroys all of that on the first install. The
// user does not file a bug; they uninstall.
//
// So the contract here is narrow and absolute: touch the bytes we are changing
// and no others. A file we edit and then revert must be byte-identical to how
// it started.
//
// Two strategies, chosen by what the format allows:
//
//   - JSON and JSONC, in this file, via a syntax tree that preserves comments
//     and whitespace exactly (github.com/tailscale/hujson). VS Code and its
//     forks ship configuration with comments in it, so a plain encoding/json
//     round trip is not an option.
//   - TOML, in block.go, via a marker-delimited region we own outright. There
//     is no comment-preserving TOML editor worth the dependency, and owning a
//     clearly-labelled region is more honest than pretending to edit around
//     the user's content.
package configedit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tailscale/hujson"
)

// JSONDoc is a JSON or JSONC document open for editing.
type JSONDoc struct {
	path     string
	original []byte
	// existed records whether the file was on disk when loaded, which
	// determines whether a plan creates or modifies it.
	existed bool
	value   hujson.Value
}

// LoadJSON reads a JSON/JSONC file for editing. A missing file is not an
// error: it yields an empty document, since "create the config" and "add to
// the config" are the same operation from the caller's point of view.
func LoadJSON(path string) (*JSONDoc, error) {
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		// A missing file is not an error: "create the config" and "add to the
		// config" are the same operation to a caller. err is not cleared here
		// because nothing reads it again on this branch.
		v, perr := hujson.Parse([]byte("{}\n"))
		if perr != nil {
			return nil, perr
		}
		return &JSONDoc{path: path, original: nil, existed: false, value: v}, nil
	default:
		return nil, err
	}

	// An existing but empty file is common when a client has created the file
	// without writing to it yet.
	if len(bytes.TrimSpace(raw)) == 0 {
		v, perr := hujson.Parse([]byte("{}\n"))
		if perr != nil {
			return nil, perr
		}
		return &JSONDoc{path: path, original: raw, existed: true, value: v}, nil
	}

	v, err := hujson.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &JSONDoc{path: path, original: raw, existed: true, value: v}, nil
}

// Path returns the file's path.
func (d *JSONDoc) Path() string { return d.path }

// Existed reports whether the file was present on disk.
func (d *JSONDoc) Existed() bool { return d.existed }

// Original returns the file's bytes as loaded, or nil if it did not exist.
func (d *JSONDoc) Original() []byte { return d.original }

// Bytes returns the current document, preserving all untouched formatting and
// comments. If nothing was changed, this is byte-identical to Original.
func (d *JSONDoc) Bytes() ([]byte, error) {
	out := d.value.Pack()
	if !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	return out, nil
}

// Set writes value at the given key path, creating intermediate objects as
// needed. Existing sibling keys, their order, and any comments around them are
// left untouched.
func (d *JSONDoc) Set(keys []string, value any) error {
	if len(keys) == 0 {
		return fmt.Errorf("empty key path")
	}

	current, err := d.decode()
	if err != nil {
		return err
	}

	var ops []patchOp

	// RFC 6902 "add" fails if the parent container does not exist, so any
	// missing ancestors are created first, in order.
	node := current
	for i := 0; i < len(keys)-1; i++ {
		next, ok := node[keys[i]]
		if child, isObj := next.(map[string]any); ok && isObj {
			node = child
			continue
		}
		if ok {
			return fmt.Errorf("%s: %q is not an object", d.path, strings.Join(keys[:i+1], "."))
		}
		ops = append(ops, patchOp{Op: "add", Path: pointer(keys[:i+1]), Value: map[string]any{}})
		child := map[string]any{}
		node[keys[i]] = child
		node = child
	}

	// Create the ancestors first, so the parent exists and its indentation can
	// be read off a real sibling rather than guessed.
	if len(ops) > 0 {
		if err := d.patch(ops); err != nil {
			return err
		}
		for i := range ops {
			if err := d.indentInserted(keys[:i+1]); err != nil {
				return err
			}
		}
	}

	indent := d.indentFor(keys)
	if indent == "" {
		indent = depthIndent(len(keys))
	}

	// Encode the value pre-indented for its final depth. hujson preserves the
	// formatting inside a patch value, so this is what makes an added entry
	// read like the rest of the file instead of one long line.
	raw, err := json.MarshalIndent(value, indent, "  ")
	if err != nil {
		return err
	}
	if err := d.patchRaw("add", pointer(keys), raw); err != nil {
		return err
	}
	return d.indentInserted(keys)
}

// indentFor returns the leading whitespace a new member at this key path
// should carry, read from its future siblings.
func indentFor(obj *hujson.Object) string {
	for i := range obj.Members {
		if ws := trailingWhitespaceLine(obj.Members[i].Name.BeforeExtra); ws != nil {
			return string(ws[1:]) // drop the newline
		}
	}
	return ""
}

func (d *JSONDoc) indentFor(keys []string) string {
	parent := d.value.Find(pointer(keys[:len(keys)-1]))
	if parent == nil {
		return ""
	}
	obj, ok := parent.Value.(*hujson.Object)
	if !ok {
		return ""
	}
	return indentFor(obj)
}

// indentInserted gives a newly added member the leading whitespace its
// siblings have.
//
// The patch machinery preserves the formatting *inside* an inserted value but
// inserts the member itself with no leading whitespace, so a new entry lands
// jammed onto the end of the previous line. That is technically a
// minimal-diff edit and practically unreadable, which is not the trade this
// package is trying to make: preserving the user's formatting is only worth
// something if what we add is legible too.
//
// The indent is copied from an existing sibling rather than assumed, so a file
// using tabs or four spaces gets its own convention back.
func (d *JSONDoc) indentInserted(keys []string) error {
	parent := d.value.Find(pointer(keys[:len(keys)-1]))
	if parent == nil {
		return nil
	}
	obj, ok := parent.Value.(*hujson.Object)
	if !ok {
		return nil
	}

	name := keys[len(keys)-1]
	idx := -1
	for i := range obj.Members {
		if literalString(obj.Members[i].Name) == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	if len(obj.Members[idx].Name.BeforeExtra) == 0 {
		obj.Members[idx].Name.BeforeExtra = hujson.Extra(siblingIndent(obj, idx, len(keys)))
	}
	// A container that had no members has no closing-brace whitespace either,
	// so without this the braces pile up on one line as `}}}`. Only an empty
	// extra is filled in; a container the user already formatted keeps its own.
	if len(obj.AfterExtra) == 0 && len(obj.Members) > 0 {
		obj.AfterExtra = hujson.Extra("\n" + depthIndent(len(keys)-1))
	}
	// The space after the colon, which the patch machinery also omits.
	if len(obj.Members[idx].Value.BeforeExtra) == 0 {
		obj.Members[idx].Value.BeforeExtra = hujson.Extra(" ")
	}
	return nil
}

// siblingIndent picks the whitespace to put before a member, preferring an
// existing sibling's so the file keeps its own indentation style.
//
// depth is how many levels down the new member sits, used only when there is no
// sibling to learn from — a file this tool is creating from nothing. Falling
// back to a bare newline there produced valid but unreadable output, with every
// level flush left and the closing braces collapsed onto one line, which fails
// the same standard applied to files we edit: what we add has to be legible.
func siblingIndent(obj *hujson.Object, idx, depth int) []byte {
	for i := idx - 1; i >= 0; i-- {
		if ws := trailingWhitespaceLine(obj.Members[i].Name.BeforeExtra); ws != nil {
			return ws
		}
	}
	for i := idx + 1; i < len(obj.Members); i++ {
		if ws := trailingWhitespaceLine(obj.Members[i].Name.BeforeExtra); ws != nil {
			return ws
		}
	}
	return []byte("\n" + depthIndent(depth))
}

// depthIndent is the conventional two-space indent for a given nesting level.
func depthIndent(depth int) string {
	if depth < 0 {
		return ""
	}
	return strings.Repeat("  ", depth)
}

// trailingWhitespaceLine extracts the newline-plus-indent at the end of an
// Extra, skipping over any comments it also contains.
func trailingWhitespaceLine(extra hujson.Extra) []byte {
	i := bytes.LastIndexByte(extra, '\n')
	if i < 0 {
		return nil
	}
	indent := extra[i+1:]
	for _, c := range indent {
		if c != ' ' && c != '\t' {
			return nil
		}
	}
	out := make([]byte, 0, len(indent)+1)
	out = append(out, '\n')
	return append(out, indent...)
}

// literalString returns an object member name's decoded string.
func literalString(v Value) string {
	lit, ok := v.Value.(hujson.Literal)
	if !ok {
		return ""
	}
	return lit.String()
}

// Value is an alias so helper signatures do not leak the import into callers.
type Value = hujson.Value

// Delete removes the key at the given path. Removing something that is not
// there is a no-op, not an error: uninstall must be idempotent, because the
// user may well have deleted the entry by hand already.
func (d *JSONDoc) Delete(keys []string) error {
	if len(keys) == 0 {
		return fmt.Errorf("empty key path")
	}
	ok, err := d.Has(keys)
	if err != nil || !ok {
		return err
	}
	return d.patch([]patchOp{{Op: "remove", Path: pointer(keys)}})
}

// Has reports whether a key path is present.
func (d *JSONDoc) Has(keys []string) (bool, error) {
	current, err := d.decode()
	if err != nil {
		return false, err
	}
	node := any(current)
	for _, k := range keys {
		obj, ok := node.(map[string]any)
		if !ok {
			return false, nil
		}
		node, ok = obj[k]
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// Keys returns the keys of the object at the given path, or nil if it is
// absent or not an object.
func (d *JSONDoc) Keys(keys []string) ([]string, error) {
	current, err := d.decode()
	if err != nil {
		return nil, err
	}
	node := any(current)
	for _, k := range keys {
		obj, ok := node.(map[string]any)
		if !ok {
			return nil, nil
		}
		node, ok = obj[k]
		if !ok {
			return nil, nil
		}
	}
	obj, ok := node.(map[string]any)
	if !ok {
		return nil, nil
	}
	out := make([]string, 0, len(obj))
	for k := range obj {
		out = append(out, k)
	}
	return out, nil
}

// StringAt returns the string value at a key path, or an error if it is absent
// or not a string.
func (d *JSONDoc) StringAt(keys []string) (string, error) {
	current, err := d.decode()
	if err != nil {
		return "", err
	}
	node := any(current)
	for _, k := range keys {
		obj, ok := node.(map[string]any)
		if !ok {
			return "", fmt.Errorf("%s: %s is not an object", d.path, strings.Join(keys, "."))
		}
		node, ok = obj[k]
		if !ok {
			return "", fmt.Errorf("%s: %s not found", d.path, strings.Join(keys, "."))
		}
	}
	s, ok := node.(string)
	if !ok {
		return "", fmt.Errorf("%s: %s is not a string", d.path, strings.Join(keys, "."))
	}
	return s, nil
}

// StringSliceAt returns the array of strings at a key path, or nil if it is
// absent or not an array of strings.
func (d *JSONDoc) StringSliceAt(keys []string) ([]string, error) {
	current, err := d.decode()
	if err != nil {
		return nil, err
	}
	node := any(current)
	for _, k := range keys {
		obj, ok := node.(map[string]any)
		if !ok {
			return nil, nil
		}
		node, ok = obj[k]
		if !ok {
			return nil, nil
		}
	}
	arr, ok := node.([]any)
	if !ok {
		return nil, nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, nil
		}
		out = append(out, s)
	}
	return out, nil
}

// decode returns the document's data as plain Go values, for existence checks.
// Comments and trailing commas are removed from the clone only; the document
// being edited is untouched.
func (d *JSONDoc) decode() (map[string]any, error) {
	clone := d.value.Clone()
	clone.Standardize()

	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(clone.Pack()))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", d.path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

type patchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

func (d *JSONDoc) patch(ops []patchOp) error {
	raw, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	if err := d.value.Patch(raw); err != nil {
		return fmt.Errorf("%s: %w", d.path, err)
	}
	return nil
}

// patchRaw applies one operation with a pre-encoded value, so the value's own
// formatting reaches the document intact.
func (d *JSONDoc) patchRaw(op, path string, value []byte) error {
	quotedPath, err := json.Marshal(path)
	if err != nil {
		return err
	}
	patch := fmt.Sprintf(`[{"op":%q,"path":%s,"value":%s}]`, op, quotedPath, value)
	if err := d.value.Patch([]byte(patch)); err != nil {
		return fmt.Errorf("%s: %w", d.path, err)
	}
	return nil
}

// pointer builds an RFC 6901 JSON Pointer from key segments.
func pointer(keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteByte('/')
		// Order matters: "~" must be escaped before "/" introduces new "~1"
		// sequences that would then be escaped a second time.
		k = strings.ReplaceAll(k, "~", "~0")
		k = strings.ReplaceAll(k, "/", "~1")
		b.WriteString(k)
	}
	return b.String()
}
