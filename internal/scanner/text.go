package scanner

import (
	"fmt"
	"strings"
	"unicode"
)

// Unicode analysis.
//
// This is where over-eager matching does the most damage, because the
// characters involved have entirely legitimate uses in scripts other than the
// one this file happens to be written in. A scanner that flags every Persian
// skill for using a zero-width non-joiner is not a security tool, it is a
// nuisance that gets muted — so each check below is narrowed by the context it
// appears in rather than by the character alone.

// bidiControls make text render in a different order from how it parses. This
// is the "Trojan Source" technique: a reviewer reads one instruction and the
// model receives another. Unlike the marks below, these have no use that a
// plain character cannot serve.
var bidiControls = map[rune]string{
	0x202A: "LEFT-TO-RIGHT EMBEDDING",
	0x202B: "RIGHT-TO-LEFT EMBEDDING",
	0x202C: "POP DIRECTIONAL FORMATTING",
	0x202D: "LEFT-TO-RIGHT OVERRIDE",
	0x202E: "RIGHT-TO-LEFT OVERRIDE",
	0x2066: "LEFT-TO-RIGHT ISOLATE",
	0x2067: "RIGHT-TO-LEFT ISOLATE",
	0x2068: "FIRST STRONG ISOLATE",
	0x2069: "POP DIRECTIONAL ISOLATE",
}

// alwaysSuspiciousInvisible are invisible characters with no ordinary role in
// prose. A word joiner or a soft hyphen inside instruction text is either an
// accident of copy-paste or an attempt to break up a recognisable phrase.
var alwaysSuspiciousInvisible = map[rune]string{
	0x200B: "ZERO WIDTH SPACE",
	0x2060: "WORD JOINER",
	0x00AD: "SOFT HYPHEN",
	0x2061: "FUNCTION APPLICATION",
	0x2062: "INVISIBLE TIMES",
	0x2063: "INVISIBLE SEPARATOR",
	0x2064: "INVISIBLE PLUS",
}

// contextualInvisible are invisible characters that are entirely correct in
// some scripts and suspicious in others.
//
//   - ZERO WIDTH NON-JOINER is required to write Persian and Urdu properly.
//   - ZERO WIDTH JOINER is how emoji sequences are composed.
//
// Flagging either unconditionally would fire on ordinary content, so each is
// only reported when the surrounding line gives it no reason to be there.
var contextualInvisible = map[rune]string{
	0x200C: "ZERO WIDTH NON-JOINER",
	0x200D: "ZERO WIDTH JOINER",
}

func isInvisible(r rune) bool {
	if _, ok := alwaysSuspiciousInvisible[r]; ok {
		return true
	}
	if _, ok := contextualInvisible[r]; ok {
		return true
	}
	if _, ok := bidiControls[r]; ok {
		return true
	}
	return r == 0xFEFF
}

// scanUnicode reports concealment techniques in one line.
func scanUnicode(file string, lineNo int, line string) []Finding {
	var out []Finding

	for _, r := range line {
		if name, ok := bidiControls[r]; ok {
			out = append(out, finding(RuleBidiControl, file, lineNo, line,
				fmt.Sprintf("contains %s (U+%04X), which makes the rendered text differ from what is parsed", name, r)))
			break
		}
	}

	if name, r, ok := firstSuspiciousInvisible(line); ok {
		out = append(out, finding(RuleZeroWidth, file, lineNo, line,
			fmt.Sprintf("contains %s (U+%04X), invisible to a reader and visible to a model", name, r)))
	}

	if word, ok := mixedScriptWord(line); ok {
		out = append(out, finding(RuleHomoglyph, file, lineNo, word,
			fmt.Sprintf("the word %q mixes character sets that look alike", word)))
	}

	return out
}

// firstSuspiciousInvisible finds an invisible character the line has no reason
// to contain.
func firstSuspiciousInvisible(line string) (string, rune, bool) {
	hasArabicScript := containsScript(line, unicode.Arabic) || containsScript(line, unicode.Hebrew)
	hasSymbols := containsEmoji(line)

	for _, r := range line {
		if name, ok := alwaysSuspiciousInvisible[r]; ok {
			return name, r, true
		}
		if name, ok := contextualInvisible[r]; ok {
			switch r {
			case 0x200C:
				// Required to write Persian and Urdu correctly.
				if hasArabicScript {
					continue
				}
			case 0x200D:
				// How emoji sequences are joined.
				if hasSymbols {
					continue
				}
			}
			return name, r, true
		}
	}
	return "", 0, false
}

// mixedScriptWord finds a word combining alphabets whose letters look
// identical — Latin with Cyrillic or Greek.
//
// Checked per word rather than per line: a document containing both English and
// Russian paragraphs is ordinary, while a single word drawing from both is
// essentially never accidental.
func mixedScriptWord(line string) (string, bool) {
	for _, word := range strings.FieldsFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(word)) < 3 {
			continue
		}
		var latin, cyrillic, greek bool
		for _, r := range word {
			switch {
			case unicode.Is(unicode.Latin, r):
				latin = true
			case unicode.Is(unicode.Cyrillic, r):
				cyrillic = true
			case unicode.Is(unicode.Greek, r):
				greek = true
			}
		}
		if latin && (cyrillic || greek) {
			return word, true
		}
	}
	return "", false
}

func containsScript(s string, table *unicode.RangeTable) bool {
	for _, r := range s {
		if unicode.Is(table, r) {
			return true
		}
	}
	return false
}

// containsEmoji reports whether a line holds pictographic characters, which is
// what makes a zero-width joiner legitimate.
func containsEmoji(s string) bool {
	for _, r := range s {
		if r >= 0x1F000 && r <= 0x1FAFF {
			return true
		}
		if r >= 0x2600 && r <= 0x27BF {
			return true
		}
	}
	return false
}
