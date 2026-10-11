package registry_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/skill"
)

func skillMD(name string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("---\nname: " + name + "\ndescription: The " + name + " skill.\n---\n")}
}

const marketplace = `{
  "name": "acme",
  "owner": {"name": "Acme"},
  "metadata": {"pluginRoot": "./plugins"},
  "plugins": [
    {"name": "security", "source": "./plugins/security"},
    {"name": "frontend", "source": "frontend"},
    {"name": "single", "source": "./single"},
    {"name": "remote", "source": {"source": "github", "repo": "someone/else"}}
  ]
}`

func fixture(governance string) fstest.MapFS {
	return fstest.MapFS{
		".claude-plugin/marketplace.json":            {Data: []byte(marketplace)},
		"plugins/security/skills/review/SKILL.md":    skillMD("review"),
		"plugins/security/skills/secrets/SKILL.md":   skillMD("secrets"),
		"plugins/frontend/skills/review/SKILL.md":    skillMD("review"),
		"plugins/frontend/skills/react/SKILL.md":     skillMD("react"),
		"plugins/frontend/skills/react/scripts/x.sh": {Data: []byte("echo")},
		"single/SKILL.md":                            skillMD("single"),
		"skillcrew.yaml":                             {Data: []byte(governance)},
	}
}

const governance = `
org: acme
default: available
skills:
  security:review: required
  frontend:review:
    status: recommended
    install_as: frontend-review
  frontend:react:
    status: deprecated
    message: Use the new one.
    replaced_by: security:secrets
  security:secrets:
    status: available
    sha: 0123456789abcdef0123456789abcdef01234567
  ghost:gone: blocked
teams:
  backend:
    skills:
      security:secrets: required
`

func TestLoad(t *testing.T) {
	reg, err := registry.Load(fixture(governance))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range reg.Skills {
		ids = append(ids, s.ID)
	}
	want := "frontend:react frontend:review security:review security:secrets single:single"
	if got := strings.Join(ids, " "); got != want {
		t.Fatalf("skills = %q, want %q", got, want)
	}
	s, ok := reg.Skill("frontend:react")
	if !ok || s.Dir != "plugins/frontend/skills/react" || s.Plugin != "frontend" || s.Name != "react" {
		t.Fatalf("unexpected skill: %+v", s)
	}
	if s, _ := reg.Skill("single:single"); s.Dir != "single" {
		t.Fatalf("root SKILL.md plugin should be a single skill, got %+v", s)
	}
	if len(reg.Warnings) != 1 || !strings.Contains(reg.Warnings[0], "remote") {
		t.Fatalf("expected a warning about the external plugin, got %v", reg.Warnings)
	}

	g := reg.Governance
	if g.Org != "acme" || g.Default != registry.Available {
		t.Fatalf("unexpected governance header: %+v", g)
	}
	if r := g.Skills["security:review"]; r.Status != registry.Required {
		t.Fatalf("scalar rule not parsed: %+v", r)
	}
	if r := g.Skills["frontend:react"]; r.Status != registry.Deprecated || r.Message != "Use the new one." || r.ReplacedBy != "security:secrets" {
		t.Fatalf("mapping rule not parsed: %+v", r)
	}
	if r := g.Teams["backend"].Skills["security:secrets"]; r.Status != registry.Required {
		t.Fatalf("team rule not parsed: %+v", r)
	}
}

func TestInstallNameAndOrgRule(t *testing.T) {
	reg, err := registry.Load(fixture(governance))
	if err != nil {
		t.Fatal(err)
	}
	review, _ := reg.Skill("frontend:review")
	if got := reg.InstallName(review); got != "frontend-review" {
		t.Fatalf("install name = %q", got)
	}
	single, _ := reg.Skill("single:single")
	if got := reg.InstallName(single); got != "single" {
		t.Fatalf("install name = %q", got)
	}
	if r := reg.Governance.OrgRule("single:single"); r.Status != registry.Available {
		t.Fatalf("unlisted skills should get the default status, got %+v", r)
	}
}

func TestLoadErrors(t *testing.T) {
	base := fixture(governance)
	cases := map[string]func(fstest.MapFS){
		"no marketplace":  func(f fstest.MapFS) { delete(f, ".claude-plugin/marketplace.json") },
		"bad marketplace": func(f fstest.MapFS) { f[".claude-plugin/marketplace.json"] = &fstest.MapFile{Data: []byte("{")} },
		"no skillcrew":    func(f fstest.MapFS) { delete(f, "skillcrew.yaml") },
		"unknown status": func(f fstest.MapFS) {
			f["skillcrew.yaml"] = &fstest.MapFile{Data: []byte("org: a\nskills:\n  x:y: mandatory\n")}
		},
		"unknown yaml key": func(f fstest.MapFS) { f["skillcrew.yaml"] = &fstest.MapFile{Data: []byte("org: a\nskils: {}\n")} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := fstest.MapFS{}
			for k, v := range base {
				f[k] = v
			}
			mutate(f)
			if _, err := registry.Load(f); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestValidate(t *testing.T) {
	reg, err := registry.Load(fixture(governance))
	if err != nil {
		t.Fatal(err)
	}
	issues := reg.Validate(fixture(governance))
	for _, i := range issues {
		if i.Severity == registry.Error {
			t.Fatalf("valid registry reported an error: %v", i)
		}
	}
}

func TestValidateDetectsProblems(t *testing.T) {
	cases := map[string]struct {
		governance string
		mutate     func(fstest.MapFS)
		want       string
	}{
		"missing org":               {governance: "skills: {}\n", want: "org"},
		"unknown skill":             {governance: "org: a\nskills:\n  nope:nope: required\n", want: "nope:nope"},
		"blocked unknown is ok":     {governance: "org: a\nskills:\n  frontend:review: {install_as: fr}\n  nope:nope: blocked\n", want: ""},
		"collision":                 {governance: "org: a\n", want: "review"},
		"team softens":              {governance: "org: a\nskills:\n  security:review: required\n  frontend:review: {install_as: fr}\nteams:\n  t:\n    skills:\n      security:review: recommended\n", want: "cannot soften"},
		"team blocks required":      {governance: "org: a\nskills:\n  security:review: required\n  frontend:review: {install_as: fr}\nteams:\n  t:\n    skills:\n      security:review: blocked\n", want: "cannot soften"},
		"team pins":                 {governance: "org: a\nskills:\n  frontend:review: {install_as: fr}\nteams:\n  t:\n    skills:\n      security:review: {status: required, ref: v1}\n", want: "only at organization level"},
		"ref and sha":               {governance: "org: a\nskills:\n  frontend:review: {install_as: fr, ref: v1, sha: abc}\n", want: "both"},
		"bad alias":                 {governance: "org: a\nskills:\n  frontend:review: {install_as: Bad_Name}\n", want: "Bad_Name"},
		"unknown replacement":       {governance: "org: a\nskills:\n  frontend:review: {install_as: fr, status: deprecated, replaced_by: x:y}\n", want: "x:y"},
		"invalid skill":             {governance: "org: a\nskills:\n  frontend:review: {install_as: fr}\n", mutate: func(f fstest.MapFS) { f["plugins/security/skills/secrets/SKILL.md"] = skillMD("wrong") }, want: "must match"},
		"collision ignores blocked": {governance: "org: a\nskills:\n  frontend:review: blocked\n", want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := fixture(tc.governance)
			if tc.mutate != nil {
				tc.mutate(f)
			}
			reg, err := registry.Load(f)
			if err != nil {
				t.Fatal(err)
			}
			var errs []string
			for _, i := range reg.Validate(f) {
				if i.Severity == registry.Error {
					errs = append(errs, i.Message)
				}
			}
			joined := strings.Join(errs, "\n")
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("expected no errors, got:\n%s", joined)
				}
				return
			}
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("expected an error containing %q, got:\n%s", tc.want, joined)
			}
		})
	}
}

func TestDiscoverIgnoresGovernance(t *testing.T) {
	f := fixture(governance)
	delete(f, "skillcrew.yaml")
	reg, err := registry.Discover(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Skills) != 5 || reg.Name != "acme" || reg.Governance.Default != registry.Available {
		t.Fatalf("unexpected registry: %+v", reg)
	}
}

func TestDiscoverSkipsUnsafeNames(t *testing.T) {
	f := fixture(governance)
	f[".claude-plugin/marketplace.json"] = &fstest.MapFile{Data: []byte(`{"name":"acme","plugins":[
		{"name":"security","source":"./plugins/security"},
		{"name":"security","source":"./plugins/frontend"},
		{"name":"bad name\u001b[2J","source":"./single"}]}`)}
	f["plugins/security/skills/Bad_Name/SKILL.md"] = skillMD("Bad_Name")
	reg, err := registry.Discover(f)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range reg.Skills {
		ids = append(ids, s.ID)
	}
	if got := strings.Join(ids, " "); got != "security:review security:secrets" {
		t.Fatalf("skills = %q", got)
	}
	if len(reg.Warnings) != 3 {
		t.Fatalf("expected a warning per skipped entry, got %v", reg.Warnings)
	}
}

func TestPluginDir(t *testing.T) {
	reg, err := registry.Load(fixture(governance))
	if err != nil {
		t.Fatal(err)
	}
	for plugin, want := range map[string]string{"frontend": "plugins/frontend", "single": "single"} {
		if got, ok := reg.PluginDir(plugin); !ok || got != want {
			t.Errorf("PluginDir(%s) = %q %v", plugin, got, ok)
		}
	}
	if _, ok := reg.PluginDir("nope"); ok {
		t.Error("unknown plugin")
	}
}

func TestValidateTeamRules(t *testing.T) {
	cases := map[string]string{
		"missing status":       "org: a\nskills:\n  frontend:review: {install_as: fr}\nteams:\n  t:\n    skills:\n      security:review: {message: hi}\n",
		"require an org block": "org: a\nskills:\n  frontend:review: {install_as: fr}\n  security:review: blocked\nteams:\n  t:\n    skills:\n      security:review: required\n",
	}
	for name, gov := range cases {
		t.Run(name, func(t *testing.T) {
			f := fixture(gov)
			reg, err := registry.Load(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range reg.Validate(f) {
				if i.Severity == registry.Error && strings.Contains(i.Message, "teams.t") {
					return
				}
			}
			t.Fatalf("expected a team error, got %v", reg.Validate(f))
		})
	}
}

// A bad plugin entry must not stop the whole registry, or the kill switch
// (blocked skills) would stop working everywhere.
func TestEscapingPluginIsSkipped(t *testing.T) {
	f := fixture(governance)
	f[".claude-plugin/marketplace.json"] = &fstest.MapFile{Data: []byte(`{"name":"a","plugins":[
		{"name":"evil","source":"./../x"},
		{"name":"security","source":"./plugins/security"}]}`)}
	reg, err := registry.Discover(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Skills) != 2 || len(reg.Warnings) != 1 || !strings.Contains(reg.Warnings[0], `"evil"`) {
		t.Fatalf("skills %v warnings %v", reg.Skills, reg.Warnings)
	}
}

// Paths that name the same file on macOS (case, Unicode normalization) would
// install different content than what Linux CI and reviewers see.
func TestValidateRejectsCollidingPaths(t *testing.T) {
	for name, files := range map[string][2]string{
		"case":    {"plugins/security/skills/review/notes.md", "plugins/security/skills/review/NOTES.md"},
		"unicode": {"plugins/security/skills/review/résumé.sh", "plugins/security/skills/review/résumé.sh"},
	} {
		t.Run(name, func(t *testing.T) {
			f := fixture(governance)
			f[files[0]] = &fstest.MapFile{Data: []byte("benign")}
			f[files[1]] = &fstest.MapFile{Data: []byte("malicious")}
			reg, err := registry.Load(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range reg.Validate(f) {
				if i.Severity == registry.Error && strings.Contains(i.Message, "same file") {
					return
				}
			}
			t.Fatalf("collision not reported: %v", reg.Validate(f))
		})
	}
}

func TestValidateRejectsReservedNames(t *testing.T) {
	f := fixture("org: a\nskills:\n  frontend:review: {install_as: synced}\n")
	reg, _ := registry.Load(f)
	for _, i := range reg.Validate(f) {
		if i.Severity == registry.Error && strings.Contains(i.Message, "reserved") {
			return
		}
	}
	t.Fatal("a reserved install name must be rejected")
}

func errorsAndWarnings(issues []registry.Issue) (errs, warns string) {
	var e, w []string
	for _, i := range issues {
		if i.Severity == registry.Error {
			e = append(e, i.Message)
		} else {
			w = append(w, i.Message)
		}
	}
	return strings.Join(e, "\n"), strings.Join(w, "\n")
}

func TestValidatePinFormat(t *testing.T) {
	for _, tc := range []struct{ pin, want string }{
		{"sha: abc", "full commit id"},
		{"sha: 0123456789ABCDEF0123456789ABCDEF01234567", "full commit id"},
		{"sha: main", "full commit id"},
		{"ref: v1..2", "tag name"},
		{"ref: -v1", "tag name"},
		{"ref: 'v 1'", "tag name"},
		{"ref: v1.lock", "tag name"},
		{"ref: refs/tags/v1", "tag name"},
		{"ref: 'v1~1'", "tag name"},
		{"ref: v1/", "tag name"},
		{"ref: v1.2.0", ""},
		{"ref: release/v1", ""},
		{"sha: 0123456789abcdef0123456789abcdef01234567", ""},
	} {
		t.Run(tc.pin, func(t *testing.T) {
			f := fixture("org: a\nskills:\n  frontend:review: {install_as: fr, " + tc.pin + "}\n")
			reg, err := registry.Load(f)
			if err != nil {
				t.Fatal(err)
			}
			errs, _ := errorsAndWarnings(reg.Validate(f))
			if tc.want == "" && errs != "" {
				t.Fatalf("unexpected errors:\n%s", errs)
			}
			if !strings.Contains(errs, tc.want) {
				t.Fatalf("expected an error containing %q, got:\n%s", tc.want, errs)
			}
		})
	}
}

func TestValidTagName(t *testing.T) {
	for name, want := range map[string]bool{
		"v1": true, "v1.2.3": true, "release/v1": true, "": false, "@": false, "a@{b": false,
		".v1": false, "a/.b": false, "a//b": false, "v1.": false, "a:b": false, "a?b": false,
		"a*b": false, "a[b": false, `a\b`: false, "a\x01b": false, "-x": false,
	} {
		if got := registry.ValidTagName(name); got != want {
			t.Errorf("ValidTagName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestValidatePluginLayout(t *testing.T) {
	f := fixture("org: a\nskills:\n  frontend:review: {install_as: fr}\n")
	f[".claude-plugin/marketplace.json"] = &fstest.MapFile{Data: []byte(`{"name":"a","plugins":[
		{"name":"security","source":"./plugins/security"},
		{"name":"frontend","source":"./plugins/frontend"},
		{"name":"single","source":"./single"},
		{"name":"ghost","source":"./plugins/ghost"},
		{"name":"custom","source":"./plugins/custom"},
		{"name":"cmds","source":"./plugins/cmds"}]}`)}
	f["plugins/unlisted/skills/x/SKILL.md"] = skillMD("x")
	f["plugins/nothing/README.md"] = &fstest.MapFile{Data: []byte("not a plugin")}
	f["plugins/custom/.claude-plugin/plugin.json"] = &fstest.MapFile{Data: []byte(`{"name":"custom","skills":["./extra"]}`)}
	f["plugins/custom/skills/c/SKILL.md"] = skillMD("c")
	f["plugins/custom/extra/d/SKILL.md"] = skillMD("d")
	f["plugins/cmds/commands/hello.md"] = &fstest.MapFile{Data: []byte("hi")}
	reg, err := registry.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := errorsAndWarnings(reg.Validate(f))
	if !strings.Contains(errs, `"ghost"`) || !strings.Contains(errs, "does not exist") {
		t.Errorf("a listed plugin without directory must be an error:\n%s", errs)
	}
	for _, want := range []string{"plugins/unlisted", `"custom"`, `"cmds"`} {
		if !strings.Contains(warns, want) {
			t.Errorf("warnings should mention %s:\n%s", want, warns)
		}
	}
	if strings.Contains(warns+errs, "plugins/nothing") {
		t.Errorf("a directory without skills is not a plugin:\n%s\n%s", warns, errs)
	}
	if strings.Contains(warns, `"security"`) {
		t.Errorf("a regular plugin must not be reported:\n%s", warns)
	}
}

// Plugin layout findings are for administrators: a sync must not show them
// to every developer.
func TestPluginLayoutIsNotASyncWarning(t *testing.T) {
	f := fixture(governance)
	f["plugins/unlisted/skills/x/SKILL.md"] = skillMD("x")
	reg, err := registry.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range reg.Warnings {
		if strings.Contains(w, "unlisted") {
			t.Fatalf("unexpected registry warning %q", w)
		}
	}
}

func TestIssuesNameTheirSkill(t *testing.T) {
	f := fixture("org: a\nskills:\n  frontend:review: {install_as: fr, sha: abc}\n")
	f["plugins/security/skills/secrets/SKILL.md"] = skillMD("wrong")
	reg, err := registry.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, i := range reg.Validate(f) {
		got[i.Skill] = true
	}
	for _, id := range []string{"security:secrets", "frontend:review"} {
		if !got[id] {
			t.Errorf("no issue names %s: %+v", id, reg.Validate(f))
		}
	}
}

// A blocked skill is never installed: its content does not fail validation.
func TestBlockedSkillContentIsAWarning(t *testing.T) {
	f := fixture("org: a\nskills:\n  frontend:review: {install_as: fr}\n  security:secrets: blocked\n")
	f["plugins/security/skills/secrets/SKILL.md"] = skillMD("wrong")
	reg, err := registry.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := errorsAndWarnings(reg.Validate(f))
	if errs != "" || !strings.Contains(warns, "security:secrets") {
		t.Fatalf("errors:\n%s\nwarnings:\n%s", errs, warns)
	}
}

func TestGovernanceRejectsDuplicateKeys(t *testing.T) {
	for name, yaml := range map[string]string{
		"top level":   "org: a\norg: b\n",
		"skills":      "org: a\nskills:\n  x:y: required\n  x:y: blocked\n",
		"teams":       "org: a\nteams:\n  web: {}\n  web: {}\n",
		"team skills": "org: a\nteams:\n  web:\n    skills:\n      x:y: required\n      x:y: blocked\n",
		"rule fields": "org: a\nskills:\n  x:y: {status: required, status: blocked}\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := fixture(governance)
			f["skillcrew.yaml"] = &fstest.MapFile{Data: []byte(yaml)}
			if _, err := registry.Load(f); err == nil || !strings.Contains(err.Error(), "already defined") {
				t.Fatalf("expected a duplicate key error, got %v", err)
			}
		})
	}
}

func TestGovernanceRejectsUnknownFields(t *testing.T) {
	for name, yaml := range map[string]string{
		"team":  "org: a\nteams:\n  web:\n    skils: {}\n",
		"rule":  "org: a\nskills:\n  x:y: {status: required, pin: v1}\n",
		"teams": "org: a\nteams: [web]\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := fixture(governance)
			f["skillcrew.yaml"] = &fstest.MapFile{Data: []byte(yaml)}
			if _, err := registry.Load(f); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestGovernanceAcceptsAnchorsAndEmptySections(t *testing.T) {
	f := fixture(governance)
	f["skillcrew.yaml"] = &fstest.MapFile{Data: []byte("org: a\nskills: &s\n  x:y: required\nteams:\n  web:\n    skills: *s\n  ops:\n  qa:\n    skills:\n")}
	reg, err := registry.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	g := reg.Governance
	if g.Skills["x:y"].Status != registry.Required || g.Teams["web"].Skills["x:y"].Status != registry.Required {
		t.Fatalf("anchors not resolved: %+v", g)
	}
	if _, ok := g.Teams["ops"]; !ok {
		t.Fatalf("empty team dropped: %+v", g.Teams)
	}
}

// Duplicate keys are detected in linear time: yaml.v3 compares every pair of
// keys of a mapping, which took seconds on a large governance file.
func TestLargeGovernanceParsesQuickly(t *testing.T) {
	var b strings.Builder
	b.WriteString("org: acme\nskills:\n")
	for i := range 60000 {
		fmt.Fprintf(&b, "  p%d:s%d: required\n", i, i)
	}
	b.WriteString("teams:\n  web:\n    skills:\n")
	for i := range 60000 {
		fmt.Fprintf(&b, "      p%d:s%d: required\n", i, i)
	}
	f := fixture(governance)
	f["skillcrew.yaml"] = &fstest.MapFile{Data: []byte(b.String())}
	start := time.Now()
	reg, err := registry.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("parsing 120,000 rules took %s", d)
	}
	if len(reg.Governance.Skills) != 60000 || len(reg.Governance.Teams["web"].Skills) != 60000 {
		t.Fatal("rules not parsed")
	}
}

// A skill created by "skillcrew skill new" whose instructions were never
// written is reported, without failing validation.
func TestUnwrittenSkillIsAWarning(t *testing.T) {
	f := fixture("org: a\nskills:\n  frontend:review: {install_as: fr}\n")
	f["plugins/security/skills/secrets/SKILL.md"] = &fstest.MapFile{Data: []byte("---\nname: secrets\ndescription: D.\n---\n\n# Secrets\n\n" + skill.Placeholder + "\nMore text.\n")}
	reg, err := registry.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := errorsAndWarnings(reg.Validate(f))
	if errs != "" || !strings.Contains(warns, "security:secrets") || !strings.Contains(warns, "TODO") {
		t.Fatalf("errors:\n%s\nwarnings:\n%s", errs, warns)
	}
}
