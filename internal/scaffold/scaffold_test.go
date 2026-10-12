package scaffold_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/scaffold"
)

const fixtureMarketplace = `{
  "name": "acme-tools",
  "owner": {"name": "Acme"},
  "metadata": {"pluginRoot": "./plugins"},
  "plugins": [
    {"name": "security", "source": "security"},
    {"name": "docs", "source": "./extra/docs"},
    {"name": "remote", "source": {"source": "github", "repo": "acme/remote"}}
  ]
}
`

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillMD(name string) string {
	return "---\nname: " + name + "\ndescription: Does " + name + ".\n---\n\n# " + name + "\n"
}

func marketplaceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, ".claude-plugin/marketplace.json", fixtureMarketplace)
	writeFile(t, dir, "plugins/security/skills/review/SKILL.md", skillMD("review"))
	writeFile(t, dir, "plugins/security/skills/secrets/SKILL.md", skillMD("secrets"))
	writeFile(t, dir, "extra/docs/skills/pdf/SKILL.md", skillMD("pdf"))
	return dir
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// assertValid checks the key acceptance criterion: the result loads and validates.
func assertValid(t *testing.T, dir string) *registry.Registry {
	t.Helper()
	fsys := os.DirFS(dir)
	reg, err := registry.Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, issue := range reg.Validate(fsys) {
		if issue.Severity == registry.Error {
			t.Errorf("validation: %s", issue)
		}
	}
	return reg
}

func listFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			files[filepath.ToSlash(rel)] = "symlink to " + target
			return err
		}
		data, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestInitConvert(t *testing.T) {
	dir := marketplaceDir(t)
	res, err := scaffold.Init(dir, scaffold.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Converted {
		t.Error("expected Converted")
	}
	if want := []string{"skillcrew.yaml"}; !reflect.DeepEqual(res.Created, want) {
		t.Errorf("Created = %v, want %v", res.Created, want)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "remote") {
		t.Errorf("Warnings = %v, want one about the remote plugin", res.Warnings)
	}
	if got := readFile(t, dir, ".claude-plugin/marketplace.json"); got != fixtureMarketplace {
		t.Error("marketplace.json was modified")
	}

	reg := assertValid(t, dir)
	g := reg.Governance
	if g.Org != "acme-tools" || g.Default != registry.Available {
		t.Errorf("org/default = %q/%q", g.Org, g.Default)
	}
	ids := []string{"docs:pdf", "security:review", "security:secrets"}
	if len(g.Skills) != len(ids) {
		t.Errorf("skills = %v, want %v", g.Skills, ids)
	}
	for _, id := range ids {
		if g.Skills[id].Status != registry.Available {
			t.Errorf("%s status = %q, want available", id, g.Skills[id].Status)
		}
	}
	if len(g.Teams) != 0 {
		t.Errorf("teams should be a commented example, got %v", g.Teams)
	}

	yaml := readFile(t, dir, "skillcrew.yaml")
	for _, s := range []string{"required", "recommended", "deprecated", "blocked", "plugin:skill", "# teams:"} {
		if !strings.Contains(yaml, s) {
			t.Errorf("skillcrew.yaml lacks %q", s)
		}
	}
	section := yaml[strings.Index(yaml, "\nskills:"):]
	if strings.Index(section, "docs:pdf") > strings.Index(section, "security:review") {
		t.Error("skills are not sorted")
	}
}

func TestInitConvertOrgOverride(t *testing.T) {
	dir := marketplaceDir(t)
	if _, err := scaffold.Init(dir, scaffold.Options{Org: "Globex Corp"}); err != nil {
		t.Fatal(err)
	}
	if reg := assertValid(t, dir); reg.Governance.Org != "globex-corp" {
		t.Errorf("org = %q, want globex-corp", reg.Governance.Org)
	}
}

func TestInitExistingGovernance(t *testing.T) {
	dir := marketplaceDir(t)
	writeFile(t, dir, "skillcrew.yaml", "org: old\n")

	_, err := scaffold.Init(dir, scaffold.Options{})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a mention of --force", err)
	}
	if got := readFile(t, dir, "skillcrew.yaml"); got != "org: old\n" {
		t.Error("skillcrew.yaml was overwritten without Force")
	}

	res, err := scaffold.Init(dir, scaffold.Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Converted || !reflect.DeepEqual(res.Created, []string{"skillcrew.yaml"}) {
		t.Errorf("unexpected result %+v", res)
	}
	if reg := assertValid(t, dir); reg.Governance.Org != "acme-tools" {
		t.Errorf("org = %q after Force", reg.Governance.Org)
	}
}

func TestInitExistingGovernanceNewMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "skillcrew.yaml", "org: old\n")
	if _, err := scaffold.Init(dir, scaffold.Options{}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a mention of --force", err)
	}
}

func TestInitNew(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Acme Skills")
	res, err := scaffold.Init(dir, scaffold.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		".claude-plugin/marketplace.json",
		".github/workflows/validate.yml",
		"AGENTS.md",
		"CLAUDE.md",
		"README.md",
		"plugins/example/.claude-plugin/plugin.json",
		"plugins/example/skills/hello-world/SKILL.md",
		"skillcrew.yaml",
	}
	if res.Converted || !reflect.DeepEqual(res.Created, want) || len(res.Warnings) != 0 {
		t.Errorf("unexpected result %+v", res)
	}

	reg := assertValid(t, dir)
	if reg.Name != "acme-skills" || reg.Governance.Org != "acme-skills" {
		t.Errorf("name/org = %q/%q", reg.Name, reg.Governance.Org)
	}
	if got := reg.Governance.Skills["example:hello-world"].Status; got != registry.Recommended {
		t.Errorf("example:hello-world = %q, want recommended", got)
	}

	var m struct {
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
		Plugins []struct {
			Name, Source, Description string
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(readFile(t, dir, ".claude-plugin/marketplace.json")), &m); err != nil {
		t.Fatal(err)
	}
	if m.Owner.Name != "acme-skills" || len(m.Plugins) != 1 || m.Plugins[0].Source != "./plugins/example" || m.Plugins[0].Description == "" {
		t.Errorf("unexpected marketplace %+v", m)
	}
	var p struct{ Name, Description, Version string }
	if err := json.Unmarshal([]byte(readFile(t, dir, "plugins/example/.claude-plugin/plugin.json")), &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "example" || p.Version != "0.1.0" || p.Description == "" {
		t.Errorf("unexpected plugin %+v", p)
	}

	readme := readFile(t, dir, "README.md")
	for _, s := range []string{"skillcrew init", "https://github.com/yoanbernabeu/skillcrew", "required", "blocked"} {
		if !strings.Contains(readme, s) {
			t.Errorf("README.md lacks %q", s)
		}
	}
	if !strings.Contains(readme, "skillcrew skill new") {
		t.Error("README.md should show how to add a skill with skillcrew skill new")
	}
	agents := readFile(t, dir, "AGENTS.md")
	for _, s := range []string{"skillcrew skill new", "skillcrew validate", "marketplace.json", "skillcrew.yaml", "required", "blocked", "teams"} {
		if !strings.Contains(agents, s) {
			t.Errorf("AGENTS.md lacks %q", s)
		}
	}
	if got := readFile(t, dir, "CLAUDE.md"); got != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md = %q, want it to import AGENTS.md", got)
	}
	wf := readFile(t, dir, ".github/workflows/validate.yml")
	for _, s := range []string{
		"pull_request",
		"permissions:\n  contents: read",
		"persist-credentials: false",
		"go run github.com/yoanbernabeu/skillcrew/cmd/skillcrew@latest validate",
	} {
		if !strings.Contains(wf, s) {
			t.Errorf("validate.yml lacks %q", s)
		}
	}
	assertActionsPinned(t, wf)

	info, err := os.Stat(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("README.md mode = %v", info.Mode().Perm())
	}
}

func TestInitNewOptions(t *testing.T) {
	dir := t.TempDir()
	if _, err := scaffold.Init(dir, scaffold.Options{Org: "globex", Owner: "Globex Platform Team"}); err != nil {
		t.Fatal(err)
	}
	reg := assertValid(t, dir)
	if reg.Name != "globex" || reg.Governance.Org != "globex" {
		t.Errorf("name/org = %q/%q", reg.Name, reg.Governance.Org)
	}
	if !strings.Contains(readFile(t, dir, ".claude-plugin/marketplace.json"), `"Globex Platform Team"`) {
		t.Error("owner not written")
	}
}

func TestInitNewRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "mine\n")
	before := listFiles(t, dir)

	_, err := scaffold.Init(dir, scaffold.Options{Force: true})
	if err == nil || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("err = %v, want one naming README.md", err)
	}
	if after := listFiles(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("directory changed: %v", after)
	}
}

func TestInitNewKeepsAgentInstructions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "CLAUDE.md", "my notes\n")
	res, err := scaffold.Init(dir, scaffold.Options{Org: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, dir, "CLAUDE.md") != "my notes\n" || readFile(t, dir, "AGENTS.md") == "" {
		t.Error("CLAUDE.md must be kept and AGENTS.md written")
	}
	notice := strings.Join(res.Notices, "\n")
	if !strings.Contains(notice, "CLAUDE.md") || !strings.Contains(notice, "@AGENTS.md") {
		t.Errorf("notices = %v", res.Notices)
	}
}

func TestSanitizeOrg(t *testing.T) {
	cases := map[string]string{
		"acme":             "acme",
		"Acme Corp":        "acme-corp",
		"  --My_Org!!--  ": "my-org",
		"a..b//c":          "a-b-c",
		"!!!":              "my-org",
		"":                 "my-org",
		"claude":           "team-claude",
		"Anthropic":        "team-anthropic",
		"claude-code":      "team-claude-code",
		"claude-tools":     "team-claude-tools",
		"anthropic-skills": "team-anthropic-skills",
		"claudette":        "claudette",
		"my-claude":        "my-claude",
	}
	for in, want := range cases {
		if got := scaffold.SanitizeOrg(in); got != want {
			t.Errorf("SanitizeOrg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInitRefusesNonRegularGovernance(t *testing.T) {
	cases := map[string]func(t *testing.T, dir, outside string){
		"dangling symlink": func(t *testing.T, dir, outside string) {
			if err := os.Symlink(filepath.Join(outside, ".bashrc"), filepath.Join(dir, "skillcrew.yaml")); err != nil {
				t.Fatal(err)
			}
		},
		"symlink to a file": func(t *testing.T, dir, outside string) {
			writeFile(t, outside, ".bashrc", "mine\n")
			if err := os.Symlink(filepath.Join(outside, ".bashrc"), filepath.Join(dir, "skillcrew.yaml")); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(t *testing.T, dir, _ string) {
			if err := os.Mkdir(filepath.Join(dir, "skillcrew.yaml"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		for _, force := range []bool{false, true} {
			for _, convert := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/force=%v/convert=%v", name, force, convert), func(t *testing.T) {
					dir, outside := t.TempDir(), t.TempDir()
					if convert {
						dir = marketplaceDir(t)
					}
					setup(t, dir, outside)
					before, beforeOutside := listFiles(t, dir), listFiles(t, outside)

					if _, err := scaffold.Init(dir, scaffold.Options{Force: force}); err == nil || !strings.Contains(err.Error(), "skillcrew.yaml") {
						t.Fatalf("err = %v, want one naming skillcrew.yaml", err)
					}
					if after := listFiles(t, outside); !reflect.DeepEqual(beforeOutside, after) {
						t.Errorf("file written outside dir: %v", after)
					}
					if after := listFiles(t, dir); !reflect.DeepEqual(before, after) {
						t.Errorf("directory changed: %v", after)
					}
				})
			}
		}
	}
}

func TestInitForceReplacesRegularFile(t *testing.T) {
	dir := marketplaceDir(t)
	writeFile(t, dir, "skillcrew.yaml", "org: old\n")
	if err := os.Chmod(filepath.Join(dir, "skillcrew.yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := scaffold.Init(dir, scaffold.Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	assertValid(t, dir)
	info, err := os.Lstat(filepath.Join(dir, "skillcrew.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want a regular 0644 file", info.Mode())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") || strings.HasPrefix(e.Name(), ".skillcrew") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestInitNewRefusesSymlinkedDirs(t *testing.T) {
	for _, link := range []string{"plugins", "plugins/example", ".claude-plugin", ".github/workflows"} {
		t.Run(link, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			p := filepath.Join(dir, filepath.FromSlash(link))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, p); err != nil {
				t.Fatal(err)
			}
			if _, err := scaffold.Init(dir, scaffold.Options{}); err == nil || !strings.Contains(err.Error(), link) {
				t.Fatalf("err = %v, want one naming %s", err, link)
			}
			if after := listFiles(t, outside); len(after) != 0 {
				t.Errorf("files written outside dir: %v", after)
			}
		})
	}
}

func TestInitConvertSkipsUnsafeIDs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".claude-plugin/marketplace.json", `{
  "name": "acme",
  "owner": {"name": "Acme"},
  "plugins": [
    {"name": "security", "source": "./plugins/security"},
    {"name": "bad: plugin", "source": "./plugins/bad"}
  ]
}
`)
	writeFile(t, dir, "plugins/security/skills/review/SKILL.md", skillMD("review"))
	writeFile(t, dir, "plugins/security/skills/$(touch PWNED)/SKILL.md", skillMD("pwned"))
	writeFile(t, dir, "plugins/security/skills/Upper_Case/SKILL.md", skillMD("upper"))
	writeFile(t, dir, "plugins/bad/skills/ok/SKILL.md", skillMD("ok"))

	res, err := scaffold.Init(dir, scaffold.Options{})
	if err != nil {
		t.Fatal(err)
	}
	yaml := readFile(t, dir, "skillcrew.yaml")
	for _, s := range []string{"PWNED", "Upper_Case", "bad: plugin"} {
		if strings.Contains(yaml, s) {
			t.Errorf("skillcrew.yaml contains unsafe id %q:\n%s", s, yaml)
		}
		found := false
		for _, w := range res.Warnings {
			found = found || strings.Contains(w, s)
		}
		if !found {
			t.Errorf("no warning about %q: %v", s, res.Warnings)
		}
	}
	reg, err := registry.Load(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"security:review"}; !reflect.DeepEqual(sortedIDs(reg.Governance.Skills), want) {
		t.Errorf("skills = %v, want %v", reg.Governance.Skills, want)
	}
}

func sortedIDs[V any](m map[string]V) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// assertActionsPinned checks that every action in a workflow is pinned to a
// full commit SHA, with the release it stands for in a comment.
func assertActionsPinned(t *testing.T, wf string) {
	t.Helper()
	pinned := regexp.MustCompile(`^uses: [\w.-]+/[\w.-]+@[0-9a-f]{40} # v\d+\.\d+\.\d+$`)
	n := 0
	for _, line := range strings.Split(wf, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if !strings.HasPrefix(line, "uses:") {
			continue
		}
		n++
		if !pinned.MatchString(line) {
			t.Errorf("action not pinned to a commit SHA: %q", line)
		}
	}
	if n == 0 {
		t.Error("workflow uses no action")
	}
}

func TestInitNewWorkflowVersion(t *testing.T) {
	for _, tc := range []struct {
		version, want string
	}{
		{"", "@latest validate"},
		{"dev", "@latest validate"},
		{"0.1.0", "@latest validate"},
		{"v0.1.0-rc.1", "@latest validate"},
		{"v1.2.3-0.20261011120000-abcdef123456+dirty", "@latest validate"},
		{"v0.1.0; rm -rf /", "@latest validate"},
		{"v0.1.0", "@v0.1.0 validate"},
		{"v12.34.56", "@v12.34.56 validate"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := scaffold.Init(dir, scaffold.Options{Version: tc.version}); err != nil {
				t.Fatal(err)
			}
			wf := readFile(t, dir, ".github/workflows/validate.yml")
			want := "go run github.com/yoanbernabeu/skillcrew/cmd/skillcrew" + tc.want
			if !strings.Contains(wf, want) {
				t.Errorf("validate.yml lacks %q:\n%s", want, wf)
			}
		})
	}
}

func TestInitConvertAliasesCollisions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name":"acme","plugins":[
  {"name":"security","source":"./plugins/security"},
  {"name":"frontend","source":"./plugins/frontend"}]}`)
	writeFile(t, dir, "plugins/security/skills/review/SKILL.md", skillMD("review"))
	writeFile(t, dir, "plugins/frontend/skills/review/SKILL.md", skillMD("review"))
	writeFile(t, dir, "plugins/frontend/skills/security-review/SKILL.md", skillMD("security-review"))
	res, err := scaffold.Init(dir, scaffold.Options{})
	if err != nil {
		t.Fatal(err)
	}
	reg := assertValid(t, dir)
	aliases := map[string]string{}
	for id, r := range reg.Governance.Skills {
		aliases[id] = r.InstallAs
	}
	if aliases["frontend:review"] != "frontend-review" || aliases["security:review"] == "" || aliases["security:review"] == "security-review" {
		t.Errorf("aliases = %v", aliases)
	}
	if aliases["frontend:security-review"] != "" {
		t.Errorf("a skill without collision keeps its name: %v", aliases)
	}
	if w := strings.Join(res.Warnings, "\n"); !strings.Contains(w, "install_as") {
		t.Errorf("the aliases must be reported: %v", res.Warnings)
	}
}

func TestInitConvertBlocksInvalidSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name":"acme","plugins":[{"name":"security","source":"./plugins/security"}]}`)
	writeFile(t, dir, "plugins/security/skills/review/SKILL.md", skillMD("review"))
	writeFile(t, dir, "plugins/security/skills/broken/SKILL.md", "---\nname: other\n---\n")
	res, err := scaffold.Init(dir, scaffold.Options{})
	if err != nil {
		t.Fatal(err)
	}
	reg := assertValid(t, dir)
	if got := reg.Governance.Skills["security:broken"].Status; got != registry.Blocked {
		t.Errorf("an invalid skill must be left out (blocked), got %q", got)
	}
	if w := strings.Join(res.Warnings, "\n"); !strings.Contains(w, "security:broken") {
		t.Errorf("no warning about the invalid skill: %v", res.Warnings)
	}
	if !strings.Contains(readFile(t, dir, "skillcrew.yaml"), "# security:broken") {
		t.Error("skillcrew.yaml should explain why the skill is blocked")
	}
}

func TestInitConvertReportsValidation(t *testing.T) {
	dir := marketplaceDir(t)
	writeFile(t, dir, "plugins/unlisted/skills/x/SKILL.md", skillMD("x"))
	res, err := scaffold.Init(dir, scaffold.Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range res.Issues {
		found = found || strings.Contains(i.Message, "plugins/unlisted")
		if strings.Contains(i.Message, "remote") {
			t.Errorf("issues repeat the warnings: %v", i)
		}
	}
	if !found {
		t.Errorf("validation issues not reported: %+v", res.Issues)
	}
}

func TestInitConvertMissingPluginIsAnIssue(t *testing.T) {
	dir := marketplaceDir(t)
	writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name":"acme","plugins":[{"name":"ghost","source":"./plugins/ghost"}]}`)
	res, err := scaffold.Init(dir, scaffold.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) == 0 || res.Issues[0].Severity != registry.Error {
		t.Fatalf("issues = %+v", res.Issues)
	}
}

func TestGeneratedTexts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme-skills")
	if _, err := scaffold.Init(dir, scaffold.Options{}); err != nil {
		t.Fatal(err)
	}
	yaml := readFile(t, dir, "skillcrew.yaml")
	for _, s := range []string{"never installed on new machines", "can disable"} {
		if !strings.Contains(yaml, s) {
			t.Errorf("the deprecated comment lacks %q:\n%s", s, yaml)
		}
	}
	if strings.Contains(yaml, "still installed,") {
		t.Errorf("deprecated skills are not installed everywhere:\n%s", yaml)
	}
	readme := readFile(t, dir, "README.md")
	if title, _, _ := strings.Cut(readme, "\n"); strings.HasSuffix(title, "skills skills") || !strings.Contains(title, "acme-skills") {
		t.Errorf("README title: %q", title)
	}
	if strings.Contains(readme, "Still installed") {
		t.Error("README misdescribes deprecated")
	}
	if !strings.Contains(readFile(t, dir, ".github/workflows/validate.yml"), "fetch-depth: 0") {
		t.Error("the workflow needs tags and history to check pins")
	}
}

func TestInitReportsOrgName(t *testing.T) {
	for _, tc := range []struct {
		org, dirName, want string
		convert            bool
	}{
		{"", "Acme Skills", "directory", false},
		{"Globex Corp", "x", `"Globex Corp"`, false},
		{"globex", "x", "", false},
		{"", "x", "marketplace", true},
	} {
		dir := filepath.Join(t.TempDir(), tc.dirName)
		if tc.convert {
			dir = marketplaceDir(t)
		}
		res, err := scaffold.Init(dir, scaffold.Options{Org: tc.org})
		if err != nil {
			t.Fatal(err)
		}
		notes := strings.Join(res.Notices, "\n")
		if tc.want == "" && notes != "" || !strings.Contains(notes, tc.want) {
			t.Errorf("%+v: notices %q", tc, notes)
		}
	}
}

func TestInitRefusalsGiveANextStep(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "mine\n")
	if _, err := scaffold.Init(dir, scaffold.Options{}); err == nil || !strings.Contains(err.Error(), "empty directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestInitNewCI(t *testing.T) {
	for _, tc := range []struct {
		ci      string
		file    string // CI file created, if any
		request string // what the README calls a change
		runs    bool   // the README and AGENTS.md say CI runs validate
	}{
		{"", ".github/workflows/validate.yml", "pull request", true},
		{"github", ".github/workflows/validate.yml", "pull request", true},
		{"gitlab", ".gitlab-ci.yml", "merge request", true},
		{"none", "", "pull request", false},
	} {
		t.Run(tc.ci, func(t *testing.T) {
			dir := t.TempDir()
			res, err := scaffold.Init(dir, scaffold.Options{CI: tc.ci})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{".github/workflows/validate.yml", ".gitlab-ci.yml"} {
				if created := slices.Contains(res.Created, f); created != (f == tc.file) {
					t.Errorf("%s created = %v", f, created)
				}
				if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(f))); (err == nil) != (f == tc.file) {
					t.Errorf("%s on disk: %v", f, err)
				}
			}
			assertValid(t, dir)
			readme := readFile(t, dir, "README.md")
			agents := readFile(t, dir, "AGENTS.md")
			skill := readFile(t, dir, "plugins/example/skills/hello-world/SKILL.md")
			for name, s := range map[string]string{"README.md": readme, "AGENTS.md": agents, "SKILL.md": skill} {
				if !strings.Contains(s, tc.request) && (name != "AGENTS.md" || tc.runs) {
					t.Errorf("%s lacks %q", name, tc.request)
				}
				if other := map[string]string{"pull request": "merge request", "merge request": "pull request"}[tc.request]; strings.Contains(s, other) {
					t.Errorf("%s mentions %q", name, other)
				}
			}
			for name, s := range map[string]string{"README.md": readme, "AGENTS.md": agents} {
				if runs := strings.Contains(s, "CI runs"); runs != tc.runs {
					t.Errorf("%s says CI runs validate: %v, want %v", name, runs, tc.runs)
				}
			}
		})
	}
}

func TestInitNewGitLabCI(t *testing.T) {
	dir := t.TempDir()
	if _, err := scaffold.Init(dir, scaffold.Options{CI: "gitlab"}); err != nil {
		t.Fatal(err)
	}
	ci := readFile(t, dir, ".gitlab-ci.yml")
	for _, s := range []string{
		`GIT_DEPTH: "0"`,
		`$CI_PIPELINE_SOURCE == "merge_request_event"`,
		"$CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH",
		"go run github.com/yoanbernabeu/skillcrew/cmd/skillcrew@latest validate",
	} {
		if !strings.Contains(ci, s) {
			t.Errorf(".gitlab-ci.yml lacks %q:\n%s", s, ci)
		}
	}
	// The image is pinned by digest, as actions are pinned by commit SHA.
	if !regexp.MustCompile(`(?m)^  image: golang:\d+\.\d+@sha256:[0-9a-f]{64}$`).MatchString(ci) {
		t.Errorf("image not pinned by digest:\n%s", ci)
	}
}

func TestInitNewCIVersion(t *testing.T) {
	for ci, file := range map[string]string{"github": ".github/workflows/validate.yml", "gitlab": ".gitlab-ci.yml"} {
		for version, want := range map[string]string{"dev": "@latest validate", "v1.2.3": "@v1.2.3 validate"} {
			dir := t.TempDir()
			if _, err := scaffold.Init(dir, scaffold.Options{CI: ci, Version: version}); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, dir, file); !strings.Contains(got, "/cmd/skillcrew"+want) {
				t.Errorf("%s, version %q: %s lacks %q:\n%s", ci, version, file, want, got)
			}
		}
	}
}

func TestInitUnknownCI(t *testing.T) {
	for _, mode := range []string{"new", "convert"} {
		dir := t.TempDir()
		if mode == "convert" {
			writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name":"acme","plugins":[]}`)
		}
		before := listFiles(t, dir)
		_, err := scaffold.Init(dir, scaffold.Options{CI: "jenkins"})
		if err == nil {
			t.Fatalf("%s: unknown CI accepted", mode)
		}
		for _, s := range []string{"jenkins", "github", "gitlab", "none"} {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("%s: err = %v, want a mention of %q", mode, err, s)
			}
		}
		if after := listFiles(t, dir); !reflect.DeepEqual(before, after) {
			t.Errorf("%s: directory changed: %v", mode, after)
		}
	}
}

func TestInitNewRefusesToOverwriteGitLabCI(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitlab-ci.yml", "mine\n")
	_, err := scaffold.Init(dir, scaffold.Options{CI: "gitlab"})
	if err == nil || !strings.Contains(err.Error(), ".gitlab-ci.yml") {
		t.Fatalf("err = %v, want one naming .gitlab-ci.yml", err)
	}
	if readFile(t, dir, ".gitlab-ci.yml") != "mine\n" {
		t.Error(".gitlab-ci.yml was overwritten")
	}
}

func TestInitConvertIgnoresCI(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name":"acme","plugins":[{"name":"p","source":"./plugins/p"}]}`)
	writeFile(t, dir, "plugins/p/skills/review/SKILL.md", skillMD("review"))
	res, err := scaffold.Init(dir, scaffold.Options{CI: "gitlab"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".gitlab-ci.yml")); err == nil {
		t.Error("conversion must not write a CI file")
	}
	if notice := strings.Join(res.Notices, "\n"); !strings.Contains(notice, "--ci") || !strings.Contains(notice, "skillcrew.yoandev.co/docs/guides/ci/") {
		t.Errorf("notices = %v, want one saying --ci only applies to new registries", res.Notices)
	}
}
