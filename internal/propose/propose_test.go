package propose_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/propose"
	"github.com/yoanbernabeu/skillcrew/internal/testutil"
)

type fakeOpener struct {
	calls [][]string
	url   string
	err   error
}

func (f *fakeOpener) Open(_ context.Context, remoteURL, base, branch, title, body string) (string, error) {
	f.calls = append(f.calls, []string{remoteURL, base, branch, title, body})
	return f.url, f.err
}

const skillDir = "plugins/security/skills/review"

func setup(t *testing.T) (*testutil.Origin, *gitx.Repo) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "dev")
	t.Setenv("GIT_AUTHOR_EMAIL", "dev@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "dev")
	t.Setenv("GIT_COMMITTER_EMAIL", "dev@example.com")
	o := testutil.NewOrigin(t, map[string]string{
		".claude-plugin/marketplace.json":        testutil.Marketplace("security"),
		testutil.SkillPath("security", "review"): testutil.SkillMD("review", "v1\n"),
		skillDir + "/notes.md":                   "old notes\n",
		testutil.SkillPath("security", "audit"):  testutil.SkillMD("audit", "audit\n"),
	})
	repo, err := gitx.Clone(context.Background(), o.Dir, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	return o, repo
}

func source(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func request(src string) propose.Request {
	return propose.Request{
		Base:     "main",
		Branch:   "skillcrew/review-20261011-120000",
		SkillDir: skillDir,
		Name:     "review",
		Source:   src,
		Title:    "Improve review skill",
		Body:     "Proposed by skillcrew.",
	}
}

func assertCleanWorktrees(t *testing.T, repo *gitx.Repo) {
	t.Helper()
	out, err := repo.Git(context.Background(), "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "worktree "); n != 1 {
		t.Fatalf("worktrees left behind:\n%s", out)
	}
}

func hasBranch(o *testutil.Origin, branch string) bool {
	return o.Git("branch", "--list", branch) != ""
}

func TestProposeModifiesExistingSkill(t *testing.T) {
	o, repo := setup(t)
	src := source(t, map[string]string{
		"SKILL.md":     testutil.SkillMD("review", "v2\n"),
		"scripts/x.sh": "echo hi\n",
	})
	op := &fakeOpener{url: "https://github.com/acme/skills/pull/1"}
	req := request(src)

	res, err := propose.Propose(context.Background(), repo, req, op)
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != req.Branch || res.URL != op.url {
		t.Fatalf("result = %+v", res)
	}
	if len(op.calls) != 1 {
		t.Fatalf("opener calls = %v", op.calls)
	}
	want := []string{o.Dir, "main", req.Branch, req.Title, req.Body}
	for i := range want {
		if op.calls[0][i] != want[i] {
			t.Errorf("opener arg %d = %q, want %q", i, op.calls[0][i], want[i])
		}
	}
	if got := o.Git("show", req.Branch+":"+skillDir+"/SKILL.md"); got != strings.TrimSpace(testutil.SkillMD("review", "v2\n")) {
		t.Errorf("SKILL.md on branch = %q", got)
	}
	if got := o.Git("log", "-1", "--format=%s", req.Branch); got != req.Title {
		t.Errorf("commit message = %q", got)
	}
	files := o.Git("show", "--name-only", "--format=", req.Branch)
	wantFiles := []string{skillDir + "/SKILL.md", skillDir + "/notes.md", skillDir + "/scripts/x.sh"}
	if strings.Join(strings.Fields(files), ",") != strings.Join(wantFiles, ",") {
		t.Errorf("changed files = %q, want %v", files, wantFiles)
	}
	if !strings.Contains(o.Git("show", "--stat", req.Branch), "3 files changed") {
		t.Errorf("unexpected stat:\n%s", o.Git("show", "--stat", req.Branch))
	}
	if o.Git("rev-parse", "main") != o.Git("rev-parse", req.Branch+"^") {
		t.Error("branch must be based on main")
	}
	assertCleanWorktrees(t, repo)
}

func TestProposeRestoresNameOfAliasedCopy(t *testing.T) {
	o, repo := setup(t)
	src := source(t, map[string]string{"SKILL.md": testutil.SkillMD("sec-review", "v2\n")})
	if _, err := propose.Propose(context.Background(), repo, request(src), &fakeOpener{}); err != nil {
		t.Fatal(err)
	}
	got := o.Git("show", "skillcrew/review-20261011-120000:"+skillDir+"/SKILL.md")
	if !strings.Contains(got, "\nname: review\n") || strings.Contains(got, "name: sec-review") {
		t.Fatalf("SKILL.md = %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "SKILL.md")); !strings.Contains(string(b), "sec-review") {
		t.Fatal("the source must not be modified")
	}
}

func TestProposeNewSkill(t *testing.T) {
	o, repo := setup(t)
	src := source(t, map[string]string{"SKILL.md": testutil.SkillMD("lint", "new\n")})
	req := request(src)
	for _, dir := range []string{"plugins/security/skills/lint", "plugins/newplugin/skills/lint"} {
		req.SkillDir, req.Name, req.Branch = dir, "lint", "skillcrew/"+strings.ReplaceAll(dir, "/", "-")
		if _, err := propose.Propose(context.Background(), repo, req, &fakeOpener{}); err != nil {
			t.Fatal(err)
		}
		if files := o.Git("show", "--name-only", "--format=", req.Branch); files != dir+"/SKILL.md" {
			t.Fatalf("changed files = %q", files)
		}
	}
	assertCleanWorktrees(t, repo)
}

func TestProposeNoChanges(t *testing.T) {
	o, repo := setup(t)
	src := source(t, map[string]string{
		"SKILL.md": testutil.SkillMD("review", "v1\n"),
		"notes.md": "old notes\n",
	})
	op := &fakeOpener{}
	req := request(src)
	_, err := propose.Propose(context.Background(), repo, req, op)
	if !errors.Is(err, propose.ErrNoChanges) {
		t.Fatalf("err = %v, want ErrNoChanges", err)
	}
	if hasBranch(o, req.Branch) || len(op.calls) != 0 {
		t.Fatal("nothing must be pushed nor opened")
	}
	assertCleanWorktrees(t, repo)
}

func TestProposeInvalidSkill(t *testing.T) {
	o, repo := setup(t)
	src := source(t, map[string]string{"SKILL.md": "---\nname: review\n---\nno description\n"})
	op := &fakeOpener{}
	req := request(src)
	if _, err := propose.Propose(context.Background(), repo, req, op); err == nil {
		t.Fatal("expected a validation error")
	}
	if hasBranch(o, req.Branch) || len(op.calls) != 0 {
		t.Fatal("nothing must be pushed nor opened")
	}
	assertCleanWorktrees(t, repo)
}

func TestProposeRejectsUnsafeSkillDir(t *testing.T) {
	_, repo := setup(t)
	src := source(t, map[string]string{"SKILL.md": testutil.SkillMD("review", "v2\n")})
	for _, dir := range []string{"", ".", "../review", "/abs/review", "a/../../review"} {
		req := request(src)
		req.SkillDir = dir
		if _, err := propose.Propose(context.Background(), repo, req, &fakeOpener{}); err == nil {
			t.Errorf("SkillDir %q must be rejected", dir)
		}
	}
	assertCleanWorktrees(t, repo)
}

func TestProposeUnknownBase(t *testing.T) {
	o, repo := setup(t)
	src := source(t, map[string]string{"SKILL.md": testutil.SkillMD("review", "v2\n")})
	req := request(src)
	req.Base = "nope"
	if _, err := propose.Propose(context.Background(), repo, req, &fakeOpener{}); err == nil {
		t.Fatal("expected an error")
	}
	if hasBranch(o, req.Branch) {
		t.Fatal("nothing must be pushed")
	}
	assertCleanWorktrees(t, repo)
}

func TestProposeOpenerFailure(t *testing.T) {
	o, repo := setup(t)
	src := source(t, map[string]string{"SKILL.md": testutil.SkillMD("review", "v2\n")})
	req := request(src)
	res, err := propose.Propose(context.Background(), repo, req, &fakeOpener{err: errors.New("no gh")})
	if !errors.Is(err, propose.ErrNoPullRequest) || !strings.Contains(err.Error(), "no gh") {
		t.Fatalf("err = %v", err)
	}
	if res.Branch != req.Branch || res.URL != "" {
		t.Fatalf("result = %+v", res)
	}
	if !hasBranch(o, req.Branch) {
		t.Fatal("branch must be pushed")
	}
	assertCleanWorktrees(t, repo)
}

func TestProposeExistingBranchIsNotOverwritten(t *testing.T) {
	o, repo := setup(t)
	req := request(source(t, map[string]string{"SKILL.md": testutil.SkillMD("review", "v2\n")}))
	if _, err := propose.Propose(context.Background(), repo, req, &fakeOpener{}); err != nil {
		t.Fatal(err)
	}
	first := o.Git("rev-parse", req.Branch)
	req.Source = source(t, map[string]string{"SKILL.md": testutil.SkillMD("review", "v3\n")})
	if _, err := propose.Propose(context.Background(), repo, req, &fakeOpener{}); err == nil {
		t.Fatal("pushing over an existing branch must fail")
	}
	if o.Git("rev-parse", req.Branch) != first {
		t.Fatal("existing branch was overwritten")
	}
	assertCleanWorktrees(t, repo)
}

// TestProposeRefusesRegistrySymlinks reproduces a registry commit turning a
// component of the skill directory into a symlink (or a file): Propose must
// fail without touching anything outside the registry.
func TestProposeRefusesRegistrySymlinks(t *testing.T) {
	cases := map[string]string{
		"symlinked parent":    "plugins/sec/skills",
		"symlinked skill dir": "plugins/sec/skills/review",
		"symlinked plugin":    "plugins/sec",
	}
	for name, link := range cases {
		t.Run(name, func(t *testing.T) {
			victim := t.TempDir()
			data := filepath.Join(victim, "review", "data.txt")
			if err := os.MkdirAll(filepath.Dir(data), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(data, []byte("precious\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			o := testutil.NewOrigin(t, map[string]string{".claude-plugin/marketplace.json": testutil.Marketplace("sec")})
			p := filepath.Join(o.Dir, filepath.FromSlash(link))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			// Point the link so that SkillDir resolves to victim/review.
			dest := victim
			switch link {
			case "plugins/sec/skills/review":
				dest = filepath.Join(victim, "review")
			case "plugins/sec":
				if err := os.MkdirAll(filepath.Join(victim, "skills", "review"), 0o755); err != nil {
					t.Fatal(err)
				}
				data = filepath.Join(victim, "skills", "review", "data.txt")
				if err := os.WriteFile(data, []byte("precious\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(dest, p); err != nil {
				t.Fatal(err)
			}
			o.Commit("malicious")
			repo, err := gitx.Clone(context.Background(), o.Dir, filepath.Join(t.TempDir(), "reg.git"))
			if err != nil {
				t.Fatal(err)
			}

			req := request(source(t, map[string]string{"SKILL.md": testutil.SkillMD("review", "pwned\n")}))
			req.SkillDir = "plugins/sec/skills/review"
			op := &fakeOpener{}
			if _, err := propose.Propose(context.Background(), repo, req, op); err == nil {
				t.Fatal("Propose must refuse a symlinked skill directory")
			}
			if b, err := os.ReadFile(data); err != nil || string(b) != "precious\n" {
				t.Fatalf("victim file = %q, %v", b, err)
			}
			if entries, _ := os.ReadDir(filepath.Dir(data)); len(entries) != 1 {
				t.Fatalf("files written outside the registry: %v", entries)
			}
			if hasBranch(o, req.Branch) || len(op.calls) != 0 {
				t.Fatal("nothing must be pushed nor opened")
			}
			assertCleanWorktrees(t, repo)
		})
	}
}

func TestProposeRefusesFileInSkillPath(t *testing.T) {
	o, repo := setup(t)
	o.Write(map[string]string{"plugins/security/skills/blob": "not a dir\n"})
	o.Commit("blob")
	if err := repo.Fetch(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
	req := request(source(t, map[string]string{"SKILL.md": testutil.SkillMD("review", "v2\n")}))
	req.SkillDir = "plugins/security/skills/blob/review"
	if _, err := propose.Propose(context.Background(), repo, req, &fakeOpener{}); err == nil {
		t.Fatal("a file in the skill path must be refused")
	}
	assertCleanWorktrees(t, repo)
}

func TestProposeRejectsInvalidName(t *testing.T) {
	o, repo := setup(t)
	src := source(t, map[string]string{"SKILL.md": testutil.SkillMD("review", "v2\n")})
	// Propose works in a temporary directory: "../../x" would land in TMPDIR.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, name := range []string{"../../x", "", "Review", "a/b"} {
		req := request(src)
		req.Name = name
		if _, err := propose.Propose(context.Background(), repo, req, &fakeOpener{}); err == nil {
			t.Errorf("name %q must be rejected", name)
		}
	}
	if hasBranch(o, request(src).Branch) {
		t.Fatal("nothing must be pushed")
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("files left in TMPDIR: %v", entries)
	}
	assertCleanWorktrees(t, repo)
}
