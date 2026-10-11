// Package propose turns a local skill into a pull request on the registry,
// the only way to change a team skill.
package propose

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/skill"
)

var (
	// ErrNoChanges reports a proposal identical to the registry version.
	ErrNoChanges = errors.New("the proposed skill does not differ from the registry")
	// ErrNoPullRequest reports that the branch was pushed but no pull request
	// could be opened; the developer has to open it manually.
	ErrNoPullRequest = errors.New("branch pushed but no pull request opened")
)

// Request describes a proposal.
type Request struct {
	Base     string // registry branch the PR targets, e.g. "main"
	Branch   string // branch to create and push, e.g. "skillcrew/review-20261011-120000"
	SkillDir string // skill directory inside the registry, slash-separated, e.g. "plugins/security/skills/review"
	Name     string // skill name in the registry; the proposed SKILL.md frontmatter name is reset to it
	Source   string // local directory holding the proposed skill files
	Title    string // commit message and PR title
	Body     string // PR body
}

// Result of a proposal.
type Result struct {
	Branch string
	URL    string // pull request URL, or empty if no PR could be opened
}

// Opener opens a pull request for a pushed branch and returns its URL.
type Opener interface {
	Open(ctx context.Context, remoteURL, base, branch, title, body string) (string, error)
}

// Propose validates the skill in req.Source, commits it in place of
// req.SkillDir on a new branch based on origin's req.Base, pushes that branch
// (never forcing, never pushing anything else) and opens a pull request.
//
// It returns ErrNoChanges when the skill matches the registry. When the push
// succeeds but opener fails (or is nil), it returns the Result with Branch set
// and an error wrapping both ErrNoPullRequest and the opener error, so the
// caller can tell the developer to open the pull request manually.
func Propose(ctx context.Context, repo *gitx.Repo, req Request, opener Opener) (Result, error) {
	if err := checkSkillDir(req.SkillDir); err != nil {
		return Result{}, err
	}
	if err := skill.ValidateName(req.Name); err != nil {
		return Result{}, err
	}
	tmp, err := os.MkdirTemp("", "skillcrew-propose-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)

	prepared := filepath.Join(tmp, "skill", req.Name)
	if err := skill.CopyAs(req.Source, prepared); err != nil {
		return Result{}, err
	}

	worktree := filepath.Join(tmp, "worktree")
	base := "refs/remotes/origin/" + req.Base
	if err := repo.NeutralizeAttributes(); err != nil {
		return Result{}, err
	}
	if _, err := repo.Git(ctx, safe("worktree", "add", "--quiet", "--detach", worktree, base)...); err != nil {
		return Result{}, err
	}
	defer func() {
		// Clean up even when ctx is canceled.
		cleanup := context.WithoutCancel(ctx)
		_, _ = repo.Git(cleanup, "worktree", "remove", "--force", worktree)
		_, _ = repo.Git(cleanup, "worktree", "prune")
	}()

	if err := checkTree(ctx, repo, worktree, base, req.SkillDir); err != nil {
		return Result{}, err
	}
	if err := commit(ctx, repo, worktree, prepared, req); err != nil {
		return Result{}, err
	}
	if _, err := repo.Git(ctx, safe("-C", worktree, "push", "--quiet", "origin", "HEAD:refs/heads/"+req.Branch)...); err != nil {
		return Result{}, err
	}

	res := Result{Branch: req.Branch}
	if opener == nil {
		return res, ErrNoPullRequest
	}
	remote, err := repo.RemoteURL(ctx)
	if err == nil {
		res.URL, err = opener.Open(ctx, remote, req.Base, req.Branch, req.Title, req.Body)
	}
	if err != nil {
		return Result{Branch: req.Branch}, fmt.Errorf("%w: %w", ErrNoPullRequest, err)
	}
	return res, nil
}

// checkSkillDir rejects directories that would escape or replace the whole registry.
func checkSkillDir(dir string) error {
	if dir == "" || path.IsAbs(dir) || path.Clean(dir) != dir || dir == "." || dir == ".." || strings.HasPrefix(dir, "../") {
		return fmt.Errorf("invalid skill directory %q", dir)
	}
	return nil
}

// safeConfig keeps a registry checkout from creating symlinks (which could
// point outside the worktree) or fetching LFS objects from arbitrary hosts.
var safeConfig = []string{
	"-c", "core.symlinks=false",
	"-c", "filter.lfs.smudge=",
	"-c", "filter.lfs.process=",
	"-c", "filter.lfs.required=false",
}

// safe prefixes git args with safeConfig.
func safe(args ...string) []string {
	return append(append([]string{}, safeConfig...), args...)
}

// checkTree refuses a skill directory whose existing components are not
// directories, both in the worktree and in the base tree: a registry commit
// could otherwise turn one into a symlink and make Propose delete or write
// files outside the registry. Missing components (a new skill or plugin) are
// fine: Propose creates them.
func checkTree(ctx context.Context, repo *gitx.Repo, worktree, base, skillDir string) error {
	parts := strings.Split(skillDir, "/")
	for i := range parts {
		rel := strings.Join(parts[:i+1], "/")
		info, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("refusing to write the skill: %s is not a directory in the registry", rel)
		}
		out, err := repo.Git(ctx, "ls-tree", base, "--", rel)
		if err != nil {
			return err
		}
		if mode, _, _ := strings.Cut(out, " "); mode != "040000" {
			return fmt.Errorf("refusing to write the skill: %s is not a directory in the registry", rel)
		}
	}
	return nil
}

// commit replaces the skill directory in the worktree and commits it on a new branch.
func commit(ctx context.Context, repo *gitx.Repo, worktree, prepared string, req Request) error {
	git := func(args ...string) (string, error) {
		return repo.Git(ctx, safe(append([]string{"-C", worktree}, args...)...)...)
	}
	if _, err := git("checkout", "-q", "-b", req.Branch); err != nil {
		return err
	}
	target := filepath.Join(worktree, filepath.FromSlash(req.SkillDir))
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := fsx.CopyDir(prepared, target); err != nil {
		return err
	}
	if _, err := git("add", "-A"); err != nil {
		return err
	}
	if status, err := git("status", "--porcelain"); err != nil {
		return err
	} else if status == "" {
		return ErrNoChanges
	}
	_, err := git("commit", "-q", "-m", req.Title)
	return err
}
