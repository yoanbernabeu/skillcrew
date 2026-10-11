package gitx_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	tu "github.com/yoanbernabeu/skillcrew/internal/testutil"
)

// origin creates a non-bare repository with one commit and returns its path.
func origin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "v1")
	write(t, dir, "sub/run.sh", "#!/bin/sh")
	if err := os.Chmod(filepath.Join(dir, "sub/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	commit(t, dir, "first")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, msg string) string {
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestCloneFetchResolveExport(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	first := git(t, src, "rev-parse", "HEAD")
	git(t, src, "tag", "v1")

	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.FetchTags(ctx, true, "v1"); err != nil {
		t.Fatal(err)
	}
	if b, err := repo.DefaultBranch(ctx); err != nil || b != "main" {
		t.Fatalf("default branch = %q, %v", b, err)
	}
	if sha, err := repo.Branch(ctx, "main"); err != nil || sha != first {
		t.Fatalf("branch main = %q, %v", sha, err)
	}

	write(t, src, "a.txt", "v2")
	second := commit(t, src, "second")
	if err := repo.Fetch(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if sha, err := repo.Branch(ctx, "main"); err != nil || sha != second {
		t.Errorf("branch main = %q, %v", sha, err)
	}
	if sha, err := repo.Tag(ctx, "v1"); err != nil || sha != first {
		t.Errorf("tag v1 = %q, %v", sha, err)
	}
	if sha, err := repo.Commit(ctx, first); err != nil || sha != first {
		t.Errorf("commit = %q, %v", sha, err)
	}
	for _, bad := range []string{"nope", "main", first[:12], "-x"} {
		if _, err := repo.Commit(ctx, bad); err == nil {
			t.Errorf("Commit(%q) must fail: only full commit ids are accepted", bad)
		}
	}
	if _, err := repo.Branch(ctx, "nope"); err == nil {
		t.Error("unknown branch must fail")
	}

	dest := filepath.Join(t.TempDir(), "export")
	if err := repo.Export(ctx, first, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "a.txt")); string(b) != "v1" {
		t.Fatalf("exported content = %q", b)
	}
	if info, err := os.Stat(filepath.Join(dest, "sub/run.sh")); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("executable bit lost: %v %v", info, err)
	}
}

func TestOpenAndRemoteURL(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	dir := filepath.Join(t.TempDir(), "reg.git")
	if _, err := gitx.Clone(ctx, src, dir); err != nil {
		t.Fatal(err)
	}
	if url, err := gitx.Open(dir).RemoteURL(ctx); err != nil || url != src {
		t.Fatalf("remote url = %q, %v", url, err)
	}
	if _, err := gitx.Open(t.TempDir()).RemoteURL(ctx); err == nil {
		t.Fatal("opening a non-repository must fail on use")
	}
}

func TestCloneErrorIsRedacted(t *testing.T) {
	_, err := gitx.Clone(context.Background(), "https://user:s3cret@127.0.0.1:1/x.git", filepath.Join(t.TempDir(), "x"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error leaks the secret: %v", err)
	}
}

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"fetch https://bob:tok@github.com/a/b failed": "fetch https://***@github.com/a/b failed",
		"git@github.com:a/b.git":                      "git@github.com:a/b.git",
		"http://x@h/p and https://y:z@h/q":            "http://***@h/p and https://***@h/q",
	}
	for in, want := range cases {
		if got := gitx.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	local := t.TempDir()
	cases := map[string]string{
		"github.com/acme/skills":             "https://github.com/acme/skills.git",
		"gitlab.example.com/g/sub/repo":      "https://gitlab.example.com/g/sub/repo.git",
		"https://github.com/acme/skills.git": "https://github.com/acme/skills.git",
		"git@github.com:acme/skills.git":     "git@github.com:acme/skills.git",
		"ssh://git@host/x.git":               "ssh://git@host/x.git",
		local:                                local,
		"./relative":                         "./relative",
		"host:owner/repo.git":                "host:owner/repo.git",
		"file:///srv/skills.git":             "file:///srv/skills.git",
	}
	// A shorthand that happens to exist as a relative directory stays a URL.
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("github.com/acme/skills", 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitx.NormalizeURL("github.com/acme/skills"); got != "https://github.com/acme/skills.git" {
		t.Errorf("shorthand resolved to a local directory: %q", got)
	}
	for in, want := range cases {
		if got := gitx.NormalizeURL(in); got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// A branch named like a pinned commit or tag must never stand in for it.
func TestRefsCannotImpersonatePins(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	first := git(t, src, "rev-parse", "HEAD")
	git(t, src, "tag", "v1")
	write(t, src, "a.txt", "evil")
	commit(t, src, "evil")
	git(t, src, "branch", first)
	git(t, src, "branch", "v1-branch")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.FetchTags(ctx, true, "v1-branch"); err == nil {
		t.Fatal("v1-branch is not a tag")
	}
	if err := repo.FetchTags(ctx, true, "v1"); err != nil {
		t.Fatal(err)
	}
	if sha, err := repo.Commit(ctx, first); err != nil || sha != first {
		t.Fatalf("commit pin resolved to %q (%v), want %s", sha, err, first)
	}
	if sha, err := repo.Tag(ctx, "v1"); err != nil || sha != first {
		t.Fatalf("tag pin resolved to %q (%v)", sha, err)
	}
	if _, err := repo.Tag(ctx, "v1-branch"); err == nil {
		t.Fatal("a branch must not resolve as a tag")
	}
}

func TestExportLimits(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	sha := git(t, src, "rev-parse", "HEAD")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(b int64, f int) { gitx.ExportMaxBytes, gitx.ExportMaxFiles = b, f }(gitx.ExportMaxBytes, gitx.ExportMaxFiles)

	gitx.ExportMaxFiles = 1
	if err := repo.Export(ctx, sha, t.TempDir()); err == nil {
		t.Error("too many files must fail")
	}
	gitx.ExportMaxFiles, gitx.ExportMaxBytes = 100, 3
	if err := repo.Export(ctx, sha, t.TempDir()); err == nil {
		t.Error("too many bytes must fail")
	}
}

func TestFetchHonorsDeadline(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	dir := filepath.Join(t.TempDir(), "reg.git")
	repo, err := gitx.Clone(ctx, src, dir)
	if err != nil {
		t.Fatal(err)
	}
	// An SSH remote whose transport hangs forever.
	script := filepath.Join(t.TempDir(), "hang.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	git(t, dir, "config", "remote.origin.url", "ssh://example.invalid/x.git")

	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := repo.Fetch(ctx, "main"); err == nil {
		t.Fatal("expected a timeout")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("fetch took %s despite the deadline", d)
	}
}

// Neither a tag nor a nested ref name can stand in for a missing exact ref.
func TestExactRefsOnly(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	git(t, src, "tag", "refs/tags/v1")             // creates refs/tags/refs/tags/v1
	git(t, src, "tag", "refs/remotes/origin/gone") // creates refs/tags/refs/remotes/origin/gone
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	_ = repo.FetchTags(ctx, true, "v1")
	if sha, err := repo.Tag(ctx, "v1"); err == nil {
		t.Errorf("Tag(v1) resolved to %s through a nested ref", sha)
	}
	if sha, err := repo.Branch(ctx, "gone"); err == nil {
		t.Errorf("Branch(gone) resolved to %s through a tag", sha)
	}
	if _, err := repo.Branch(ctx, "main"); err != nil {
		t.Errorf("Branch(main): %v", err)
	}
}

func TestCloneLeavesNoLocalBranches(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	dir := filepath.Join(t.TempDir(), "reg.git")
	if _, err := gitx.Clone(ctx, src, dir); err != nil {
		t.Fatal(err)
	}
	if out := git(t, dir, "for-each-ref", "refs/heads"); out != "" {
		t.Fatalf("stale local branches: %s", out)
	}
}

func TestMovedTagDoesNotBreakFetch(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	first := git(t, src, "rev-parse", "HEAD")
	git(t, src, "tag", "v1")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.FetchTags(ctx, true, "v1"); err != nil {
		t.Fatal(err)
	}
	write(t, src, "a.txt", "moved")
	second := commit(t, src, "second")
	git(t, src, "tag", "-f", "v1")
	if err := repo.Fetch(ctx, "main"); err != nil {
		t.Fatalf("branches must still be fetched: %v", err)
	}
	if sha, _ := repo.Branch(ctx, "main"); sha != second {
		t.Fatalf("main = %s, want %s", sha, second)
	}
	if err := repo.FetchTags(ctx, true, "v1"); err == nil {
		t.Error("a moved tag must be reported")
	}
	if sha, _ := repo.Tag(ctx, "v1"); sha != first {
		t.Errorf("a moved tag must keep its first commit, got %s", sha)
	}
}

func TestExportIgnoresArchiveAttributes(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	write(t, src, ".gitattributes", "a.txt export-subst\nsub/run.sh export-ignore\n")
	write(t, src, "a.txt", "$Format:%s$")
	sha := commit(t, src, "inject")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := repo.Export(ctx, sha, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "a.txt")); string(b) != "$Format:%s$" {
		t.Errorf("export-subst applied: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub/run.sh")); err != nil {
		t.Error("export-ignore applied")
	}
}

func TestRepositoryHooksNeverRun(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	// An absolute hooksPath, so the hook would run in any repository.
	hooks := t.TempDir()
	write(t, hooks, "post-checkout", "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(filepath.Join(hooks, "post-checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[core]\n\thooksPath = "+hooks+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := repo.Git(ctx, "worktree", "add", "--detach", wt, "refs/remotes/origin/main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repository hook ran")
	}
}

func TestTree(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	sha := git(t, src, "rev-parse", "HEAD")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := repo.Tree(ctx, sha)
	if err != nil {
		t.Fatal(err)
	}
	sub, ok := entries["sub"]
	if !ok || !sub.Dir || sub.OID != git(t, src, "rev-parse", "HEAD:sub") {
		t.Fatalf("sub entry: %+v", sub)
	}
	a := entries["a.txt"]
	b, err := repo.Blob(ctx, a.OID)
	if err != nil || string(b) != "v1" {
		t.Fatalf("blob: %q %v", b, err)
	}
	dest := t.TempDir()
	if err := repo.Export(ctx, sub.OID, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "run.sh")); err != nil {
		t.Fatal("exporting a tree id must export its content at the root")
	}
}

// Only the followed branch is fetched: a case-variant branch (e.g. Main,
// which collides with main on case-insensitive filesystems) never is.
func TestOnlyTheFollowedBranchIsFetched(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	main := git(t, src, "rev-parse", "HEAD")
	write(t, src, "a.txt", "attacker")
	evil := commit(t, src, "evil")
	git(t, src, "reset", "-q", "--hard", main)
	git(t, src, "pack-refs", "--all")
	// Packed refs can hold both Main and main, as a case-sensitive server does.
	packed := fmt.Sprintf("# pack-refs with: peeled fully-peeled sorted \n%s refs/heads/Main\n%s refs/heads/main\n%s refs/heads/other\n", evil, main, evil)
	if err := os.WriteFile(filepath.Join(src, ".git", "packed-refs"), []byte(packed), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "reg.git")
	repo, err := gitx.Clone(ctx, src, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Fetch(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if sha, _ := repo.Branch(ctx, "main"); sha != main {
		t.Fatalf("main = %s, want %s", sha, main)
	}
	if refs := git(t, dir, "for-each-ref", "--format=%(refname)"); strings.Contains(refs, "other") || strings.Contains(refs, "Main") {
		t.Fatalf("unexpected refs fetched:\n%s", refs)
	}
}

func TestExportWritesRawBlobs(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[filter \"pwn\"]\n\tsmudge = touch "+marker+"; cat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	write(t, src, ".gitattributes", "* filter=pwn\nnotes.txt ident\n")
	write(t, src, "notes.txt", "$Id$")
	sha := commit(t, src, "filters")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := repo.Export(ctx, sha, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a filter driver ran")
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "notes.txt")); string(b) != "$Id$" {
		t.Fatalf("ident applied: %q", b)
	}
}

func TestInheritedGitEnvironmentIsIgnored(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	want := git(t, src, "rev-parse", "HEAD")
	other := origin(t)
	git(t, other, "checkout", "-q", "-b", "elsewhere")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	if sha, err := repo.Branch(ctx, "main"); err != nil || sha != want {
		t.Fatalf("GIT_DIR redirected git: %q %v", sha, err)
	}
}

func TestRemoteDefaultBranch(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	git(t, src, "branch", "-m", "trunk")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := repo.DefaultBranch(ctx); err != nil || b != "trunk" {
		t.Fatalf("default branch = %q, %v", b, err)
	}
	if _, err := repo.Branch(ctx, "trunk"); err != nil {
		t.Fatal("the default branch must be fetched by Clone")
	}
}

// A crafted tree can hold a ".." entry; exporting it must not write outside dest.
func TestExportRefusesEscapingPaths(t *testing.T) {
	ctx := context.Background()
	repo, err := gitx.Clone(ctx, origin(t), filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	mktree := func(entries string) string {
		cmd := exec.Command("git", "mktree")
		cmd.Dir, cmd.Stdin = repo.Dir, strings.NewReader(entries)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("mktree: %v", err)
		}
		return strings.TrimSpace(string(out))
	}
	blob, err := repo.Git(ctx, "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	inner := mktree("100644 blob " + blob + "\tescaped\n")
	evil := mktree("040000 tree " + inner + "\t..\n")

	root := t.TempDir()
	if err := repo.Export(ctx, evil, filepath.Join(root, "dest")); err == nil {
		t.Error("exporting a path that escapes the destination must fail")
	}
	if _, err := os.Stat(filepath.Join(root, "escaped")); err == nil {
		t.Fatal("a file was written outside the destination")
	}
}

// A remote asking for credentials fails at once instead of prompting: hooks
// and timers have nobody to answer.
func TestCredentialPromptsAreDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="skills"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	// The go command exports GIT_TERMINAL_PROMPT=0 to tests: Skillcrew must set it itself.
	for _, name := range []string{"GIT_TERMINAL_PROMPT", "GIT_ASKPASS", "SSH_ASKPASS"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := gitx.Clone(ctx, srv.URL+"/skills.git", filepath.Join(t.TempDir(), "reg.git"))
	if err == nil || !strings.Contains(err.Error(), "terminal prompts disabled") {
		t.Fatalf("expected git to refuse to prompt, got %v", err)
	}
}

// Tags are immutable here: a tag known locally needs no network round trip,
// unless the caller asks to verify that it did not move upstream.
func TestFetchTagsOnlyFetchesMissingTags(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	first := git(t, src, "rev-parse", "HEAD")
	git(t, src, "tag", "v1")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.FetchTags(ctx, false, "v1"); err != nil {
		t.Fatal(err)
	}
	if sha, err := repo.Tag(ctx, "v1"); err != nil || sha != first {
		t.Fatalf("tag v1 = %q, %v", sha, err)
	}
	git(t, repo.Dir, "config", "remote.origin.url", filepath.Join(t.TempDir(), "gone"))
	commands := tu.CountGit(t)
	if err := repo.FetchTags(ctx, false, "v1", "v1"); err != nil {
		t.Fatalf("a known tag must not contact the remote: %v", err)
	}
	if n := tu.Network(commands()); len(n) > 0 {
		t.Fatalf("network commands for a known tag: %v", n)
	}
	if err := repo.FetchTags(ctx, true, "v1"); err == nil {
		t.Error("verifying tags must contact the remote")
	}
	if err := repo.FetchTags(ctx, false, "v2"); err == nil {
		t.Error("a missing tag must be fetched from the remote")
	}
}

func TestFetchTagsCreatesSeveralTagsAtOnce(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	git(t, src, "tag", "v1")
	git(t, src, "tag", "-a", "-m", "annotated", "v2")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.FetchTags(ctx, false, "v1", "v2"); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"v1", "v2"} {
		if got, want := git(t, repo.Dir, "rev-parse", "refs/tags/"+tag), git(t, src, "rev-parse", "refs/tags/"+tag); got != want {
			t.Errorf("tag %s = %s, want %s", tag, got, want)
		}
	}
}

// Pins are resolved with a fixed number of git commands, by exact tag name
// and full commit id only.
func TestResolvePins(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	first := git(t, src, "rev-parse", "HEAD")
	git(t, src, "tag", "v1")
	write(t, src, "sub/run.sh", "#!/bin/sh\necho v2")
	second := commit(t, src, "second")
	git(t, src, "tag", "-a", "-m", "annotated", "v2")
	git(t, src, "tag", "refs/tags/v3") // creates refs/tags/refs/tags/v3
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.FetchTags(ctx, false, "v1", "v2", "refs/tags/v3"); err != nil {
		t.Fatal(err)
	}
	commands := tu.CountGit(t)
	pins := []gitx.Pin{
		{Tag: "v1", Dir: "sub"},
		{SHA: first, Dir: "sub"},
		{Tag: "v2", Dir: "sub"},
		{SHA: second, Dir: "."},
		{Tag: "nope", Dir: "sub"},
		{SHA: first, Dir: "missing"},
		{SHA: first[:12], Dir: "sub"},
		{SHA: "main", Dir: "sub"},
		{Tag: "v3", Dir: "sub"},
		{SHA: strings.Repeat("0", 40), Dir: "sub"},
		{SHA: first, Dir: "sub\n" + first + ":a.txt"},
		{SHA: first, Dir: "a.txt"},
	}
	got := repo.ResolvePins(ctx, pins)
	if n := len(commands()); n > 2 {
		t.Errorf("%d git commands to resolve %d pins, want at most 2", n, len(pins))
	}
	want := []gitx.Target{
		{Commit: first, Tree: git(t, src, "rev-parse", first+":sub")},
		{Commit: first, Tree: git(t, src, "rev-parse", first+":sub")},
		{Commit: second, Tree: git(t, src, "rev-parse", second+":sub")},
		{Commit: second, Tree: git(t, src, "rev-parse", second+"^{tree}")},
	}
	for i, p := range pins {
		switch {
		case i < len(want) && (got[i] != want[i]):
			t.Errorf("pin %+v = %+v, want %+v", p, got[i], want[i])
		case i >= len(want) && got[i].Err == nil:
			t.Errorf("pin %+v must fail, got %+v", p, got[i])
		}
	}
}

// Every wanted tree is exported with one listing per commit and a single
// object stream, whatever the number of trees.
func TestExportTreesInOneBatch(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	write(t, src, "skills/one/SKILL.md", "one v1")
	write(t, src, "skills/two/SKILL.md", "two")
	write(t, src, "skills/two/nested/deep.txt", "deep")
	first := commit(t, src, "skills")
	write(t, src, "skills/one/SKILL.md", "one v2")
	second := commit(t, src, "one v2")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	tree := func(rev string) string { return git(t, src, "rev-parse", rev) }
	root := t.TempDir()
	trees := []gitx.ExportTree{
		{Commit: second, Dir: "skills/one", OID: tree(second + ":skills/one"), Dest: filepath.Join(root, "one")},
		{Commit: second, Dir: "skills/two", OID: tree(second + ":skills/two"), Dest: filepath.Join(root, "two")},
		{Commit: second, Dir: "skills/two/nested", OID: tree(second + ":skills/two/nested"), Dest: filepath.Join(root, "nested")},
		{Commit: first, Dir: "skills/one", OID: tree(first + ":skills/one"), Dest: filepath.Join(root, "one-v1")},
		{Commit: second, Dir: "sub", OID: tree(second + ":skills/two"), Dest: filepath.Join(root, "mismatch")},
		{OID: tree(second + ":sub"), Dest: filepath.Join(root, "by-id")},
	}
	commands := tu.CountGit(t)
	errs := repo.ExportTrees(ctx, trees)
	listings, streams := 0, 0
	for _, c := range commands() {
		listings += strings.Count(c, "ls-tree")
		streams += strings.Count(c, "cat-file")
	}
	if listings != 3 || streams != 1 {
		t.Errorf("%d listings and %d object streams, want 3 (one per commit, one by id) and 1", listings, streams)
	}
	for file, want := range map[string]string{
		"one/SKILL.md":        "one v2",
		"two/SKILL.md":        "two",
		"two/nested/deep.txt": "deep",
		"nested/deep.txt":     "deep",
		"one-v1/SKILL.md":     "one v1",
		"by-id/run.sh":        "#!/bin/sh",
	} {
		if b, err := os.ReadFile(filepath.Join(root, file)); err != nil || string(b) != want {
			t.Errorf("%s = %q (%v), want %q", file, b, err, want)
		}
	}
	for i, err := range errs {
		if (err != nil) != (trees[i].Dest == filepath.Join(root, "mismatch")) {
			t.Errorf("export of %s: %v", trees[i].Dest, err)
		}
	}
}

// A tree over the limits is rejected from its listing, before any object is
// streamed, and the error says it would fail again.
func TestExportRejectsOversizedTreesBeforeStreaming(t *testing.T) {
	ctx := context.Background()
	src := origin(t)
	sha := git(t, src, "rev-parse", "HEAD")
	repo, err := gitx.Clone(ctx, src, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(b int64, f int) { gitx.ExportMaxBytes, gitx.ExportMaxFiles = b, f }(gitx.ExportMaxBytes, gitx.ExportMaxFiles)
	for name, set := range map[string]func(){
		"bytes": func() { gitx.ExportMaxFiles, gitx.ExportMaxBytes = 100, 3 },
		"files": func() { gitx.ExportMaxFiles, gitx.ExportMaxBytes = 1, 1<<20 },
	} {
		t.Run(name, func(t *testing.T) {
			set()
			commands := tu.CountGit(t)
			err := repo.Export(ctx, sha, t.TempDir())
			var rejected *gitx.RejectedError
			if !errors.As(err, &rejected) {
				t.Fatalf("expected a RejectedError, got %v", err)
			}
			for _, c := range commands() {
				if strings.Contains(c, "cat-file") {
					t.Fatalf("objects were streamed: %s", c)
				}
			}
		})
	}
}
