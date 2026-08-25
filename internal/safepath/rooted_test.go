package safepath_test

import (
	"errors"
	"testing"

	"github.com/agentbridgehq/agentbridge/internal/safepath"
)

// A rooted path must be refused on every platform, not only where the host's
// filepath.IsAbs happens to recognise it.
func TestResolveRejectsRootedPathsOnEveryPlatform(t *testing.T) {
	root, err := safepath.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/etc/passwd", `\windows\system32`, "/", `\`} {
		if _, err := root.Resolve(p); !errors.Is(err, safepath.ErrAbsolute) {
			t.Errorf("Resolve(%q) = %v, want ErrAbsolute", p, err)
		}
	}
}
