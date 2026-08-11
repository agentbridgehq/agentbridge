package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Git is fetched by invoking the `git` binary rather than through a library.
//
// This is a deliberate reversal of the plan in docs/08-tech-stack.md, and the
// reason is authentication. A pure-Go implementation has to reimplement
// credential helpers, SSH agent forwarding, `insteadOf` rewrites, enterprise
// proxies and SSO device flows — and the first plugin an enterprise developer
// tries to install is the private one in their own organization. Shelling out
// inherits all of that for free, correctly, on day one.
//
// The costs are real and accepted: `git` must be installed, and every argument
// has to be handled as untrusted input. Nothing here goes through a shell, so
// the only injection surface is arguments being read as flags, which is why
// leading dashes are rejected before anything is executed.

// ErrGitMissing is returned when the git binary cannot be found.
var ErrGitMissing = errors.New("git is not installed or not on PATH")

// fullSHA matches a complete 40-character object name.
var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// gitEnv is the environment every git invocation runs with.
func gitEnv() []string {
	return append(os.Environ(),
		// Fail rather than block forever waiting for a password that nobody is
		// there to type. Credential helpers and the SSH agent are unaffected;
		// only interactive prompting is disabled.
		"GIT_TERMINAL_PROMPT=0",
		// Deterministic output regardless of the user's locale.
		"LC_ALL=C",
	)
}

func requireGit() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("%w: fetching a plugin from a repository needs it; "+
			"install git, or use a local directory reference instead", ErrGitMissing)
	}
	return nil
}

// runGit executes git and returns its standard output.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && !knownFlag(a) {
			return "", fmt.Errorf("refusing to pass %q to git: it would be read as a flag", a)
		}
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return string(out), nil
}

// knownFlag allows the specific flags this package passes, so that the
// leading-dash guard cannot be bypassed by a reference that happens to look
// like one of them.
func knownFlag(a string) bool {
	switch a {
	case "-q", "-c", "--depth", "--no-tags", "--detach", "--bare", "--quiet", "--":
		return true
	}
	return strings.HasPrefix(a, "--depth=")
}

// ResolveRevision turns a branch, tag or partial revision into an immutable
// commit, without fetching the repository.
//
// This is the step that makes a reference reproducible. "Install what main
// points at" is a different answer every week; recording the commit is what
// lets a lockfile mean something.
func ResolveRevision(ctx context.Context, url, rev string) (string, error) {
	if err := requireGit(); err != nil {
		return "", err
	}
	if fullSHA.MatchString(rev) {
		// Already immutable. Resolving it remotely would fail on servers that
		// do not advertise unreferenced objects, for no benefit.
		return rev, nil
	}

	args := []string{"ls-remote", "--", url}
	if rev != "" {
		args = append(args, rev)
	} else {
		args = append(args, "HEAD")
	}

	out, err := runGit(ctx, "", args...)
	if err != nil {
		return "", err
	}

	sha, err := pickRevision(out, rev)
	if err != nil {
		return "", fmt.Errorf("%s: %w", url, err)
	}
	return sha, nil
}

// pickRevision selects a commit from ls-remote output.
//
// A tag matches twice: once as refs/tags/<name> and once as
// refs/tags/<name>^{} when it is annotated. The dereferenced form is the
// commit; the other is the tag object, which is not what anyone means by
// "install v1.2.0".
func pickRevision(lsRemote, rev string) (string, error) {
	var (
		head        string
		exactBranch string
		exactTag    string
		peeledTag   string
	)

	for _, line := range strings.Split(lsRemote, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sha, ref := fields[0], fields[1]
		switch {
		case ref == "HEAD":
			head = sha
		case rev != "" && ref == "refs/heads/"+rev:
			exactBranch = sha
		case rev != "" && ref == "refs/tags/"+rev+"^{}":
			peeledTag = sha
		case rev != "" && ref == "refs/tags/"+rev:
			exactTag = sha
		}
	}

	switch {
	case rev == "" && head != "":
		return head, nil
	case peeledTag != "":
		return peeledTag, nil
	case exactBranch != "":
		return exactBranch, nil
	case exactTag != "":
		return exactTag, nil
	case rev == "":
		return "", errors.New("remote advertised no HEAD")
	default:
		return "", fmt.Errorf("no branch or tag named %q", rev)
	}
}

// fetchCommit materializes one commit into dir.
//
// A shallow fetch of the exact commit is tried first, since it is dramatically
// cheaper for the large monorepos plugins increasingly live in. Not every
// server allows fetching an arbitrary object by name, so a full fetch is the
// fallback rather than the default.
func fetchCommit(ctx context.Context, url, sha, dir string) error {
	if err := requireGit(); err != nil {
		return err
	}
	if !fullSHA.MatchString(sha) {
		return fmt.Errorf("refusing to fetch %q: not a complete commit id", sha)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	if _, err := runGit(ctx, dir, "init", "-q"); err != nil {
		return err
	}
	if _, err := runGit(ctx, dir, "remote", "add", "origin", url); err != nil {
		return err
	}

	if _, err := runGit(ctx, dir, "fetch", "--depth", "1", "--no-tags", "origin", sha); err != nil {
		if _, ferr := runGit(ctx, dir, "fetch", "--no-tags", "origin"); ferr != nil {
			// Report the shallow failure: it is the more specific of the two
			// and usually names the real problem, such as authentication.
			return err
		}
	}

	if _, err := runGit(ctx, dir, "checkout", "-q", "--detach", sha); err != nil {
		return err
	}

	// Verify rather than trust. A checkout that silently landed somewhere else
	// would produce a package that does not match the commit we recorded, and
	// the whole point of pinning is that the record is true.
	got, err := runGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(got); got != sha {
		return fmt.Errorf("checkout landed on %s, expected %s", got, sha)
	}
	return nil
}

// packageSubdir returns the plugin directory inside a fetched tree, rejecting
// any path that escapes it.
//
// Shared by both fetchers: the monorepo-of-plugins layout is the same problem
// whether the tree arrived from a git checkout or an unpacked artifact, and the
// fixed component locations in the specification make it otherwise impossible
// to express.
func packageSubdir(tree, subdir string) (string, error) {
	if subdir == "" {
		return tree, nil
	}
	target := filepath.Join(tree, filepath.FromSlash(subdir))

	rel, err := filepath.Rel(tree, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("subdirectory %q escapes the package", subdir)
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("subdirectory %q not found in the package", subdir)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("subdirectory %q is not a directory", subdir)
	}
	return target, nil
}
