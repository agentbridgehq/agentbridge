package schema

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxNameLength is the manifest schema's upper bound on `name`.
const MaxNameLength = 64

// ValidateName enforces the Agent Plugins 1.0.0 plugin name rule.
//
// The canonical schema expresses this as
//
//	^(?!.*(?:--|\.\.))[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$
//
// which Go's regexp package cannot compile: RE2 has no lookahead, by design.
// Rather than pull in a second regex engine for one field, the rule is
// implemented directly. That also lets each violation report what is actually
// wrong instead of "does not match pattern", which matters because this
// message is the first thing a plugin author sees when their plugin will not
// load anywhere.
//
// Note that the pattern forbids only the doubled sequences "--" and ".."; the
// mixed sequences "-." and ".-" are permitted. This implementation matches that
// exactly, deliberately, rather than applying the stricter rule a reader might
// expect.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("name must not be empty")
	}
	if n := utf8.RuneCountInString(name); n > MaxNameLength {
		return fmt.Errorf("name must be at most %d characters, got %d", MaxNameLength, n)
	}
	if strings.ContainsAny(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return fmt.Errorf("name must be lowercase, got %q", name)
	}

	for i, r := range name {
		if isNameAlnum(r) || r == '-' || r == '.' {
			continue
		}
		return fmt.Errorf("name may only contain lowercase letters, digits, %q and %q; found %q at position %d",
			"-", ".", string(r), i)
	}

	first, _ := utf8.DecodeRuneInString(name)
	last, _ := utf8.DecodeLastRuneInString(name)
	if !isNameAlnum(first) {
		return fmt.Errorf("name must start with a letter or digit, got %q", string(first))
	}
	if !isNameAlnum(last) {
		return fmt.Errorf("name must end with a letter or digit, got %q", string(last))
	}

	if strings.Contains(name, "--") {
		return errors.New(`name must not contain consecutive hyphens ("--")`)
	}
	if strings.Contains(name, "..") {
		return errors.New(`name must not contain consecutive periods ("..")`)
	}
	return nil
}

func isNameAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
