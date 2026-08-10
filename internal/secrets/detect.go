package secrets

import (
	"math"
	"regexp"
	"strings"
)

// Detection of credentials in configuration is two questions, and both matter:
// does the *name* suggest a credential, and does the *value* look like one?
//
// Name matching alone misses a token in a variable called API_URL, which is
// exactly the mistake a hurried author makes. Value matching alone flags every
// long random-looking string. Together they are good enough to be worth acting
// on, and the action — refusing to write it, or offering to move it to the
// keychain — is cheap to undo if wrong.

// nameSuggestsSecret matches variable names that conventionally hold
// credentials.
var nameSuggestsSecret = regexp.MustCompile(`(?i)(^|_)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|APIKEY|API_KEY|ACCESS_KEY|SECRET_KEY|PRIVATE_KEY|CLIENT_SECRET|AUTH|SESSION|COOKIE|PAT)($|_)`)

// knownTokenShapes are prefixes issuers use to make their credentials
// identifiable. Matching these is high-confidence: nothing else looks like an
// OpenAI or GitHub token by accident.
var knownTokenShapes = []struct {
	prefix string
	issuer string
}{
	{"sk-", "OpenAI-style API key"},
	{"sk_live_", "Stripe live key"},
	{"sk_test_", "Stripe test key"},
	{"rk_live_", "Stripe restricted key"},
	{"ghp_", "GitHub personal access token"},
	{"gho_", "GitHub OAuth token"},
	{"ghs_", "GitHub server token"},
	{"github_pat_", "GitHub fine-grained token"},
	{"xoxb-", "Slack bot token"},
	{"xoxp-", "Slack user token"},
	{"AKIA", "AWS access key id"},
	{"ASIA", "AWS temporary access key id"},
	{"AIza", "Google API key"},
	{"ya29.", "Google OAuth token"},
	{"glpat-", "GitLab personal access token"},
	{"npm_", "npm access token"},
	{"dop_v1_", "DigitalOcean token"},
	{"hf_", "Hugging Face token"},
	{"eyJ", "JSON Web Token"},
	{"-----BEGIN", "PEM-encoded private key"},
}

// Confidence grades a detection.
type Confidence string

const (
	// High means the value itself identifies as a credential.
	High Confidence = "high"
	// Medium means the name says credential and the value is plausible.
	Medium Confidence = "medium"
	// Low means the name says credential but the value does not look like one.
	Low Confidence = "low"
)

// Finding is one suspected credential.
type Finding struct {
	// Key is the environment variable or header name.
	Key        string
	Confidence Confidence
	// Reason explains the match in terms a person can check.
	Reason string
	// Suggested is a secret name to migrate this value to.
	Suggested string
}

// Detect examines one name/value pair.
//
// Values that are placeholders, references, or obviously not secrets are never
// reported: a false positive that blocks an install is far more annoying than
// one that merely warns, and this result is used to block.
func Detect(key, value string) (Finding, bool) {
	if value == "" || IsRef(value) {
		return Finding{}, false
	}
	// Specification placeholders and any other unexpanded template are not
	// credentials.
	if strings.Contains(value, "${") {
		return Finding{}, false
	}

	for _, shape := range knownTokenShapes {
		if strings.HasPrefix(value, shape.prefix) {
			return Finding{
				Key:        key,
				Confidence: High,
				Reason:     "value looks like a " + shape.issuer,
				Suggested:  suggestName(key),
			}, true
		}
	}

	if nameSuggestsSecret.MatchString(key) {
		conf := Low
		reason := "name suggests a credential"
		if looksRandom(value) {
			conf = Medium
			reason = "name suggests a credential and the value looks like one"
		}
		return Finding{Key: key, Confidence: conf, Reason: reason, Suggested: suggestName(key)}, true
	}

	return Finding{}, false
}

// DetectAll examines a map, returning findings in a stable order.
func DetectAll(values map[string]string) []Finding {
	var out []Finding
	for _, k := range sortedKeys(values) {
		if f, ok := Detect(k, values[k]); ok {
			out = append(out, f)
		}
	}
	return out
}

// looksRandom estimates whether a value carries enough entropy to be a
// generated credential rather than a setting.
//
// The thresholds are deliberately loose. This only ever raises confidence from
// low to medium, so being wrong changes the wording of a warning rather than
// whether one appears.
func looksRandom(value string) bool {
	if len(value) < 12 {
		return false
	}
	// A value with spaces or a scheme is a sentence or a URL, not a token.
	if strings.ContainsAny(value, " \t") || strings.Contains(value, "://") {
		return false
	}
	return shannonEntropy(value) > 3.0
}

func shannonEntropy(s string) float64 {
	counts := map[rune]float64{}
	for _, r := range s {
		counts[r]++
	}
	total := float64(len([]rune(s)))
	var e float64
	for _, c := range counts {
		p := c / total
		e -= p * math.Log2(p)
	}
	return e
}

// suggestName proposes a secret name derived from a variable name.
func suggestName(key string) string {
	lower := strings.ToLower(key)
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, lower)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Sorting keeps plans and reports deterministic.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Mask renders a secret value for display, showing enough to recognize it and
// not enough to use it.
func Mask(value string) string {
	runes := []rune(value)
	switch {
	case len(runes) == 0:
		return ""
	case len(runes) <= 8:
		return strings.Repeat("•", len(runes))
	default:
		return string(runes[:4]) + strings.Repeat("•", 8) + string(runes[len(runes)-2:])
	}
}
