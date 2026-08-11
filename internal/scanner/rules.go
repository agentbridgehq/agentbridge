package scanner

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The rule catalogue.
//
// Severity is assigned by one question only: **how hard is this pattern to
// reach innocently?** Not how bad it would be if malicious — almost everything
// here would be bad if malicious, and grading on that produces a scanner where
// everything is High and nothing is read.
//
// So instruction-override phrasing is High because there is almost no honest
// reason to tell an agent to disregard what it was told before. A reference to
// a credential path is Medium because a plugin about AWS may legitimately
// mention one. Bidirectional control characters are High because they exist
// only to make text render differently from how it parses.

// Rule documents one detection.
type Rule struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Severity Severity `json:"severity"`
	// Rationale explains why the pattern is suspicious, so a reader can judge
	// the finding rather than trust it.
	Rationale string `json:"rationale"`
	// Remedy says what to do. Every rule has one; a finding a reader cannot act
	// on is an interruption.
	Remedy string `json:"remedy"`
}

// Stable rule identifiers. These appear in SARIF output and in policy, so
// renaming one is a breaking change.
const (
	RuleInstructionOverride = "skill.instruction_override"
	RuleConcealFromUser     = "skill.conceal_from_user"
	RuleBypassConfirmation  = "skill.bypass_confirmation"
	RuleCredentialAccess    = "skill.credential_access"
	RuleExfiltration        = "skill.exfiltration"
	RuleDestructiveAction   = "skill.destructive_action"
	RuleBidiControl         = "text.bidi_control"
	RuleZeroWidth           = "text.zero_width"
	RuleHomoglyph           = "text.homoglyph"
	RuleHiddenComment       = "text.hidden_comment"
	RuleEncodedBlob         = "text.encoded_blob"
	RuleScriptDestructive   = "script.destructive_command"
	RuleScriptRemoteExec    = "script.remote_execution"
	RuleServerSecretLiteral = "server.credential_literal"
	RuleServerRemoteEgress  = "server.remote_endpoint"
)

var catalog = map[string]Rule{
	RuleInstructionOverride: {
		ID: RuleInstructionOverride, Title: "instruction override", Severity: High,
		Rationale: "The text tells the agent to disregard, override or replace instructions it was given elsewhere. " +
			"There is almost no honest reason for a plugin to say this: a skill describes a capability, it does not " +
			"renegotiate the agent's standing instructions.",
		Remedy: "Read the surrounding text. If the plugin is not about prompt injection itself, treat this as hostile.",
	},
	RuleConcealFromUser: {
		ID: RuleConcealFromUser, Title: "instruction to conceal activity", Severity: High,
		Rationale: "The text directs the agent to hide something from the person using it. Concealment has no " +
			"legitimate role in a capability description, and it is what turns a data-access instruction into an " +
			"exfiltration one.",
		Remedy: "Treat as hostile unless the plugin is demonstrably about output formatting rather than disclosure.",
	},
	RuleBypassConfirmation: {
		ID: RuleBypassConfirmation, Title: "instruction to act without confirmation", Severity: Medium,
		Rationale: "The text tells the agent to proceed without asking. Sometimes reasonable for a read-only " +
			"operation, and much less so when it appears near a destructive or credential-touching one.",
		Remedy: "Check what the agent is being told to do unprompted, and whether it is reversible.",
	},
	RuleCredentialAccess: {
		ID: RuleCredentialAccess, Title: "reference to a credential location", Severity: Medium,
		Rationale: "The instruction text names a file or variable that conventionally holds credentials. A plugin " +
			"about cloud tooling may mention one legitimately, so this is evidence rather than a verdict.",
		Remedy: "Check what the agent is told to do with it. Reading a credential to use it is normal; reading one to " +
			"include it in a message is not.",
	},
	RuleExfiltration: {
		ID: RuleExfiltration, Title: "instruction to send data outward", Severity: High,
		Rationale: "The text directs the agent to transmit content to an address, encode it, or attach it to a " +
			"request. Combined with anything that reads local state, this is the shape of exfiltration.",
		Remedy: "Establish exactly what is sent and where. Treat as hostile if the destination is not the plugin's own service.",
	},
	RuleDestructiveAction: {
		ID: RuleDestructiveAction, Title: "instruction describing a destructive action", Severity: Medium,
		Rationale: "The text tells the agent to delete, drop, reset or force-overwrite something. Legitimate for a " +
			"cleanup or migration plugin, and serious when paired with an instruction not to ask first.",
		Remedy: "Check whether the action is reversible and whether the agent is told to confirm.",
	},
	RuleBidiControl: {
		ID: RuleBidiControl, Title: "bidirectional control characters", Severity: High,
		Rationale: "Unicode bidirectional overrides make text render differently from how it is parsed — the " +
			"\"Trojan Source\" technique. A reviewer sees one instruction and the model receives another. There is no " +
			"legitimate use for these in a skill written in a left-to-right script.",
		Remedy: "Remove them. If the skill is genuinely written in a right-to-left language, use the plain characters " +
			"and no explicit overrides.",
	},
	RuleZeroWidth: {
		ID: RuleZeroWidth, Title: "zero-width characters in instruction text", Severity: Medium,
		Rationale: "Zero-width characters are invisible to a reader and visible to a model. They can hide text " +
			"outright or break up words that would otherwise be recognisable, and they arrive by accident often " +
			"enough — copy-paste from a rendered page — to be worth checking rather than assuming.",
		Remedy: "Strip them and see whether the text still says what it appeared to say.",
	},
	RuleHomoglyph: {
		ID: RuleHomoglyph, Title: "mixed scripts within a word", Severity: Medium,
		Rationale: "A word combining Latin with Cyrillic or Greek letters that look identical. This defeats reading " +
			"and any keyword matching, and is essentially never accidental within a single word.",
		Remedy: "Normalise the text to one script and re-read it.",
	},
	RuleHiddenComment: {
		ID: RuleHiddenComment, Title: "instructions inside an HTML comment", Severity: High,
		Rationale: "An HTML comment is invisible in rendered Markdown and fully visible to a model reading the file. " +
			"Instruction-shaped text placed there is hidden from exactly the person reviewing the plugin.",
		Remedy: "Read the comment. Content meant for a person belongs in the visible text; content meant only for the " +
			"model, hidden from the reader, is the problem.",
	},
	RuleEncodedBlob: {
		ID: RuleEncodedBlob, Title: "long encoded string", Severity: Low,
		Rationale: "A long base64-like run in instruction text. Usually an embedded asset, occasionally an instruction " +
			"the author did not want read.",
		Remedy: "Decode it and check.",
	},
	RuleScriptDestructive: {
		ID: RuleScriptDestructive, Title: "destructive command in a bundled script", Severity: Medium,
		Rationale: "A script shipped with the skill contains a recursive delete, a force push, or a destructive " +
			"database statement. These scripts run with the user's own access.",
		Remedy: "Read the script. There is no sandbox: the specification defines none.",
	},
	RuleScriptRemoteExec: {
		ID: RuleScriptRemoteExec, Title: "script downloads and executes code", Severity: High,
		Rationale: "A bundled script pipes a download straight into a shell or interpreter. Whatever the plugin was " +
			"reviewed as, what actually runs is whatever that URL serves today.",
		Remedy: "Treat the plugin as unreviewable until this is removed: its behaviour is not determined by its contents.",
	},
	RuleServerSecretLiteral: {
		ID: RuleServerSecretLiteral, Title: "credential in server configuration", Severity: High,
		Rationale: "An MCP server's environment holds what looks like a live credential. §9.2 states that env values " +
			"are visible package data and that plugins MUST NOT embed secrets in them; anyone with the plugin has it.",
		Remedy: "Assume the credential is compromised and rotate it. Use a secret reference instead.",
	},
	RuleServerRemoteEgress: {
		ID: RuleServerRemoteEgress, Title: "server sends data to a remote endpoint", Severity: Info,
		Rationale: "The plugin connects to a remote MCP server, so anything the agent passes to its tools leaves the " +
			"machine. Not a defect — just the fact most worth knowing before installing.",
		Remedy: "Confirm the endpoint is the plugin's own service and that you accept data reaching it.",
	},
}

// Catalog returns every rule, ordered by identifier.
func Catalog() []Rule {
	out := make([]Rule, 0, len(catalog))
	for _, r := range catalog {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Lookup returns a rule's documentation.
func Lookup(id string) (Rule, bool) {
	r, ok := catalog[id]
	return r, ok
}

// ---------------------------------------------------------------- patterns

// Instruction-shaped patterns. Written against lowercased text and kept
// deliberately specific: a loose pattern here costs far more than a missed
// detection, because it is the one that gets the whole scanner muted.
var (
	instructionOverride = regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override|bypass)\b[^.\n]{0,40}\b(previous|prior|earlier|above|preceding|all)\b[^.\n]{0,30}\b(instruction|prompt|rule|direction|guideline|constraint)s?\b` +
		`|(?i)\byou are (now|actually)\b` +
		`|(?i)\b(regardless|irrespective) of\b[^.\n]{0,40}\b(instruction|rule|policy|guideline)s?\b` +
		`|(?i)\bnew (system )?(prompt|instructions?)\b\s*[:：]`)

	concealFromUser = regexp.MustCompile(`(?i)\b(do not|don't|never)\b[^.\n]{0,30}\b(tell|inform|show|mention|reveal|display|report|disclose|notify)\b[^.\n]{0,25}\b(the )?(user|human|person|operator)\b` +
		`|(?i)\bwithout\b[^.\n]{0,20}\b(the )?(user|human)('s)?\b[^.\n]{0,20}\b(knowledge|awareness|noticing)\b` +
		`|(?i)\b(silently|secretly|covertly)\b[^.\n]{0,25}\b(send|include|append|attach|read|collect|forward)\b`)

	bypassConfirmation = regexp.MustCompile(`(?i)\bwithout\b[^.\n]{0,20}\b(asking|prompting|confirmation|approval|permission|checking)\b` +
		`|(?i)\b(do not|don't|never)\b[^.\n]{0,20}\b(ask|prompt|confirm|request permission)\b` +
		`|(?i)\bauto(matically)?[- ]approve\b`)

	exfiltration = regexp.MustCompile(`(?i)\b(send|post|upload|transmit|forward|exfiltrate|report)\b[^.\n]{0,50}\b(to|at)\b\s+https?://` +
		`|(?i)\binclude\b[^.\n]{0,40}\b(in|with)\b[^.\n]{0,20}\b(the )?(request|url|query|header|payload|body)\b` +
		`|(?i)\bbase64\b[^.\n]{0,30}\b(then|and)\b[^.\n]{0,25}\b(send|post|upload|append|include)\b`)

	destructiveText = regexp.MustCompile(`(?i)\b(delete|remove|drop|purge|wipe|destroy)\b[^.\n]{0,25}\b(all|every|entire|whole)\b` +
		`|(?i)\brm\s+-[rf]{1,2}\b` +
		`|(?i)\bdrop\s+(table|database|schema)\b` +
		`|(?i)\bgit\s+push\b[^.\n]{0,20}--force\b`)

	// Credential locations. Literal, low-ambiguity strings only: anything
	// needing judgement belongs to a human, not a regex.
	credentialPaths = []string{
		"~/.aws/credentials", ".aws/credentials", "~/.ssh/id_rsa", ".ssh/id_rsa",
		"~/.ssh/id_ed25519", "~/.netrc", "~/.npmrc", "~/.docker/config.json",
		"~/.kube/config", "~/.config/gcloud", "id_rsa", ".env.local", ".env.production",
		"~/.gitconfig", "~/.pypirc",
	}

	// Scripts.
	scriptRemoteExec = regexp.MustCompile(`(?i)(curl|wget)\b[^|\n]{0,120}\|\s*(sudo\s+)?(ba|z|k|)sh\b` +
		`|(?i)(curl|wget)\b[^|\n]{0,120}\|\s*(python|node|ruby|perl)\b` +
		`|(?i)\beval\b[^\n]{0,40}\$\((curl|wget)\b`)

	scriptDestructive = regexp.MustCompile(`(?i)\brm\s+-[rf]{1,2}[a-z]*\s+[/~$]` +
		`|(?i)\bdrop\s+(table|database)\b` +
		`|(?i)\bgit\s+push\b[^\n]{0,30}--force\b` +
		`|(?i)\bmkfs\b|\bdd\s+if=/dev/(zero|random)\b`)

	// A long unbroken base64-ish run. The threshold is high enough that
	// ordinary hashes and identifiers do not reach it.
	encodedBlob = regexp.MustCompile(`[A-Za-z0-9+/]{160,}={0,2}`)

	htmlComment = regexp.MustCompile(`(?s)<!--(.*?)-->`)
)

// scanInstructions applies the text rules to content a model will read.
func scanInstructions(file, content string) []Finding {
	var out []Finding

	for i, line := range strings.Split(content, "\n") {
		lineNo := i + 1

		if m := instructionOverride.FindString(line); m != "" {
			out = append(out, finding(RuleInstructionOverride, file, lineNo, line,
				because("the text directs the agent to disregard instructions it was given elsewhere", m)))
		}
		if m := concealFromUser.FindString(line); m != "" {
			out = append(out, finding(RuleConcealFromUser, file, lineNo, line,
				because("the text directs the agent to hide something from the person using it", m)))
		}
		if m := bypassConfirmation.FindString(line); m != "" {
			out = append(out, finding(RuleBypassConfirmation, file, lineNo, line,
				because("the text directs the agent to act without asking first", m)))
		}
		if m := exfiltration.FindString(line); m != "" {
			out = append(out, finding(RuleExfiltration, file, lineNo, line,
				because("the text directs the agent to send content outward", m)))
		}
		if m := destructiveText.FindString(line); m != "" {
			out = append(out, finding(RuleDestructiveAction, file, lineNo, line,
				because("the text describes a destructive action", m)))
		}
		if p := matchCredentialPath(line); p != "" {
			out = append(out, finding(RuleCredentialAccess, file, lineNo, line,
				fmt.Sprintf("the text references %s", p)))
		}
		if m := encodedBlob.FindString(line); m != "" {
			out = append(out, finding(RuleEncodedBlob, file, lineNo, m,
				fmt.Sprintf("a %d-character encoded run", len(m))))
		}

		out = append(out, scanUnicode(file, lineNo, line)...)
	}

	// Comments are matched across the whole file, since they span lines.
	for _, m := range htmlComment.FindAllStringSubmatchIndex(content, -1) {
		body := content[m[2]:m[3]]
		if !looksLikeInstruction(body) {
			continue
		}
		out = append(out, finding(RuleHiddenComment, file, lineOf(content, m[0]), body,
			"instruction-shaped text inside an HTML comment, which renders invisibly"))
	}

	return out
}

// scanScript applies the rules for code the agent may run.
func scanScript(file, content string) []Finding {
	var out []Finding
	for i, line := range strings.Split(content, "\n") {
		if m := scriptRemoteExec.FindString(line); m != "" {
			out = append(out, finding(RuleScriptRemoteExec, file, i+1, line,
				because("the script pipes a download into an interpreter", m)))
		}
		if m := scriptDestructive.FindString(line); m != "" {
			out = append(out, finding(RuleScriptDestructive, file, i+1, line,
				because("the script contains a destructive command", m)))
		}
	}
	return out
}

// looksLikeInstruction filters hidden-comment matches down to text that
// addresses the model. Ordinary comments — editor directives, licence headers,
// notes to maintainers — are not interesting and would swamp the signal.
func looksLikeInstruction(body string) bool {
	lower := strings.ToLower(body)
	for _, cue := range []string{
		"you must", "you should", "always ", "never ", "ignore", "instruction",
		"assistant", "agent", "before answering", "before responding", "system prompt",
		"do not tell", "your task",
	} {
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}

func matchCredentialPath(line string) string {
	lower := strings.ToLower(line)
	// Longest first, so "~/.aws/credentials" is reported rather than the
	// substring ".aws/credentials" it contains.
	paths := append([]string(nil), credentialPaths...)
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, p := range paths {
		if strings.Contains(lower, strings.ToLower(p)) {
			return p
		}
	}
	return ""
}

// because states the rule and the fragment that triggered it.
//
// The excerpt shown to a reader is always the whole source line, never the
// regex match: reporting the match alone turns `rm -rf /tmp/build-cache` into
// `rm -rf /`, which is a materially more alarming claim than the file makes. A
// scanner that overstates its own findings does not get a second reading, so
// the match belongs in the explanation and the line belongs in the evidence.
func because(reason, match string) string {
	match = sanitize(match)
	if match == "" {
		return reason
	}
	return fmt.Sprintf("%s (matched %q)", reason, match)
}

func lineOf(content string, offset int) int {
	return strings.Count(content[:offset], "\n") + 1
}

func finding(ruleID, file string, line int, excerpt, message string) Finding {
	rule := catalog[ruleID]
	return Finding{
		RuleID:   ruleID,
		Severity: rule.Severity,
		Title:    rule.Title,
		Message:  message,
		File:     file,
		Line:     line,
		Excerpt:  sanitize(excerpt),
	}
}

// sanitize makes an excerpt safe to print.
//
// The scanner reports on text chosen by an attacker, and prints it to a
// terminal. Passing control characters through would let a finding rewrite the
// surrounding output — the same class of trick as the bidi rule, turned against
// the tool reporting it.
func sanitize(s string) string {
	s = strings.TrimSpace(s)

	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			b.WriteString(fmt.Sprintf("\\x%02x", r))
		case isInvisible(r):
			b.WriteString(fmt.Sprintf("\\u%04x", r))
		default:
			b.WriteRune(r)
		}
	}

	out := strings.Join(strings.Fields(b.String()), " ")
	const max = 140
	if len([]rune(out)) > max {
		return string([]rune(out)[:max]) + "…"
	}
	return out
}
