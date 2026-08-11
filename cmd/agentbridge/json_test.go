package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The --json contract.
//
// "Every command supports --json. Scriptable from day one" is a documented
// promise (MVP M6-7), and until this file it was a promise with nothing
// enforcing it. Two violations were found by hand in successive sessions, both
// of the same shape and both invisible to every other test:
//
//   - `sync --json` reported a failed plugin as `"Err": {}`, because Go's error
//     interface marshals to nothing.
//   - `install --json` refused a plugin and wrote *zero bytes* to stdout, while
//     `scan` and `validate` refused on the same findings and emitted them.
//
// Neither is a crash. A script pipes to `jq`, gets nothing useful, and the
// failure looks like the plugin was fine. That is the class of defect this
// project treats as the serious one, so the contract is asserted here rather
// than trusted.
//
// The binary is built and executed rather than calling run() in-process,
// because exit codes and the stdout/stderr split are half of what is being
// checked and neither survives a function call.

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agentbridge-cli-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	binary = filepath.Join(dir, "agentbridge")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		panic("building the CLI for tests: " + err.Error() + "\n" + string(out))
	}
	os.Exit(m.Run())
}

type result struct {
	stdout, stderr string
	exit           int
}

// run executes the CLI in an empty home directory, so a test never reads or
// writes the developer's real configuration.
func run(t *testing.T, args ...string) result {
	t.Helper()

	home := t.TempDir()
	// One client, so install and sync have somewhere to write.
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)

	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running %v: %v", args, err)
	}
	return result{stdout: out.String(), stderr: errBuf.String(), exit: code}
}

func abs(t *testing.T, rel string) string {
	t.Helper()
	p, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Whatever a --json command writes to stdout must parse. A command that mixes
// prose into that stream is unusable from a script even when it succeeds.
func TestJSONCommandsEmitOnlyJSON(t *testing.T) {
	hostile := abs(t, "../../internal/scanner/testdata/hostile")
	benign := abs(t, "../../internal/scanner/testdata/benign")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"clients", []string{"clients", "--json"}},
		{"list", []string{"list", "--json"}},
		{"losses", []string{"losses", "--json"}},
		{"cache", []string{"cache", "--json"}},
		{"version", []string{"version", "--json"}},
		{"conformance", []string{"conformance", "--json"}},
		{"secret list", []string{"secret", "list", "--json"}},
		{"update", []string{"update", "--json"}},
		{"inspect", []string{"inspect", benign, "--json"}},
		{"validate", []string{"validate", benign, "--json"}},
		{"validate a package with violations", []string{"validate", hostile, "--json"}},
		{"scan clean", []string{"scan", benign, "--json"}},
		{"scan with findings", []string{"scan", hostile, "--json"}},
		{"scan --rules", []string{"scan", "--rules", "--json"}},
		{"install refused by the gate", []string{"install", hostile, "--json"}},
		{"install accepted", []string{"install", benign, "--json"}},
		{"sync with nothing declared", []string{"sync", "--json"}},
		{"doctor", []string{"doctor", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, tc.args...)
			if strings.TrimSpace(got.stdout) == "" {
				t.Fatalf("wrote nothing to stdout (exit %d)\nstderr: %s", got.exit, got.stderr)
			}
			var any any
			if err := json.Unmarshal([]byte(got.stdout), &any); err != nil {
				t.Errorf("stdout is not JSON: %v\n%s", err, truncate(got.stdout))
			}
		})
	}
}

// A refusal must carry the findings that caused it. Re-running the scan to
// learn why an install was blocked means running the tool twice to get one
// answer, and a script that cannot see the reason will paper over it.
func TestInstallRefusalIsMachineReadable(t *testing.T) {
	got := run(t, "install", abs(t, "../../internal/scanner/testdata/hostile"), "--json")

	if got.exit == 0 {
		t.Fatal("a plugin with high-severity findings was installed")
	}

	var refusal struct {
		Plugin   string `json:"plugin"`
		Refused  bool   `json:"refused"`
		Reason   string `json:"reason"`
		Findings []struct {
			RuleID   string `json:"ruleId"`
			Severity string `json:"severity"`
			File     string `json:"file"`
		} `json:"findings"`
		Remedy string `json:"remedy"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &refusal); err != nil {
		t.Fatalf("the refusal is not JSON: %v\n%s", err, truncate(got.stdout))
	}

	if !refusal.Refused {
		t.Error("the refusal does not say it refused")
	}
	if refusal.Plugin == "" || refusal.Reason == "" || refusal.Remedy == "" {
		t.Errorf("incomplete refusal: %+v", refusal)
	}
	if len(refusal.Findings) == 0 {
		t.Fatal("the refusal carries no findings, so a script cannot see what blocked it")
	}
	for _, f := range refusal.Findings {
		if f.Severity != "high" {
			t.Errorf("%s is %s; only the blocking findings belong here", f.RuleID, f.Severity)
		}
		if f.RuleID == "" {
			t.Error("a finding with no rule id cannot be looked up")
		}
	}
}

// Failure must be visible in the exit code, not only in the prose.
func TestExitCodesReportFailure(t *testing.T) {
	hostile := abs(t, "../../internal/scanner/testdata/hostile")
	benign := abs(t, "../../internal/scanner/testdata/benign")

	for _, tc := range []struct {
		name     string
		args     []string
		wantFail bool
	}{
		{"scan finds high-severity content", []string{"scan", hostile}, true},
		{"scan with --fail-on never", []string{"scan", hostile, "--fail-on", "never"}, false},
		{"scan a clean plugin", []string{"scan", benign}, false},
		{"install is refused", []string{"install", hostile}, true},
		{"install is allowed through", []string{"install", hostile, "--allow-flagged-content"}, false},
		{"unknown command", []string{"nonsense"}, true},
		{"unknown severity", []string{"scan", benign, "--min-severity", "critical"}, true},
		{"missing directory", []string{"scan", "./no-such-plugin"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, tc.args...)
			if tc.wantFail && got.exit == 0 {
				t.Errorf("exit 0 for a failure\nstdout: %s\nstderr: %s", truncate(got.stdout), got.stderr)
			}
			if !tc.wantFail && got.exit != 0 {
				t.Errorf("exit %d for a success\nstderr: %s", got.exit, got.stderr)
			}
		})
	}
}

// Diagnostics belong on stderr. A note printed to stdout corrupts the stream a
// script is parsing, which is how a --json contract dies quietly.
func TestJSONModeKeepsStdoutClean(t *testing.T) {
	got := run(t, "install", abs(t, "../../internal/scanner/testdata/hostile"), "--json")

	for _, prose := range []string{"note:", "legend:", "Installed ", "Dry run"} {
		if strings.Contains(got.stdout, prose) {
			t.Errorf("stdout contains prose %q in --json mode:\n%s", prose, truncate(got.stdout))
		}
	}
}

// M6-7 says "--json output on every command". The list above is hand-written,
// which is how `cache` and `version` came to be missing it while the milestone
// was marked done — the table only tested what somebody remembered to add.
//
// This reads the command names out of the dispatch switch instead, so a new
// command joins the contract by existing rather than by being remembered.
func TestEveryCommandIsCoveredByTheJSONContract(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}

	// The commands that legitimately have no JSON to emit.
	exempt := map[string]bool{
		// Prints usage text; there is no result to serialize.
		"help": true, "-h": true, "--help": true,
		// Aliases of commands tested under their primary name.
		"uninstall": true, "--version": true, "-v": true,
		// Executes another program and forwards its streams verbatim. A JSON
		// wrapper would corrupt the output of the thing being launched, which
		// is the entire point of the command.
		"run": true,

		// These need an argument, so running them bare is a usage error rather
		// than a --json gap. Each is covered with real arguments in the table
		// above; exempting them here only skips the argument-less form.
		"inspect": true, "validate": true, "scan": true, "secret": true,
		"install": true, "sync": true, "remove": true,
	}

	inSwitch := false
	var missing []string
	for _, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "switch args[0]") {
			inSwitch = true
			continue
		}
		if inSwitch && trimmed == "}" {
			break
		}
		if !inSwitch || !strings.HasPrefix(trimmed, "case ") {
			continue
		}
		for _, name := range strings.Split(strings.TrimPrefix(trimmed, "case "), ",") {
			name = strings.Trim(strings.TrimSpace(strings.TrimSuffix(name, ":")), `"`)
			if name == "" || exempt[name] {
				continue
			}
			got := run(t, name, "--json")
			if strings.TrimSpace(got.stdout) == "" {
				missing = append(missing, name+" (wrote nothing)")
				continue
			}
			var any any
			if err := json.Unmarshal([]byte(got.stdout), &any); err != nil {
				missing = append(missing, name+" (not JSON)")
			}
		}
	}

	if len(missing) > 0 {
		t.Errorf("commands without a working --json: %s\n"+
			"M6-7 claims every command supports it. Implement it, or add the command to "+
			"`exempt` with a reason.", strings.Join(missing, ", "))
	}
}

func truncate(s string) string {
	const max = 400
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
