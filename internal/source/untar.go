package source

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Unpacking a layer.
//
// This is the most dangerous code in the project, and it is worth saying why
// plainly: it writes attacker-chosen filenames, with attacker-chosen contents,
// to a path on the user's machine, in a process that will shortly hand the
// result to an agent holding their credentials. Every historical tar
// vulnerability applies — `../` traversal, absolute paths, symlinks pointing
// out of the tree and files written through them afterwards, hardlinks, device
// nodes, setuid bits, decompression bombs.
//
// So the rules here are absolute rather than configurable:
//
//   - Only regular files and directories are created. Everything else —
//     symlinks, hardlinks, devices, FIFOs — is dropped, not followed. This
//     matches what the cache and the adapters already do with symlinks, for the
//     same reason: a package that links to somewhere else on the machine would
//     otherwise install a pointer to wherever the link said.
//   - Every path is resolved against the destination and refused if it escapes,
//     after cleaning, on both path separators.
//   - Permissions are normalized. A layer does not get to decide that a file in
//     the user's plugin cache is setuid or world-writable.
//   - Totals are bounded, so a small download cannot become a full disk.

// layerLimits bounds one unpack operation across all its layers.
type layerLimits struct {
	remainingBytes   int64
	remainingEntries int
}

func newLayerLimits() *layerLimits {
	return &layerLimits{remainingBytes: maxPackageBytes, remainingEntries: maxPackageEntries}
}

// unpackLayer extracts one tar or tar+gzip layer into dst.
func unpackLayer(r io.Reader, mediaType, dst string, limits *layerLimits) error {
	var src io.Reader = r

	if strings.HasSuffix(mediaType, "+gzip") || strings.HasSuffix(mediaType, ".gz") {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("layer is not valid gzip: %w", err)
		}
		defer zr.Close()
		src = zr
	}

	tr := tar.NewReader(src)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("layer is not a valid tar archive: %w", err)
		}

		limits.remainingEntries--
		if limits.remainingEntries < 0 {
			return fmt.Errorf("artifact contains more than %d files; this does not look like a plugin", maxPackageEntries)
		}

		target, ok := safeJoin(dst, header.Name)
		if !ok {
			return fmt.Errorf("refusing to unpack %q: it escapes the package directory", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeEntry(tr, target, header, limits); err != nil {
				return err
			}
		default:
			// Symlinks, hardlinks, devices, FIFOs and sockets. Dropped in
			// silence by design: a plugin has no use for any of them, and the
			// alternative to dropping is deciding which ones are safe, which is
			// how every tar extractor gets this wrong.
			continue
		}
	}
}

// writeEntry writes one regular file, bounded and with normalized permissions.
func writeEntry(tr io.Reader, target string, header *tar.Header, limits *layerLimits) error {
	if header.Size > limits.remainingBytes {
		return fmt.Errorf("artifact expands to more than %d bytes; this does not look like a plugin", int64(maxPackageBytes))
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	// O_EXCL rather than O_TRUNC: two entries writing the same path within one
	// artifact is either a broken build or an attempt to have a reviewer read
	// one file while the agent loads another.
	//
	// The mode is ours, not the archive's. Preserving the executable bit is
	// worth doing for scripts/; preserving setuid, setgid or sticky is not
	// worth doing for anything.
	mode := os.FileMode(0o644)
	if header.FileInfo().Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("artifact contains %q more than once", header.Name)
		}
		return err
	}
	defer f.Close()

	// Copying header.Size+1 catches an archive whose header understates the
	// entry, which would otherwise slip past the running total.
	written, err := io.Copy(f, io.LimitReader(tr, header.Size+1))
	if err != nil {
		return err
	}
	if written > header.Size {
		return fmt.Errorf("entry %q is larger than its header declares", header.Name)
	}
	limits.remainingBytes -= written
	return nil
}

// safeJoin resolves an archive path against a destination, refusing anything
// that leaves it.
//
// The order of these checks is the whole point, and getting it wrong is the
// classic version of this bug: `filepath.Clean("/../escaped.txt")` returns
// `/escaped.txt`, because Clean treats `..` above the root as meaningless and
// silently drops it. So a traversal check applied *after* cleaning never sees
// the traversal it was written to catch — the path is quietly rewritten into
// something harmless-looking and unpacked without complaint.
//
// Nothing escapes either way, but "quietly rewrite a hostile archive" is not an
// acceptable outcome: an artifact containing `../` is malformed at best and
// hostile at worst, and both deserve to stop the install rather than be
// repaired. So every element is inspected before anything is cleaned, and the
// join is re-checked afterwards as a backstop.
func safeJoin(dst, name string) (string, bool) {
	if name == "" {
		return "", false
	}

	// Windows separators are meaningful on Windows and ordinary filename
	// characters elsewhere. Treating them as separators everywhere is the
	// conservative reading: the alternative is an archive that is one long
	// filename on Linux and an escape on Windows.
	normalized := strings.ReplaceAll(filepath.ToSlash(name), `\`, "/")

	// Absolute paths, before Clean can turn them into relative ones.
	if strings.HasPrefix(normalized, "/") {
		return "", false
	}
	// A Windows drive or UNC prefix, which filepath.Join would not neutralize
	// on the platform where it matters.
	if len(normalized) >= 2 && normalized[1] == ':' {
		return "", false
	}

	// Every element, before Clean can absorb one.
	for _, element := range strings.Split(normalized, "/") {
		if element == ".." {
			return "", false
		}
	}

	// Clean with the host's separator, then normalise back — on Windows
	// filepath.Clean("/.") returns "\\", which a TrimPrefix of "/" does not
	// strip, so "." slipped through as the destination directory itself.
	clean := filepath.ToSlash(filepath.Clean("/" + normalized))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "", false
	}

	target := filepath.Join(dst, filepath.FromSlash(clean))
	rel, err := filepath.Rel(dst, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return target, true
}
