package propose

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
)

// CLIOpener opens pull requests with gh (GitHub) or glab (GitLab) when installed.
type CLIOpener struct {
	LookPath func(string) (string, error)
	Run      func(ctx context.Context, name string, args ...string) (string, error)
}

// NewCLIOpener returns a CLIOpener using the tools found in PATH.
func NewCLIOpener() CLIOpener {
	return CLIOpener{LookPath: exec.LookPath, Run: runCommand}
}

// Open opens a pull request (or GitLab merge request) from branch into base.
// Without a suitable tool it returns an error telling where to open it manually.
func (o CLIOpener) Open(ctx context.Context, remoteURL, base, branch, title, body string) (string, error) {
	host, repoPath, err := parseRemote(remoteURL)
	if err != nil {
		return "", fmt.Errorf("%w; open a pull request from branch %s", err, branch)
	}
	switch {
	case strings.Contains(host, "github"):
		if _, err := o.LookPath("gh"); err != nil {
			return "", fmt.Errorf("gh is not installed; open a pull request from branch %s: https://%s/%s/compare/%s...%s?expand=1",
				branch, host, repoPath, base, branch)
		}
		out, err := o.Run(ctx, "gh", "pr", "create", "--repo", host+"/"+repoPath,
			"--base", base, "--head", branch, "--title", title, "--body", body)
		if err != nil {
			return "", err
		}
		return lastLine(out, ""), nil
	case strings.Contains(host, "gitlab"):
		if _, err := o.LookPath("glab"); err != nil {
			return "", fmt.Errorf("glab is not installed; open a merge request from branch %s: https://%s/%s/-/merge_requests/new?merge_request[source_branch]=%s",
				branch, host, repoPath, url.QueryEscape(branch))
		}
		out, err := o.Run(ctx, "glab", "mr", "create", "--repo", host+"/"+repoPath,
			"--source-branch", branch, "--target-branch", base, "--title", title, "--description", body, "--yes")
		if err != nil {
			return "", err
		}
		return lastLine(out, "http"), nil
	}
	return "", fmt.Errorf("no pull request tool for %s; open a pull request from branch %s", host, branch)
}

// lastLine returns the last non-empty line of out containing substr, trimmed.
func lastLine(out, substr string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" && strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}

var scpRemote = regexp.MustCompile(`^[\w.-]+@([\w.-]+):(.+)$`)

// parseRemote splits a Git remote URL into its host and repository path.
func parseRemote(remote string) (host, repoPath string, err error) {
	if m := scpRemote.FindStringSubmatch(remote); m != nil {
		host, repoPath = m[1], m[2]
	} else if u, perr := url.Parse(remote); perr == nil && u.Host != "" {
		host, repoPath = u.Hostname(), u.Path
	}
	repoPath = strings.TrimSuffix(strings.Trim(repoPath, "/"), ".git")
	if host == "" || !strings.Contains(repoPath, "/") {
		return "", "", fmt.Errorf("cannot find the hosting service of %s", gitx.Redact(remote))
	}
	return host, repoPath, nil
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s: %s", name, msg)
	}
	return stdout.String(), nil
}
