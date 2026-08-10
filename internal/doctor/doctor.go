// Package doctor explains why a plugin is not doing what someone expected.
//
// This is the command the product's positioning rests on. The specification
// permits a conformant client to support *neither* skills nor MCP servers
// (§11.1, §11.2), fixed component locations mean a plugin either lands or
// silently does not, and every client spells its configuration differently. The
// predictable result is that "I installed it, why is nothing happening in X?"
// becomes the ecosystem's most common question — and today nobody answers it.
//
// So the checks here are deliberately not a health dashboard. Each one exists
// because it is a real reason a plugin appears installed and does nothing, and
// each carries the specific next action rather than a status colour. A check
// that cannot tell the user what to do next has not earned its place.
package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentbridge/agentbridge/internal/adapter"
	"github.com/agentbridge/agentbridge/internal/adapter/receipt"
	adapterreg "github.com/agentbridge/agentbridge/internal/adapter/registry"
	"github.com/agentbridge/agentbridge/internal/configedit"
	"github.com/agentbridge/agentbridge/internal/secrets"
)

// Status grades a check.
type Status string

const (
	// OK means the check passed.
	OK Status = "ok"
	// Warn means something is degraded but working.
	Warn Status = "warn"
	// Fail means this is a reason the plugin is not working.
	Fail Status = "fail"
	// Info is context rather than a verdict.
	Info Status = "info"
)

// Check is one finding.
type Check struct {
	Status Status `json:"status"`
	// Subject is what was checked: a client id, a plugin name, or "environment".
	Subject string `json:"subject"`
	Title   string `json:"title"`
	Detail  string `json:"detail,omitempty"`
	// Fix is the specific next action. A check that cannot say what to do next
	// tells the user only that they have a problem, which they already knew.
	Fix string `json:"fix,omitempty"`
}

// Report is the full diagnosis.
type Report struct {
	Checks []Check `json:"checks"`
}

func (r *Report) add(status Status, subject, title, detail, fix string) {
	r.Checks = append(r.Checks, Check{
		Status: status, Subject: subject, Title: title, Detail: detail, Fix: fix,
	})
}

// Count returns how many checks have a status.
func (r *Report) Count(s Status) int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == s {
			n++
		}
	}
	return n
}

// Healthy reports whether nothing failed.
func (r *Report) Healthy() bool { return r.Count(Fail) == 0 }

// Options narrow a diagnosis.
type Options struct {
	Env adapter.Env
	// Plugin restricts the report to one plugin. Empty means all of them.
	Plugin string
	// Client restricts the report to one client id.
	Client string
}

// Run diagnoses the machine.
func Run(store *receipt.Store, opts Options) *Report {
	r := &Report{}

	checkEnvironment(r, opts)
	checkClients(r, opts)
	checkInstalls(r, store, opts)

	return r
}

// checkEnvironment covers the things whose absence breaks whole categories of
// operation, rather than one plugin.
func checkEnvironment(r *Report, opts Options) {
	if _, err := exec.LookPath("git"); err != nil {
		r.add(Warn, "environment", "git is not on PATH",
			"plugins can still be installed from local directories",
			"install git to install from a repository")
	}

	if _, err := secrets.OpenKeyring(); err != nil {
		r.add(Warn, "environment", "no OS credential store is available",
			"secret references cannot be resolved from a keychain on this machine",
			"supply secrets through "+secrets.EnvPrefix+"* environment variables instead")
	}
}

// checkClients reports what was detected and, more usefully, what each client
// will refuse to take.
func checkClients(r *Report, opts Options) {
	installations := adapterreg.Detect(opts.Env)
	if len(installations) == 0 {
		r.add(Fail, "clients", "no agent clients detected on this machine",
			"nothing can be installed until a client is present",
			"install an agent client, or check `agentbridge clients --all` for what is looked for")
		return
	}

	for _, inst := range installations {
		if opts.Client != "" && inst.Client.ID != opts.Client {
			continue
		}

		// The single most common cause of "nothing happened": the client
		// simply has nowhere documented to put skills, so they were never
		// installed and the fidelity report said so at the time.
		switch inst.Client.Skills {
		case adapter.SupportNone:
			r.add(Info, inst.Client.ID, "this client has no skills mechanism",
				"only MCP servers are installed into it",
				"")
		case adapter.SupportUndocumented:
			r.add(Info, inst.Client.ID, "skills are not installed into this client",
				"it loads Agent Plugins, but its vendor has not documented where packages go, "+
					"so we will not write to an unverified path",
				"expect skills to be missing here; MCP servers are installed normally")
		}

		if inst.ConfigPath == "" {
			continue
		}
		if !strings.HasSuffix(inst.ConfigPath, ".json") {
			continue
		}
		if _, err := configedit.LoadJSON(inst.ConfigPath); err != nil {
			r.add(Fail, inst.Client.ID, "configuration file cannot be parsed",
				inst.ConfigPath+": "+err.Error(),
				"fix the syntax by hand; agentbridge will not rewrite a file it cannot read")
		}
	}
}

// checkInstalls diagnoses each installed plugin against what is on disk now.
func checkInstalls(r *Report, store *receipt.Store, opts Options) {
	entries := store.All()
	if len(entries) == 0 {
		r.add(Info, "plugins", "no plugins installed by agentbridge", "", "")
		return
	}

	for _, e := range entries {
		if opts.Plugin != "" && e.Plugin != opts.Plugin {
			continue
		}
		if opts.Client != "" && e.Client != opts.Client {
			continue
		}
		subject := e.Plugin + " → " + e.Client
		checkOneInstall(r, subject, e, opts)
	}
}

func checkOneInstall(r *Report, subject string, e receipt.Entry, opts Options) {
	// Drift in the client's own configuration. A client update, another tool,
	// or a hand edit can remove what we wrote, and the plugin then appears
	// installed while doing nothing at all.
	if e.ConfigPath != "" && len(e.ConfigKeys) > 0 {
		checkConfigKeys(r, subject, e)
	}

	// A whole-package install can be deleted out from under the receipt.
	//
	// Deliberately not compared against the recorded tree digest: that digest
	// addresses the *source* package, and an installed copy legitimately
	// differs — the Claude Code adapter writes a manifest and an .mcp.json on
	// top of the copied tree. Comparing them would report every such install as
	// modified, and a check that always fires trains people to ignore it.
	// Detecting real tampering with an installed copy needs a digest taken
	// after the write, which belongs with the scanning work rather than here.
	if e.PackageDir != "" && !dirExists(e.PackageDir) {
		r.add(Fail, subject, "the installed package is gone",
			e.PackageDir+" no longer exists",
			"run `agentbridge sync`, or reinstall the plugin")
		return
	}

	// The command a stdio server launches. "Nothing happened" is very often
	// an executable that was never installed.
	checkCommands(r, subject, e, opts)
}

// checkConfigKeys reports entries the receipt claims but the configuration no
// longer has.
func checkConfigKeys(r *Report, subject string, e receipt.Entry) {
	doc, err := configedit.LoadJSON(e.ConfigPath)
	if err != nil {
		r.add(Fail, subject, "configuration file cannot be parsed",
			e.ConfigPath+": "+err.Error(),
			"fix the syntax by hand")
		return
	}
	if !doc.Existed() {
		r.add(Fail, subject, "the client's configuration file is gone",
			e.ConfigPath+" no longer exists",
			"run `agentbridge sync` to write it again")
		return
	}

	var missing []string
	for _, key := range e.ConfigKeys {
		ok, err := doc.Has(key)
		if err != nil || !ok {
			missing = append(missing, strings.Join(key, "."))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		r.add(Fail, subject, "entries this plugin installed are no longer in the configuration",
			"missing: "+strings.Join(missing, ", "),
			"something removed them after installation; run `agentbridge sync` to restore")
	}
}

// checkCommands verifies that what a server is configured to launch can
// actually be launched.
func checkCommands(r *Report, subject string, e receipt.Entry, opts Options) {
	if e.ConfigPath == "" || !strings.HasSuffix(e.ConfigPath, ".json") {
		return
	}
	doc, err := configedit.LoadJSON(e.ConfigPath)
	if err != nil || !doc.Existed() {
		return
	}

	for _, key := range e.ConfigKeys {
		command, err := doc.StringAt(append(append([]string(nil), key...), "command"))
		if err != nil || command == "" {
			continue
		}
		name := strings.Join(key, ".")

		if filepath.IsAbs(command) {
			if !fileExists(command) {
				r.add(Fail, subject, "the configured command does not exist",
					name+" launches "+command,
					"reinstall the plugin, or check whether the file was removed")
			}
			continue
		}
		if _, err := exec.LookPath(command); err != nil {
			r.add(Fail, subject, "the configured command is not on PATH",
				name+" launches "+command+", which cannot be found",
				"install it, or note that the client may search a different PATH than this shell")
		}
	}
}

// CheckSecretReferences reports secret references a configuration needs but the
// credential store does not have.
//
// Separate from the rest because it needs a store, and because this is the
// failure that looks least like a configuration problem: everything is present
// and correct, and the server dies on start with a message the user never sees.
func CheckSecretReferences(r *Report, store *receipt.Store, secretStore secrets.Store, opts Options) {
	for _, e := range store.All() {
		if opts.Plugin != "" && e.Plugin != opts.Plugin {
			continue
		}
		if e.ConfigPath == "" || !strings.HasSuffix(e.ConfigPath, ".json") {
			continue
		}
		doc, err := configedit.LoadJSON(e.ConfigPath)
		if err != nil || !doc.Existed() {
			continue
		}

		subject := e.Plugin + " → " + e.Client
		for _, key := range e.ConfigKeys {
			for _, name := range referencedSecrets(doc, key) {
				if _, err := secretStore.Get(name); err != nil {
					r.add(Fail, subject, "a referenced secret is not stored",
						strings.Join(key, ".")+" needs "+name,
						"run `agentbridge secret set "+name+"`")
				}
			}
		}
	}
}

// referencedSecrets reads the secret names a launcher invocation carries.
//
// The launcher records them as `--secret ENV=name` in args, which is
// deliberately readable: a person inspecting a client's configuration can see
// which credentials a server will be given without running anything.
func referencedSecrets(doc *configedit.JSONDoc, key []string) []string {
	args, err := doc.StringSliceAt(append(append([]string(nil), key...), "args"))
	if err != nil || len(args) == 0 {
		return nil
	}

	var out []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "--secret" {
			continue
		}
		if _, name, ok := strings.Cut(args[i+1], "="); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
