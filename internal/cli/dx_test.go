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

	"github.com/yoanbernabeu/skillcrew/internal/state"
	tu "github.com/yoanbernabeu/skillcrew/internal/testutil"
)

// pin rewrites the governance of docs:pdf with a pin, in the working tree.
func (e *env) pin(pin string) {
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "docs:pdf: available", "docs:pdf: {status: available, "+pin+"}", 1)})
}

func TestValidateChecksPinsInGit(t *testing.T) {
	e := newEnv(t)
	head := e.origin.Git("rev-parse", "HEAD")
	e.origin.Git("tag", "v1")
	e.origin.Git("branch", "beta")
	for _, tc := range []struct{ pin, want string }{
		{"ref: v1", ""},
		{"sha: " + head, ""},
		{"ref: beta", "branch"},
		{"ref: v9", "does not exist"},
		{"sha: " + strings.Repeat("0", 40), "does not exist"},
	} {
		e.pin(tc.pin)
		out, err := e.run("validate", e.origin.Dir)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v\n%s", tc.pin, err, out)
			}
			continue
		}
		if err == nil || !strings.Contains(out, "error: ") || !strings.Contains(out, tc.want) {
			t.Errorf("%s: expected an error about %q, got %v\n%s", tc.pin, tc.want, err, out)
		}
	}
}

func TestValidatePinsOutsideGit(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	for name, content := range map[string]string{
		".claude-plugin/marketplace.json": tu.Marketplace("docs"),
		"skillcrew.yaml":                  "org: acme\nskills:\n  docs:pdf: {status: available, ref: v1}\n",
		tu.SkillPath("docs", "pdf"):       tu.SkillMD("pdf", "v1"),
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.mustRun("validate", dir) // not a git checkout: the format is all that can be checked
}

func TestValidateJSONNamesSkills(t *testing.T) {
	e := newEnv(t)
	e.origin.Write(map[string]string{"skillcrew.yaml": "org: acme\nskills:\n  ghost:skill: required\n"})
	out, _ := e.run("validate", e.origin.Dir, "--json")
	var issues []map[string]string
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range issues {
		found = found || i["skill"] == "ghost:skill"
	}
	if !found {
		t.Fatalf("no issue names ghost:skill: %s", out)
	}
	if strings.Contains(out, `"skill": ""`) {
		t.Fatalf("an unknown skill must be omitted: %s", out)
	}
}

func TestRegistryInitConvertReportsIssues(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	for name, content := range map[string]string{
		".claude-plugin/marketplace.json":  `{"name":"Acme Tools","plugins":[{"name":"security","source":"./plugins/security"},{"name":"ghost","source":"./plugins/ghost"}]}`,
		tu.SkillPath("security", "review"): tu.SkillMD("review", "v1"),
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := e.run("registry", "init", dir)
	if err == nil || !strings.Contains(out, "does not exist") {
		t.Fatalf("the invalid result must be reported, got %v\n%s", err, out)
	}
	for _, want := range []string{"marketplace name", `"Acme Tools"`, "acme-tools"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should mention %s:\n%s", want, out)
		}
	}
}

func TestRegistryInitNamesTheOrg(t *testing.T) {
	e := newEnv(t)
	out := e.mustRun("registry", "init", filepath.Join(t.TempDir(), "acme-skills"))
	if !strings.Contains(out, "acme-skills") || !strings.Contains(out, "directory name") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestDoctorReportsOfflineSync(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	st := state.State{
		LastSync: e.now, FetchFailedAt: e.now,
		Warnings: []string{"offline: using the last fetched registry (git fetch: fatal: could not read Username)"},
	}
	if err := st.Save(e.paths.State()); err != nil {
		t.Fatal(err)
	}
	out := e.mustRun("doctor")
	for _, want := range []string{"! fetch", "offline", "git -C " + e.paths.Registry() + " fetch"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor should mention %q:\n%s", want, out)
		}
	}
}

func TestSyncOffline(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	gone := e.origin.Dir + "-gone"
	if err := os.Rename(e.origin.Dir, gone); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(gone, e.origin.Dir) })
	out, _ := e.run("sync")
	if strings.Contains(out, "up to date") || !strings.Contains(out, "Using the last fetched registry (offline)") {
		t.Fatalf("sync output:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "fatal") || strings.HasPrefix(line, "Please make sure") {
			t.Fatalf("git errors must stay on one line:\n%s", out)
		}
	}
}

func TestTeamList(t *testing.T) {
	e := newEnv(t)
	e.origin.Write(map[string]string{"skillcrew.yaml": governance + "  frontend:\n    skills: {}\n"})
	e.origin.Commit("frontend")
	out := e.mustRun("init", e.origin.Dir)
	if !strings.Contains(out, "skillcrew team join") || !strings.Contains(out, "backend, frontend") {
		t.Errorf("init should hint at teams:\n%s", out)
	}
	e.mustRun("team", "join", "backend")
	out = e.mustRun("team", "list")
	if !strings.Contains(out, "backend") || !strings.Contains(out, "frontend") {
		t.Fatalf("team list:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "frontend") == strings.Contains(line, "joined") && strings.Contains(line, "end") {
			t.Errorf("only backend is joined: %q", line)
		}
	}
	var teams []struct {
		Name   string `json:"name"`
		Joined bool   `json:"joined"`
	}
	if err := json.Unmarshal([]byte(e.mustRun("team", "list", "--json")), &teams); err != nil {
		t.Fatal(err)
	}
	if len(teams) != 2 || teams[0].Name != "backend" || !teams[0].Joined || teams[1].Joined {
		t.Fatalf("team list --json: %+v", teams)
	}
	if out := e.mustRun("status"); !strings.Contains(out, "Teams         backend\n") {
		t.Errorf("teams line:\n%s", out)
	}
	if _, err := e.run("team", "join", "ghost"); err == nil || !strings.Contains(err.Error(), "backend, frontend") {
		t.Errorf("unknown team must list the teams, got %v", err)
	}
	if _, err := newEnv(t).run("init", e.origin.Dir, "--team", "ghost"); err == nil || !strings.Contains(err.Error(), "backend, frontend") {
		t.Errorf("unknown team must list the teams, got %v", err)
	}
}

func TestNoTeamHintWhenJoined(t *testing.T) {
	e := newEnv(t)
	if out := e.mustRun("init", e.origin.Dir, "--team", "backend"); strings.Contains(out, "team join") {
		t.Fatalf("no hint once a team is joined:\n%s", out)
	}
}

func TestInitWithoutTriggers(t *testing.T) {
	e := newEnv(t)
	e.claude.detected = false
	out := e.mustRun("init", e.origin.Dir, "--no-timer")
	for _, want := range []string{"no agent", "timer was not installed", "Nothing will sync"} {
		if !strings.Contains(out, want) {
			t.Errorf("init should say %q:\n%s", want, out)
		}
	}
	out, err := e.run("doctor")
	if err == nil || !strings.Contains(out, "nothing triggers syncs") {
		t.Fatalf("doctor must fail, got %v\n%s", err, out)
	}
}

func TestInitEndHints(t *testing.T) {
	e := newEnv(t)
	out := e.mustRun("init", e.origin.Dir)
	for _, want := range []string{"new agent session", "skillcrew list --available"} {
		if !strings.Contains(out, want) {
			t.Errorf("init should mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Nothing will sync") {
		t.Errorf("hooks and timer are installed:\n%s", out)
	}
}

func TestUninstallBeforeInit(t *testing.T) {
	e := newEnv(t)
	out, err := e.run("uninstall")
	if err != nil || !strings.Contains(out, "Skillcrew is not initialized") {
		t.Fatalf("got %v\n%s", err, out)
	}
}

func TestUninstallAborted(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	var out bytes.Buffer
	app := e.app(&out)
	app.In = strings.NewReader("n\n")
	cmd := app.Command()
	cmd.SetArgs([]string{"uninstall"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("answering no is not an error: %v", err)
	}
	for _, want := range []string{"review", "secrets", "claude-code", "timer", "Aborted."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output should mention %q:\n%s", want, out.String())
		}
	}
	if !e.installed("review") {
		t.Fatal("nothing must be removed")
	}
}

func TestUninstallRestoresPersonalSkills(t *testing.T) {
	e := newEnv(t)
	mine := filepath.Join(e.paths.AgentsSkills(), "review")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mine, "SKILL.md"), []byte(tu.SkillMD("review", "mine")), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustRun("init", e.origin.Dir)
	out := e.mustRun("uninstall", "--yes")
	data, err := os.ReadFile(filepath.Join(mine, "SKILL.md"))
	if err != nil || !strings.HasSuffix(string(data), "mine") {
		t.Fatalf("personal skill not restored: %q %v\n%s", data, err, out)
	}
	if !strings.Contains(out, "Restored") || !strings.Contains(out, mine) {
		t.Errorf("the restore must be reported:\n%s", out)
	}
}

func TestUninstallClaimsNoBackups(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if out := e.mustRun("uninstall", "--yes"); strings.Contains(out, "kept") {
		t.Fatalf("there are no backups:\n%s", out)
	}
	e = newEnv(t)
	e.mustRun("init", e.origin.Dir)
	if err := os.WriteFile(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := e.mustRun("uninstall", "--yes"); !strings.Contains(out, "kept in "+e.paths.Backups()) {
		t.Fatalf("backups of local changes must be pointed out:\n%s", out)
	}
}

func TestTableColumnsIgnoreDeprecation(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	out := e.mustRun("list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	header := lines[0]
	if got, want := strings.Index(header, "STATUS")-strings.Index(header, "ID"), len("security:secrets")+2; got != want {
		t.Errorf("the ID column is %d wide, want %d:\n%s", got, want, out)
	}
	for _, l := range lines {
		if strings.TrimRight(l, " \t") != l {
			t.Errorf("trailing blanks: %q", l)
		}
	}
	if !strings.Contains(out, "Use docs:pdf.") {
		t.Errorf("the deprecation must still be shown:\n%s", out)
	}
}

func TestSyncBeforeInit(t *testing.T) {
	e := newEnv(t)
	out, err := e.run("sync")
	if err == nil || strings.Contains(out, "up to date") || strings.Contains(out, "Synced") {
		t.Fatalf("got %v\n%s", err, out)
	}
	if _, err := os.Stat(e.paths.Root()); !os.IsNotExist(err) {
		t.Error("sync before init must not create ~/.skillcrew")
	}
}

func TestIdempotentPreferences(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.mustRun("install", "pdf")
	e.mustRun("disable", "secrets")
	e.mustRun("team", "join", "backend")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"install", "pdf"}, "docs:pdf is already installed"},
		{[]string{"disable", "secrets"}, "security:secrets is already disabled"},
		{[]string{"team", "join", "backend"}, "already a member of team backend"},
		{[]string{"install", "review"}, "security:review is already installed (required)"},
	} {
		if out := e.mustRun(tc.args...); !strings.Contains(out, tc.want) {
			t.Errorf("%v should say %q:\n%s", tc.args, tc.want, out)
		}
	}
}

func TestPreferenceErrorsGiveNextSteps(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.mustRun("install", "pdf")
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"remove", "secrets"}, []string{"recommended", `skillcrew disable security:secrets`}},
		{[]string{"disable", "pdf"}, []string{"available", `skillcrew remove docs:pdf`}},
		{[]string{"disable", "review"}, []string{"required"}},
		{[]string{"install", "old"}, []string{"deprecated", "docs:pdf"}},
		{[]string{"install", "revew"}, []string{`"review"`, "skillcrew list --available"}},
		{[]string{"enable", "nope:nope"}, []string{"unknown skill", "skillcrew list"}},
	} {
		_, err := e.run(tc.args...)
		if err == nil {
			t.Errorf("%v must fail", tc.args)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%v: %q should mention %q", tc.args, err, w)
			}
		}
	}
}

func TestEnableDeprecatedReinstalls(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.mustRun("install", "pdf")
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "docs:pdf: available", "docs:pdf: deprecated", 1)})
	e.origin.Commit("deprecate pdf")
	e.mustRun("sync")
	e.mustRun("disable", "pdf")
	if out := e.mustRun("list"); !strings.Contains(lineOf(out, "docs:pdf"), "disabled") {
		t.Errorf("list must show the disabled state:\n%s", out)
	}
	e.mustRun("enable", "pdf")
	if !e.installed("pdf") {
		t.Fatal("enabling a disabled deprecated skill must reinstall it")
	}
}

func lineOf(out, s string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

func TestListShowsDisabled(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.mustRun("disable", "secrets")
	if l := lineOf(e.mustRun("list"), "security:secrets"); !strings.Contains(l, "disabled") {
		t.Errorf("list: %q", l)
	}
}

func TestJSONOmitsEmptyFields(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	var skills []map[string]any
	if err := json.Unmarshal([]byte(e.mustRun("list", "--json")), &skills); err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s["id"] == "docs:pdf" {
			if _, ok := s["state"]; ok {
				t.Errorf("state must be omitted: %v", s)
			}
			if _, ok := s["commit"]; ok {
				t.Errorf("commit must be omitted: %v", s)
			}
		}
	}
	if out := e.mustRun("status", "--json"); !strings.Contains(out, `"teams": []`) {
		t.Errorf("teams must be []:\n%s", out)
	}
}

func TestSyncExplainsRemovals(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "security:secrets: recommended", "security:secrets: blocked", 1)})
	e.origin.Commit("block")
	if out := e.mustRun("sync"); !strings.Contains(out, "secrets (removed: blocked by your organization)") {
		t.Errorf("sync output:\n%s", out)
	}
	e.origin.Git("rm", "-q", "-r", "plugins/security/skills/review")
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "  security:review: required\n", "", 1)})
	e.origin.Commit("drop review")
	if out := e.mustRun("sync"); !strings.Contains(out, "review (removed: no longer distributed by the registry)") {
		t.Errorf("sync output:\n%s", out)
	}
}

func TestHelpTexts(t *testing.T) {
	e := newEnv(t)
	if out := e.mustRun("disable", "--help"); !strings.Contains(out, "deprecated") {
		t.Errorf("disable help:\n%s", out)
	}
	if out := e.mustRun("install", "--help"); !strings.Contains(out, "plugin:skill id or installed name") {
		t.Errorf("install help:\n%s", out)
	}
	e.mustRun("init", e.origin.Dir)
	if _, err := e.run("install"); err == nil || !strings.Contains(err.Error(), "Usage: skillcrew install <skill>") {
		t.Errorf("missing argument: %v", err)
	}
	var out, errOut bytes.Buffer
	app := e.app(&out)
	app.Err = &errOut
	app.Args = []string{"instll", "pdf"} // a typo on purpose
	if code := app.Execute(context.Background()); code != 1 || !strings.Contains(errOut.String(), "skillcrew --help") {
		t.Errorf("unknown command: %d %q", code, errOut.String())
	}
	app.Args = []string{"team", "jion", "backend"}
	errOut.Reset()
	if code := app.Execute(context.Background()); code != 1 || !strings.Contains(errOut.String(), "skillcrew team --help") {
		t.Errorf("unknown subcommand: %d %q", code, errOut.String())
	}
}

func TestInitSameRegistryAsFileURL(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", e.origin.Dir)
	e.mustRun("init", "file://"+e.origin.Dir)
	e.mustRun("init", e.origin.Dir+"/")
}

func TestInitStoresAbsoluteLocalPaths(t *testing.T) {
	e := newEnv(t)
	t.Chdir(filepath.Dir(e.origin.Dir))
	e.mustRun("init", "./"+filepath.Base(e.origin.Dir))
	if cfg := e.config(); !filepath.IsAbs(cfg.Registry) {
		t.Fatalf("registry = %q", cfg.Registry)
	}
	e.now = e.now.Add(time.Hour)
	e.mustRun("sync")
}

func TestRegistryInitCI(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(t.TempDir(), "acme-skills")
	out := e.mustRun("registry", "init", dir, "--ci", "gitlab")
	if !strings.Contains(out, "+ .gitlab-ci.yml") || strings.Contains(out, ".github") {
		t.Fatalf("output:\n%s", out)
	}
	out, err := e.run("registry", "init", t.TempDir(), "--ci", "jenkins")
	if err == nil || !strings.Contains(out+err.Error(), "gitlab") {
		t.Fatalf("an unknown CI must be refused, got %v\n%s", err, out)
	}
}
