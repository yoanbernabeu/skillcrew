package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/agent"
	"github.com/yoanbernabeu/skillcrew/internal/cli"
	"github.com/yoanbernabeu/skillcrew/internal/fsx"
	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/paths"
	"github.com/yoanbernabeu/skillcrew/internal/scheduler"
	"github.com/yoanbernabeu/skillcrew/internal/state"
	tu "github.com/yoanbernabeu/skillcrew/internal/testutil"
)

const governance = `org: acme
skills:
  security:review: required
  security:secrets: recommended
  docs:pdf: available
  docs:old:
    status: deprecated
    message: Use docs:pdf.
    replaced_by: docs:pdf
teams:
  backend:
    skills:
      docs:pdf: required
`

// fakeAgent records hook installation in memory.
type fakeAgent struct {
	name      string
	detected  bool
	installed bool
	bin       string
}

func (f *fakeAgent) Name() string                       { return f.name }
func (f *fakeAgent) Detected(string) bool               { return f.detected }
func (f *fakeAgent) HookInstalled(string) (bool, error) { return f.installed, nil }
func (f *fakeAgent) InstallHook(_, bin string) error {
	f.installed, f.bin = true, bin
	return nil
}
func (f *fakeAgent) UninstallHook(string) error { f.installed = false; return nil }

type fakeScheduler struct {
	installed bool
	interval  time.Duration
}

func (f *fakeScheduler) Install(_ string, interval time.Duration) error {
	f.installed, f.interval = true, interval
	return nil
}
func (f *fakeScheduler) Uninstall() error { f.installed = false; return nil }
func (f *fakeScheduler) Installed() bool  { return f.installed }

type fakeOpener struct{ branch string }

func (f *fakeOpener) Open(_ context.Context, _, _, branch, _, _ string) (string, error) {
	f.branch = branch
	return "https://example.com/pr/1", nil
}

type env struct {
	background [][]string // commands the hook started in the background
	opener     *fakeOpener
	t          *testing.T
	origin     *tu.Origin
	paths      paths.Paths
	claude     *fakeAgent
	cursor     *fakeAgent
	scheduler  *fakeScheduler
	now        time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	origin := tu.NewOrigin(t, map[string]string{
		".claude-plugin/marketplace.json":   tu.Marketplace("security", "docs"),
		"skillcrew.yaml":                    governance,
		tu.SkillPath("security", "review"):  tu.SkillMD("review", "v1"),
		tu.SkillPath("security", "secrets"): tu.SkillMD("secrets", "v1"),
		tu.SkillPath("docs", "pdf"):         tu.SkillMD("pdf", "v1"),
		tu.SkillPath("docs", "old"):         tu.SkillMD("old", "v1"),
	})
	return &env{
		t:         t,
		origin:    origin,
		paths:     paths.Paths{Home: t.TempDir()},
		claude:    &fakeAgent{name: "claude-code", detected: true},
		cursor:    &fakeAgent{name: "cursor"},
		scheduler: &fakeScheduler{},
		opener:    &fakeOpener{},
		now:       time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC),
	}
}

func (e *env) app(out *bytes.Buffer) *cli.App {
	return &cli.App{
		Paths:       e.paths,
		In:          strings.NewReader(""),
		Out:         out,
		Err:         out,
		Agents:      []agent.Agent{e.claude, e.cursor},
		Scheduler:   func(string) (scheduler.Scheduler, error) { return e.scheduler, nil },
		Opener:      e.opener,
		LockTimeout: 300 * time.Millisecond,
		// The background sync runs synchronously in tests, and is recorded.
		Background: func(args ...string) error {
			e.background = append(e.background, args)
			_, err := e.run(args...)
			return err
		},
		Executable: func() (string, error) { return "/usr/local/bin/skillcrew", nil },
		Now:        func() time.Time { return e.now },
		Version:    "test",
	}
}

// run executes a command and returns its output.
func (e *env) run(args ...string) (string, error) {
	e.t.Helper()
	var out bytes.Buffer
	cmd := e.app(&out).Command()
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func (e *env) mustRun(args ...string) string {
	e.t.Helper()
	out, err := e.run(args...)
	if err != nil {
		e.t.Fatalf("skillcrew %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (e *env) installed(name string) bool {
	_, err := os.Stat(filepath.Join(e.paths.AgentsSkills(), name, "SKILL.md"))
	return err == nil
}

func (e *env) config() state.Config {
	cfg, err := state.LoadConfig(e.paths.Config())
	if err != nil {
		e.t.Fatal(err)
	}
	return cfg
}

func TestInit(t *testing.T) {
	e := newEnv(t)
	out := e.mustRun("init", e.origin.Dir)

	if cfg := e.config(); cfg.Registry != e.origin.Dir || cfg.Ref != "main" {
		t.Fatalf("config: %+v", cfg)
	}
	if !e.claude.installed || e.claude.bin != "/usr/local/bin/skillcrew" || e.cursor.installed {
		t.Fatal("hooks must be installed for detected agents only")
	}
	if !e.scheduler.installed || e.scheduler.interval != time.Hour {
		t.Fatalf("timer: %+v", e.scheduler)
	}
	for _, name := range []string{"review", "secrets"} {
		if !e.installed(name) {
			t.Errorf("%s should be installed", name)
		}
	}
	if e.installed("old") {
		t.Error("a deprecated skill must not spread to new machines")
	}
	if e.installed("pdf") {
		t.Error("available skill installed")
	}
	for _, want := range []string{"claude-code", "review"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should mention %q:\n%s", want, out)
		}
	}

	// Running init again with the same registry is harmless.
	e.mustRun("init", e.origin.Dir)
	if _, err := e.run("init", t.TempDir()); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("init with another registry must fail, got %v", err)
	}
}

func TestInitWithTeamAndOptions(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir, "--team", "backend", "--no-hooks", "--no-timer")
	if e.claude.installed || e.scheduler.installed {
		t.Fatal("--no-hooks and --no-timer not honored")
	}
	if !e.installed("pdf") {
		t.Fatal("team skill not installed")
	}
	if _, err := newEnv(t).run("init", e.origin.Dir, "--team", "ghost"); err == nil {
		t.Fatal("unknown team must fail")
	}
}

func TestInitRejectsNonRegistry(t *testing.T) {
	e := newEnv(t)
	plain := tu.NewOrigin(t, map[string]string{"README.md": "hi"})
	if _, err := e.run("init", plain.Dir); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(e.paths.Config()); !os.IsNotExist(err) {
		t.Fatal("a failed init must not leave a config")
	}
	if _, err := os.Stat(e.paths.Registry()); !os.IsNotExist(err) {
		t.Fatal("a failed init must not leave a clone")
	}
}

func TestCommandsRequireInit(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{{"sync"}, {"status"}, {"list"}, {"disable", "x"}} {
		if _, err := e.run(args...); err == nil || !strings.Contains(err.Error(), "init") {
			t.Errorf("%v: expected a not initialized error, got %v", args, err)
		}
	}
}

func TestSync(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "v2")})
	e.origin.Commit("v2")

	out := e.mustRun("sync")
	if !strings.Contains(out, "review") {
		t.Fatalf("sync output should list the update:\n%s", out)
	}
	if out := e.mustRun("sync"); !strings.Contains(out, "up to date") {
		t.Fatalf("second sync should be up to date:\n%s", out)
	}
}

func TestHookMode(t *testing.T) {
	e := newEnv(t)
	// Never fails nor prints anything, even when not initialized.
	if out := e.mustRun("sync", "--hook"); out != "" {
		t.Fatalf("hook output must be empty, got %q", out)
	}
	if out := e.mustRun("sync", "--hook=json"); strings.TrimSpace(out) != "{}" {
		t.Fatalf("json hook output must be {}, got %q", out)
	}
	e.mustRun("init", e.origin.Dir)
	if out := e.mustRun("sync", "--quiet"); out != "" {
		t.Fatalf("quiet output must be empty, got %q", out)
	}
}

func TestStatusAndList(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if err := os.WriteFile(filepath.Join(e.paths.AgentsSkills(), "secrets", "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := e.mustRun("status")
	for _, want := range []string{"security:review", "required", "modified"} {
		if !strings.Contains(out, want) {
			t.Errorf("status should contain %q:\n%s", want, out)
		}
	}

	var status struct {
		Skills []struct {
			ID, Name, Status, State string
		}
	}
	if err := json.Unmarshal([]byte(e.mustRun("status", "--json")), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Skills) != 2 {
		t.Fatalf("status json: %+v", status)
	}
	if out := e.mustRun("list"); !strings.Contains(out, "Use docs:pdf.") {
		t.Errorf("list should show the deprecation notice:\n%s", out)
	}

	out = e.mustRun("list", "--available")
	if !strings.Contains(out, "docs:pdf") || strings.Contains(out, "security:review") {
		t.Fatalf("list --available:\n%s", out)
	}
	if out := e.mustRun("list"); !strings.Contains(out, "security:review") || !strings.Contains(out, "docs:pdf") {
		t.Fatalf("list:\n%s", out)
	}
}

func TestPreferences(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)

	e.mustRun("disable", "secrets")
	if e.installed("secrets") || len(e.config().Disabled) != 1 {
		t.Fatal("disable failed")
	}
	e.mustRun("enable", "security:secrets")
	if !e.installed("secrets") || len(e.config().Disabled) != 0 {
		t.Fatal("enable failed")
	}
	if _, err := e.run("disable", "review"); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("disabling a required skill must fail, got %v", err)
	}

	e.mustRun("install", "pdf")
	if !e.installed("pdf") {
		t.Fatal("install failed")
	}
	e.mustRun("remove", "pdf")
	if e.installed("pdf") {
		t.Fatal("remove failed")
	}
	if out := e.mustRun("install", "review"); !strings.Contains(out, "already installed (required)") {
		t.Fatalf("installing a required skill is a no-op:\n%s", out)
	}
	if _, err := e.run("install", "old"); err == nil {
		t.Fatal("installing a deprecated skill must fail")
	}
	if _, err := e.run("install", "nope"); err == nil {
		t.Fatal("unknown skill must fail")
	}

	e.mustRun("team", "join", "backend")
	if !e.installed("pdf") {
		t.Fatal("team skill not installed after join")
	}
	e.mustRun("team", "leave", "backend")
	if e.installed("pdf") {
		t.Fatal("team skill still installed after leave")
	}
	if _, err := e.run("team", "join", "ghost"); err == nil {
		t.Fatal("joining an unknown team must fail")
	}
}

func TestValidate(t *testing.T) {
	e := newEnv(t)
	if out := e.mustRun("validate", e.origin.Dir); !strings.Contains(out, "valid") {
		t.Fatalf("validate output:\n%s", out)
	}
	e.origin.Write(map[string]string{"skillcrew.yaml": "org: acme\nskills:\n  ghost:skill: required\n"})
	out, err := e.run("validate", e.origin.Dir)
	if err == nil || !strings.Contains(out, "ghost:skill") {
		t.Fatalf("expected a validation failure, got %v\n%s", err, out)
	}
}

func TestDoctor(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	out := e.mustRun("doctor")
	for _, want := range []string{"git", "registry", "claude-code", "timer"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor should check %q:\n%s", want, out)
		}
	}
	e.claude.installed = false
	if out, err := e.run("doctor"); err == nil {
		t.Fatalf("doctor must fail when a hook is missing:\n%s", out)
	}
}

func TestUninstall(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if out, err := e.run("uninstall"); err != nil || !strings.Contains(out, "Aborted.") || !e.installed("review") {
		t.Fatalf("uninstall without confirmation must abort, got %v\n%s", err, out)
	}
	e.mustRun("uninstall", "--yes")
	if e.installed("review") || e.claude.installed || e.scheduler.installed {
		t.Fatal("uninstall left things behind")
	}
	for _, p := range []string{e.paths.Config(), e.paths.Cache(), e.paths.Registry()} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s not removed", p)
		}
	}
}

func TestVersion(t *testing.T) {
	e := newEnv(t)
	if out := e.mustRun("version"); !strings.Contains(out, "test") {
		t.Fatalf("version output: %q", out)
	}
}

func gitIdentity(t *testing.T) {
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.com")
	}
}

func (e *env) branchFile(path string) string {
	return e.origin.Git("show", e.opener.branch+":"+path)
}

func TestProposeLocalChanges(t *testing.T) {
	gitIdentity(t)
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if _, err := e.run("propose", "secrets"); err == nil {
		t.Fatal("proposing without changes must fail")
	}
	edited := tu.SkillMD("secrets", "better")
	if err := os.WriteFile(filepath.Join(e.paths.AgentsSkills(), "secrets", "SKILL.md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	out := e.mustRun("propose", "secrets")
	if !strings.Contains(out, "https://example.com/pr/1") {
		t.Fatalf("output: %s", out)
	}
	if got := e.branchFile(tu.SkillPath("security", "secrets")); !strings.HasSuffix(got, "better") {
		t.Fatalf("branch content: %q", got)
	}
}

func TestProposeFromBackup(t *testing.T) {
	gitIdentity(t)
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if err := os.WriteFile(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md"), []byte(tu.SkillMD("review", "mine")), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustRun("sync") // restores the team version and backs up ours
	e.mustRun("propose", "security:review")
	if got := e.branchFile(tu.SkillPath("security", "review")); !strings.HasSuffix(got, "mine") {
		t.Fatalf("branch content: %q", got)
	}
}

func TestProposePersonalSkill(t *testing.T) {
	gitIdentity(t)
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	dir := filepath.Join(e.paths.AgentsSkills(), "mine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(tu.SkillMD("mine", "x")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run("propose", "mine"); err == nil {
		t.Fatal("a personal skill needs --plugin")
	}
	e.mustRun("propose", "mine", "--plugin", "docs")
	if got := e.branchFile(tu.SkillPath("docs", "mine")); !strings.HasSuffix(got, "x") {
		t.Fatalf("branch content: %q", got)
	}
}

func TestRegistryInitThenValidate(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(t.TempDir(), "acme-skills")
	out := e.mustRun("registry", "init", dir)
	if !strings.Contains(out, "skillcrew.yaml") {
		t.Fatalf("output: %s", out)
	}
	e.mustRun("validate", dir)
}

func TestSyncReportsRestoredSkills(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if err := os.WriteFile(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := e.mustRun("sync")
	if strings.Contains(out, "up to date") || !strings.Contains(out, "backed up") {
		t.Fatalf("a restore is a change:\n%s", out)
	}
}

func TestNoOpPreferencesFail(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if _, err := e.run("remove", "pdf"); err == nil {
		t.Error("removing a skill that was not installed on demand must fail")
	}
	if _, err := e.run("enable", "secrets"); err == nil {
		t.Error("enabling a skill that is not disabled must fail")
	}
}

func TestDoctorReportsCorruptLock(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if err := os.WriteFile(e.paths.Lock(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := e.run("doctor"); err == nil {
		t.Fatalf("doctor must fail on a corrupt lock:\n%s", out)
	}
}

func TestInitRefusesEmbeddedPassword(t *testing.T) {
	e := newEnv(t)
	_, err := e.run("init", "https://bob:s3cret@github.com/acme/skills.git")
	if err == nil || strings.Contains(err.Error(), "s3cret") || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("expected a redacted refusal, got %v", err)
	}
}

func TestPrivateFilesArePrivate(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if info, _ := os.Stat(e.paths.Root()); info.Mode().Perm() != 0o700 {
		t.Errorf("%s mode = %v", e.paths.Root(), info.Mode().Perm())
	}
	if info, _ := os.Stat(e.paths.Config()); info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v", info.Mode().Perm())
	}
}

func TestOutputStripsControlCharacters(t *testing.T) {
	e := newEnv(t)
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "Use docs:pdf.", "\"Use \\e]52;c;evil\\a pdf.\"", 1)})
	e.origin.Commit("escape")
	e.mustRun("init", e.origin.Dir)
	if out := e.mustRun("status"); strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("control characters reached the terminal: %q", out)
	}
}

func TestSyncJSON(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	var res struct {
		Commit    string   `json:"commit"`
		Installed []string `json:"installed"`
	}
	if err := json.Unmarshal([]byte(e.mustRun("sync", "--json")), &res); err != nil || res.Commit == "" {
		t.Fatalf("sync --json: %+v %v", res, err)
	}
}

func TestUninstallWaitsForNoSync(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	unlock, ok, err := fsx.TryLock(e.paths.SyncLock())
	if err != nil || !ok {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := e.run("uninstall", "--yes"); err == nil || !strings.Contains(err.Error(), "sync") {
		t.Fatalf("uninstall must not run during a sync, got %v", err)
	}
}

func TestInitRefusesForeignClone(t *testing.T) {
	e := newEnv(t)
	other := tu.NewOrigin(t, map[string]string{
		".claude-plugin/marketplace.json": tu.Marketplace(),
		"skillcrew.yaml":                  "org: other\n",
	})
	if _, err := gitx.Clone(context.Background(), other.Dir, e.paths.Registry()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run("init", e.origin.Dir); err == nil {
		t.Fatal("init must not reuse a clone of another repository")
	}
}

func TestInitRefusesTokenAsUsername(t *testing.T) {
	e := newEnv(t)
	_, err := e.run("init", "https://ghp_TOKEN@github.com/acme/skills.git")
	if err == nil || strings.Contains(err.Error(), "ghp_TOKEN") || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("expected a redacted refusal, got %v", err)
	}
}

// setRegistry rewrites the configured registry URL, as an older version or a
// hand edit could have left it.
func (e *env) setRegistry(url string) {
	cfg := e.config()
	cfg.Registry = url
	if err := cfg.Save(e.paths.Config()); err != nil {
		e.t.Fatal(err)
	}
}

func TestRegistryURLIsRedacted(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.setRegistry("https://ghp_SECRET@example.com/acme/skills.git")
	e.claude.installed = false // the missing hook message names the registry
	for _, args := range [][]string{{"status"}, {"status", "--json"}, {"doctor"}, {"doctor", "--json"}, {"init", e.origin.Dir}} {
		out, err := e.run(args...)
		if strings.Contains(out, "ghp_SECRET") || (err != nil && strings.Contains(err.Error(), "ghp_SECRET")) {
			t.Errorf("%v leaks the token: %v\n%s", args, err, out)
		}
	}
}

func TestExecuteSanitizesErrors(t *testing.T) {
	e := newEnv(t)
	var out, errOut bytes.Buffer
	app := e.app(&out)
	app.Err = &errOut
	app.Args = []string{"status", "--\x1b]52;c;evil\x07"}
	if code := app.Execute(context.Background()); code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if got := errOut.String(); !strings.HasPrefix(got, "Error: ") || strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("stderr: %q", got)
	}
	app.Args = []string{"version"}
	if code := app.Execute(context.Background()); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
}

func TestStatusPrintsWarningsOnce(t *testing.T) {
	e := newEnv(t)
	e.origin.Write(map[string]string{"skillcrew.yaml": governance + "  frontend:\n    skills:\n      security:review: available\n"})
	e.origin.Commit("soften")
	e.mustRun("init", e.origin.Dir, "--team", "frontend")
	out := e.mustRun("status")
	if n := strings.Count(out, "cannot soften"); n != 1 {
		t.Fatalf("the warning is printed %d times:\n%s", n, out)
	}
}

func TestInitRejectsAnotherRef(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.mustRun("init", e.origin.Dir, "--ref", "main")
	if _, err := e.run("init", e.origin.Dir, "--ref", "other"); err == nil || !strings.Contains(err.Error(), "main") {
		t.Fatalf("init with another ref must fail, got %v", err)
	}
}

func TestPreferenceConfirmations(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if _, err := e.run("team", "leave", "backend"); err == nil {
		t.Fatal("leaving a team you are not in must fail")
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"team", "join", "backend"}, "Joined team backend."},
		{[]string{"team", "leave", "backend"}, "Left team backend."},
		{[]string{"disable", "secrets"}, "Disabled security:secrets."},
		{[]string{"enable", "secrets"}, "Enabled security:secrets."},
		{[]string{"install", "pdf"}, "Installed docs:pdf."},
		{[]string{"remove", "pdf"}, "Removed docs:pdf."},
	} {
		if out := e.mustRun(tc.args...); !strings.HasPrefix(out, tc.want+"\n") {
			t.Errorf("%v should start with %q:\n%s", tc.args, tc.want, out)
		}
	}
}

func TestValidateJSONOnLoadError(t *testing.T) {
	e := newEnv(t)
	out, err := e.run("validate", t.TempDir(), "--json")
	if err == nil {
		t.Fatal("expected a failure")
	}
	var issues []struct{ Severity, Message string }
	if err := json.Unmarshal([]byte(out), &issues); err != nil || len(issues) != 1 || issues[0].Severity != "error" {
		t.Fatalf("validate --json: %v %+v\n%s", err, issues, out)
	}
}

func TestDoctorChecksClaudeSymlinks(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if out := e.mustRun("doctor"); strings.Contains(out, "symlinks") {
		t.Fatalf("no symlink check without ~/.claude:\n%s", out)
	}
	if err := os.MkdirAll(filepath.Join(e.paths.Home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := e.mustRun("doctor")
	if !strings.Contains(out, "! symlinks") || !strings.Contains(out, "review") {
		t.Fatalf("doctor should report missing symlinks:\n%s", out)
	}
	e.mustRun("sync")
	if out := e.mustRun("doctor"); !strings.Contains(out, "✓ symlinks") {
		t.Fatalf("sync should fix the symlinks:\n%s", out)
	}
}

func TestStatusShowsVersion(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	head := strings.TrimSpace(e.origin.Git("rev-parse", "HEAD"))
	for _, args := range [][]string{{"status"}, {"list"}} {
		if out := e.mustRun(args...); !strings.Contains(out, "VERSION") || !strings.Contains(out, head[:12]) {
			t.Errorf("%v should show the version:\n%s", args, out)
		}
	}
	var skills []struct{ Name, Commit string }
	if err := json.Unmarshal([]byte(e.mustRun("list", "--json")), &skills); err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if want := map[bool]string{true: head, false: ""}[s.Name != "pdf" && s.Name != "old"]; s.Commit != want {
			t.Errorf("%s commit = %q, want %q", s.Name, s.Commit, want)
		}
	}
}

func TestPlurals(t *testing.T) {
	e := newEnv(t)
	one := tu.NewOrigin(t, map[string]string{
		".claude-plugin/marketplace.json": tu.Marketplace("docs"),
		"skillcrew.yaml":                  "org: acme\nskills:\n  docs:pdf: required\n",
		tu.SkillPath("docs", "pdf"):       tu.SkillMD("pdf", "v1"),
	})
	if out := e.mustRun("validate", one.Dir); !strings.Contains(out, ": 1 skill.") {
		t.Errorf("validate: %s", out)
	}
	e.mustRun("init", one.Dir)
	if out := e.mustRun("doctor"); !strings.Contains(out, "1 team skill installed") {
		t.Errorf("doctor: %s", out)
	}
}

func TestListAvailableEmpty(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir, "--team", "backend")
	if out := e.mustRun("list", "--available"); out != "No skills available to install.\n" {
		t.Fatalf("list --available: %q", out)
	}
}

func TestVersionMatchesFlag(t *testing.T) {
	e := newEnv(t)
	if cmd, flag := e.mustRun("version"), e.mustRun("--version"); cmd != flag || cmd != "skillcrew test\n" {
		t.Fatalf("version %q, --version %q", cmd, flag)
	}
}

func TestJSONOutputs(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	for _, args := range [][]string{{"status"}, {"list"}, {"sync"}, {"doctor"}, {"validate", e.origin.Dir}} {
		out := e.mustRun(append(args, "--json")...)
		if !json.Valid([]byte(out)) {
			t.Errorf("%v --json: %s", args, out)
		}
	}
}

func TestNoticesAreShownOnce(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if err := os.WriteFile(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(2 * time.Minute)
	e.mustRun("sync", "--hook") // silent: the developer has not seen the notice yet
	if out := e.mustRun("status"); !strings.Contains(out, "backed up") {
		t.Fatalf("status must show the pending notice:\n%s", out)
	}
	if out := e.mustRun("status"); strings.Contains(out, "backed up") {
		t.Fatalf("a notice is shown once:\n%s", out)
	}
}

func TestDisableDeprecatedSkill(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.mustRun("install", "pdf")
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "docs:pdf: available", "docs:pdf: deprecated", 1)})
	e.origin.Commit("deprecate pdf")
	e.mustRun("sync")
	if !e.installed("pdf") {
		t.Fatal("a deprecated skill stays where it is installed")
	}
	e.mustRun("disable", "pdf")
	if e.installed("pdf") {
		t.Fatal("a deprecated skill can be disabled")
	}
}

// A preference change waits for a running sync instead of being skipped.
func TestPreferenceWaitsForRunningSync(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	unlock, ok, err := fsx.TryLock(e.paths.SyncLock())
	if err != nil || !ok {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		unlock()
	}()
	e.mustRun("install", "pdf")
	if !e.installed("pdf") {
		t.Fatal("the change must be applied once the running sync ends")
	}
}

func TestManualSyncFailsWhenLockIsHeld(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	unlock, _, _ := fsx.TryLock(e.paths.SyncLock())
	defer unlock()
	if _, err := e.run("sync"); err == nil || !strings.Contains(err.Error(), "sync") {
		t.Fatalf("a manual sync must report the busy lock, got %v", err)
	}
}

// init talks to the remote once to find and fetch the default branch; the
// first sync reuses what it fetched.
func TestInitFetchesOnce(t *testing.T) {
	e := newEnv(t)
	commands := tu.CountGit(t)
	e.mustRun("init", e.origin.Dir)
	if n := tu.Network(commands()); len(n) > 2 {
		t.Fatalf("init made %d network round trips, want 2 (ls-remote, fetch):\n%s", len(n), strings.Join(n, "\n"))
	}
	if !e.installed("review") {
		t.Fatal("init must install the skills")
	}
	e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "v2")})
	e.origin.Commit("v2")
	e.mustRun("init", e.origin.Dir) // an existing clone is fetched once
	if n := tu.Network(commands()); len(n) > 2 {
		t.Fatalf("init again made %d network round trips:\n%s", len(n), strings.Join(n, "\n"))
	}
	if b, _ := os.ReadFile(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md")); !strings.HasSuffix(string(b), "v2") {
		t.Fatalf("init again must install the latest registry: %q", b)
	}
}

// Agent hooks only fetch the branch: pinned tags already known are trusted.
func TestHookTrustsKnownTags(t *testing.T) {
	e := newEnv(t)
	e.origin.Git("tag", "v1")
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "security:review: required", "security:review: {status: required, ref: v1}", 1)})
	e.origin.Commit("pin review")
	e.mustRun("init", e.origin.Dir)
	e.now = e.now.Add(time.Hour)
	commands := tu.CountGit(t)
	e.mustRun("sync", "--hook")
	if n := tu.Network(commands()); len(n) != 2 {
		t.Fatalf("a hook sync must only fetch the branch, ran:\n%s", strings.Join(n, "\n"))
	}
	e.mustRun("sync")
	if n := tu.Network(commands()); len(n) != 3 {
		t.Fatalf("a manual sync also checks the pinned tags, ran:\n%s", strings.Join(n, "\n"))
	}
}

func TestInitFollowsAnotherBranch(t *testing.T) {
	e := newEnv(t)
	e.origin.Git("checkout", "-q", "-b", "next")
	e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "next")})
	e.origin.Commit("next")
	e.origin.Git("checkout", "-q", "main")
	e.mustRun("init", e.origin.Dir, "--ref", "next")
	if b, _ := os.ReadFile(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md")); !strings.HasSuffix(string(b), "next") {
		t.Fatalf("skills must come from the followed branch: %q", b)
	}
}

type countingWriter struct {
	bytes.Buffer
	writes int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	return w.Buffer.Write(p)
}

// A table is written at once, not cell by cell: each write to the terminal
// costs a system call.
func TestTablesAreBuffered(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	var buf bytes.Buffer
	out := &countingWriter{}
	app := e.app(&buf)
	app.Out = out
	cmd := app.Command()
	cmd.SetArgs([]string{"list"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "review") || out.writes > 3 {
		t.Fatalf("%d writes for the table:\n%s", out.writes, out.String())
	}
}

// The hook never waits for the network: it starts a detached sync and returns.
func TestHookStartsABackgroundSync(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.background = nil

	e.mustRun("sync", "--hook") // within the debounce window: nothing to start
	if len(e.background) != 0 {
		t.Fatalf("a debounced hook must not start a sync: %v", e.background)
	}

	e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "v2")})
	e.origin.Commit("v2")
	e.now = e.now.Add(2 * time.Minute)
	if out := e.mustRun("sync", "--hook=json"); strings.TrimSpace(out) != "{}" {
		t.Fatalf("json hook output must be {}, got %q", out)
	}
	if len(e.background) != 1 || strings.Join(e.background[0], " ") != "sync --background" {
		t.Fatalf("background commands: %v", e.background)
	}
	b, _ := os.ReadFile(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md"))
	if !strings.HasSuffix(string(b), "v2") {
		t.Fatal("the background sync must apply the update")
	}
}

func TestHookBeforeInitStartsNothing(t *testing.T) {
	e := newEnv(t)
	e.mustRun("sync", "--hook")
	if len(e.background) != 0 {
		t.Fatalf("background commands: %v", e.background)
	}
	if _, err := os.Stat(e.paths.Root()); !os.IsNotExist(err) {
		t.Fatal("a hook before init must not create state")
	}
}

func TestBackgroundSyncIsSilent(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.now = e.now.Add(2 * time.Minute)
	if out := e.mustRun("sync", "--background"); out != "" {
		t.Fatalf("background output must be empty, got %q", out)
	}
}
