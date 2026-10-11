package scaffold_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/scaffold"
	"github.com/yoanbernabeu/skillcrew/internal/skill"
)

const commitDescription = "Writes commit messages following the team convention. Use it whenever you create a git commit."

// newRegistry returns a registry created by "skillcrew registry init".
func newRegistry(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := scaffold.Init(dir, scaffold.Options{Org: "acme"}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newSkill(t *testing.T, dir string, opts scaffold.SkillOptions) scaffold.SkillResult {
	t.Helper()
	res, err := scaffold.NewSkill(dir, opts)
	if err != nil {
		t.Fatalf("NewSkill(%+v): %v", opts, err)
	}
	return res
}

func TestNewSkillInNewPlugin(t *testing.T) {
	dir := newRegistry(t)
	res := newSkill(t, dir, scaffold.SkillOptions{ID: "git:commit-message", Description: commitDescription, Status: registry.Required})

	wantCreated := []string{"plugins/git/.claude-plugin/plugin.json", "plugins/git/skills/commit-message/SKILL.md"}
	wantModified := []string{".claude-plugin/marketplace.json", "skillcrew.yaml"}
	if !reflect.DeepEqual(res.Created, wantCreated) || !reflect.DeepEqual(res.Modified, wantModified) {
		t.Errorf("created %v, modified %v", res.Created, res.Modified)
	}
	if res.File != "plugins/git/skills/commit-message/SKILL.md" || len(res.Issues) != 0 || res.OrgStatus != registry.Required {
		t.Errorf("unexpected result %+v", res)
	}

	reg := assertValid(t, dir)
	s, ok := reg.Skill("git:commit-message")
	if !ok || s.Dir != "plugins/git/skills/commit-message" {
		t.Fatalf("skill not discovered: %+v", reg.Skills)
	}
	if got := reg.Governance.Skills["git:commit-message"].Status; got != registry.Required {
		t.Errorf("status = %q, want required", got)
	}
	if got := reg.Governance.Skills["example:hello-world"].Status; got != registry.Recommended {
		t.Errorf("existing rule changed: example:hello-world = %q", got)
	}
	fm, err := skill.ParseFrontmatter([]byte(readFile(t, dir, res.File)))
	if err != nil || fm.Name != "commit-message" || fm.Description != commitDescription {
		t.Errorf("frontmatter = %+v, %v", fm, err)
	}
	if got := readFile(t, dir, "plugins/git/.claude-plugin/plugin.json"); got != "{\n  \"name\": \"git\"\n}\n" {
		t.Errorf("plugin.json = %q (no version: Claude Code would only update on version bumps)", got)
	}
	gov := readFile(t, dir, "skillcrew.yaml")
	for _, keep := range []string{"# Skillcrew governance file", "\n\n# Status of the skills not listed below.\ndefault: available\n\nskills:\n", "  example:hello-world: recommended\n  git:commit-message: required\n", "# teams:\n"} {
		if !strings.Contains(gov, keep) {
			t.Errorf("skillcrew.yaml lost %q:\n%s", keep, gov)
		}
	}
}

func TestNewSkillInExistingPlugin(t *testing.T) {
	dir := newRegistry(t)
	marketplace := readFile(t, dir, ".claude-plugin/marketplace.json")
	governance := readFile(t, dir, "skillcrew.yaml")

	res := newSkill(t, dir, scaffold.SkillOptions{ID: "example:second", Description: "Does a second thing. Use it for tests."})
	if !reflect.DeepEqual(res.Created, []string{"plugins/example/skills/second/SKILL.md"}) || len(res.Modified) != 0 {
		t.Errorf("created %v, modified %v", res.Created, res.Modified)
	}
	if readFile(t, dir, ".claude-plugin/marketplace.json") != marketplace || readFile(t, dir, "skillcrew.yaml") != governance {
		t.Error("marketplace.json or skillcrew.yaml changed although nothing had to")
	}
	reg := assertValid(t, dir)
	if _, ok := reg.Skill("example:second"); !ok {
		t.Error("skill not discovered")
	}
}

func TestNewSkillUsesTheDirectoryOfAnExistingPlugin(t *testing.T) {
	dir := marketplaceDir(t) // docs lives in extra/docs, under a pluginRoot
	writeFile(t, dir, "skillcrew.yaml", "org: acme\n")
	res := newSkill(t, dir, scaffold.SkillOptions{ID: "docs:slides", Description: "Builds slides. Use it for decks."})
	if res.File != "extra/docs/skills/slides/SKILL.md" {
		t.Errorf("file = %s", res.File)
	}
	if _, ok := assertValid(t, dir).Skill("docs:slides"); !ok {
		t.Error("skill not discovered")
	}
}

func TestNewSkillForTeams(t *testing.T) {
	cases := map[string]struct {
		governance string
		want       string // expected excerpt of the new skillcrew.yaml
	}{
		"no teams section": {
			governance: "org: acme\n",
			want:       "org: acme\n\nteams:\n  backend:\n    skills:\n      git:commit-message: required\n",
		},
		"no final newline": {
			governance: "org: acme",
			want:       "org: acme\n\nteams:\n  backend:\n    skills:\n      git:commit-message: required\n",
		},
		"other team": {
			governance: "org: acme\nteams:\n  frontend:\n    skills:\n      git:other: required\n# end\n",
			want:       "  frontend:\n    skills:\n      git:other: required\n  backend:\n    skills:\n      git:commit-message: required\n# end\n",
		},
		"team with rules, 4-space indent": {
			governance: "org: acme\nteams:\n    backend:\n        skills:\n            git:other: recommended # note\n\nskills: {}\n",
			want:       "            git:other: recommended # note\n            git:commit-message: required\n\nskills: {}\n",
		},
		"team with a mapping rule": {
			governance: "org: acme\nteams:\n  backend:\n    skills:\n      git:other:\n        status: recommended\n",
			want:       "      git:other:\n        status: recommended\n      git:commit-message: required\n",
		},
		"team without skills": {
			governance: "org: acme\nteams:\n  backend:\n",
			want:       "teams:\n  backend:\n    skills:\n      git:commit-message: required\n",
		},
		"team with empty skills": {
			governance: "org: acme\nteams:\n  backend:\n    skills:\n",
			want:       "    skills:\n      git:commit-message: required\n",
		},
		"team with empty flow skills": {
			governance: "org: acme\nteams:\n  backend:\n    skills: {} # none yet\n",
			want:       "    skills: # none yet\n      git:commit-message: required\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newRegistry(t)
			writeFile(t, dir, "plugins/git/skills/other/SKILL.md", skillMD("other"))
			writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name":"acme","plugins":[{"name":"example","source":"./plugins/example"},{"name":"git","source":"./plugins/git"}]}`)
			writeFile(t, dir, "skillcrew.yaml", tc.governance)

			newSkill(t, dir, scaffold.SkillOptions{ID: "git:commit-message", Description: commitDescription, Status: registry.Required, Team: "backend"})
			if got := readFile(t, dir, "skillcrew.yaml"); !strings.Contains(got, tc.want) {
				t.Errorf("skillcrew.yaml:\n%s\nwant it to contain:\n%s", got, tc.want)
			}
			reg := assertValid(t, dir)
			if got := reg.Governance.Teams["backend"].Skills["git:commit-message"].Status; got != registry.Required {
				t.Errorf("team status = %q", got)
			}
			if _, ok := reg.Governance.Skills["git:commit-message"]; ok {
				t.Error("an organization rule was added")
			}
		})
	}
}

func TestNewSkillOrganizationRule(t *testing.T) {
	cases := map[string]struct{ governance, want string }{
		"no skills section":    {"org: acme\n", "org: acme\n\nskills:\n  docs:guide: recommended\n"},
		"empty skills":         {"org: acme\nskills:\nteams: {}\n", "skills:\n  docs:guide: recommended\nteams: {}\n"},
		"empty flow skills":    {"org: acme\nskills: {}\n", "skills:\n  docs:guide: recommended\n"},
		"quoted keys":          {"org: acme\nskills:\n  \"docs:pdf\": available\n", "  \"docs:pdf\": available\n  docs:guide: recommended\n"},
		"comment after skills": {"org: acme\nskills:\n  docs:pdf: available\n\n# Teams\nteams: {}\n", "  docs:pdf: available\n  docs:guide: recommended\n\n# Teams\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name":"acme","plugins":[{"name":"docs","source":"./plugins/docs"}]}`)
			writeFile(t, dir, "plugins/docs/skills/pdf/SKILL.md", skillMD("pdf"))
			writeFile(t, dir, "skillcrew.yaml", tc.governance)
			newSkill(t, dir, scaffold.SkillOptions{ID: "docs:guide", Description: "Writes guides. Use it for docs.", Status: registry.Recommended})
			if got := readFile(t, dir, "skillcrew.yaml"); !strings.Contains(got, tc.want) {
				t.Errorf("skillcrew.yaml:\n%s\nwant it to contain:\n%s", got, tc.want)
			}
			if got := assertValid(t, dir).Governance.Skills["docs:guide"].Status; got != registry.Recommended {
				t.Errorf("status = %q", got)
			}
		})
	}
}

func TestNewSkillReportsCreatedTeam(t *testing.T) {
	dir := newRegistry(t)
	res := newSkill(t, dir, scaffold.SkillOptions{ID: "git:commit-message", Description: commitDescription, Status: registry.Required, Team: "backend"})
	if len(res.Notices) != 1 || !strings.Contains(res.Notices[0], `team "backend"`) {
		t.Errorf("notices = %v", res.Notices)
	}
}

func TestNewSkillDescriptionIsQuotedSafely(t *testing.T) {
	dir := newRegistry(t)
	desc := `Reviews code: "security" first, then # style. Use it on every PR`
	res := newSkill(t, dir, scaffold.SkillOptions{ID: "git:review", Description: desc})
	fm, err := skill.ParseFrontmatter([]byte(readFile(t, dir, res.File)))
	if err != nil || fm.Description != desc {
		t.Errorf("description = %q, %v", fm.Description, err)
	}
	assertValid(t, dir)
}

func TestNewSkillRefusals(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, dir string)
		opts  scaffold.SkillOptions
		want  string
	}{
		"no plugin":             {opts: scaffold.SkillOptions{ID: "commit-message"}, want: "plugin:skill"},
		"two colons":            {opts: scaffold.SkillOptions{ID: "git:commit:message"}, want: "plugin:skill"},
		"invalid skill name":    {opts: scaffold.SkillOptions{ID: "git:Commit_Message"}, want: "lowercase"},
		"invalid plugin name":   {opts: scaffold.SkillOptions{ID: "Git Tools:commit"}, want: "plugin name"},
		"no description":        {opts: scaffold.SkillOptions{ID: "git:commit"}, want: "description"},
		"description too long":  {opts: scaffold.SkillOptions{ID: "git:commit", Description: strings.Repeat("a", 1025)}, want: "description"},
		"control characters":    {opts: scaffold.SkillOptions{ID: "git:commit", Description: "Bad\x1b[31m"}, want: "control"},
		"C1 control character":  {opts: scaffold.SkillOptions{ID: "git:commit", Description: "Bad\u0085line"}, want: "control"},
		"line separator":        {opts: scaffold.SkillOptions{ID: "git:commit", Description: "Bad\u2028line"}, want: "control"},
		"bidi override":         {opts: scaffold.SkillOptions{ID: "git:commit", Description: "Bad\u202etxt.exe"}, want: "control"},
		"unknown status":        {opts: scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: "mandatory"}, want: "status"},
		"team without status":   {opts: scaffold.SkillOptions{ID: "git:commit", Description: "D.", Team: "backend"}, want: "status"},
		"invalid team name":     {opts: scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: registry.Required, Team: "back end"}, want: "team name"},
		"skill exists":          {opts: scaffold.SkillOptions{ID: "example:hello-world", Description: "D."}, want: "already exists"},
		"bare name collides":    {opts: scaffold.SkillOptions{ID: "git:hello-world", Description: "D."}, want: "example:hello-world"},
		"reserved skill name":   {opts: scaffold.SkillOptions{ID: "git:synced", Description: "D."}, want: "reserved"},
		"directory left behind": {setup: write("plugins/example/skills/draft/notes.md", "x"), opts: scaffold.SkillOptions{ID: "example:draft", Description: "D."}, want: "already exists"},
		"unlisted plugin directory": {
			setup: write("plugins/git/skills/other/SKILL.md", skillMD("other")),
			opts:  scaffold.SkillOptions{ID: "git:commit", Description: "D."}, want: "not listed",
		},
		"external plugin": {
			setup: write(".claude-plugin/marketplace.json", `{"name":"acme","plugins":[{"name":"example","source":"./plugins/example"},{"name":"git","source":{"source":"github","repo":"acme/git"}}]}`),
			opts:  scaffold.SkillOptions{ID: "git:commit", Description: "D."}, want: "outside the registry",
		},
		"single-skill plugin": {
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name":"acme","plugins":[{"name":"example","source":"./plugins/example"},{"name":"solo","source":"./plugins/solo"}]}`)
				writeFile(t, dir, "plugins/solo/SKILL.md", skillMD("solo"))
			},
			opts: scaffold.SkillOptions{ID: "solo:other", Description: "D."}, want: "SKILL.md at its root",
		},
		"already in skillcrew.yaml": {
			setup: write("skillcrew.yaml", "org: acme\nskills:\n  git:commit: blocked\n"),
			opts:  scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: registry.Required}, want: "already listed",
		},
		"already in the team": {
			setup: write("skillcrew.yaml", "org: acme\nteams:\n  backend:\n    skills:\n      git:commit: blocked\n"),
			opts:  scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: registry.Required, Team: "backend"}, want: "already listed",
		},
		"team softens the organization default": {
			setup: write("skillcrew.yaml", "org: acme\ndefault: required\n"),
			opts:  scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: registry.Recommended, Team: "backend"}, want: "cannot soften",
		},
		"team allows what the organization blocks": {
			setup: write("skillcrew.yaml", "org: acme\ndefault: blocked\n"),
			opts:  scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: registry.Required, Team: "backend"}, want: "blocked by the organization",
		},
		"flow-style skills": {
			setup: write("skillcrew.yaml", "org: acme\nskills: {example:hello-world: recommended}\n"),
			opts:  scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: registry.Required}, want: "edit skillcrew.yaml by hand",
		},
		"duplicate plugins key": {
			setup: write(".claude-plugin/marketplace.json", `{"name":"acme","plugins":[{"name":"ghost","source":"./nowhere"}],"plugins":[{"name":"example","source":"./plugins/example"}]}`),
			opts:  scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: registry.Required}, want: "edit .claude-plugin/marketplace.json by hand",
		},
		"invalid skillcrew.yaml": {
			setup: write("skillcrew.yaml", "org: acme\nskills: [\n"),
			opts:  scaffold.SkillOptions{ID: "git:commit", Description: "D."}, want: "skillcrew.yaml",
		},
		"no skillcrew.yaml": {
			setup: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "skillcrew.yaml")); err != nil {
					t.Fatal(err)
				}
			},
			opts: scaffold.SkillOptions{ID: "git:commit", Description: "D."}, want: "registry init",
		},
		"symlinked plugins directory": {
			setup: func(t *testing.T, dir string) {
				if err := os.Symlink(t.TempDir(), filepath.Join(dir, "plugins", "git")); err != nil {
					t.Fatal(err)
				}
			},
			opts: scaffold.SkillOptions{ID: "git:commit", Description: "D."}, want: "symbolic link",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newRegistry(t)
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			before := listFiles(t, dir)
			_, err := scaffold.NewSkill(dir, tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
			if after := listFiles(t, dir); !reflect.DeepEqual(before, after) {
				t.Errorf("the registry changed although the command failed")
			}
		})
	}
}

func TestNewSkillOutsideARegistry(t *testing.T) {
	_, err := scaffold.NewSkill(t.TempDir(), scaffold.SkillOptions{ID: "git:commit", Description: "D."})
	if err == nil || !strings.Contains(err.Error(), "registry init") {
		t.Fatalf("err = %v, want a hint to run registry init", err)
	}
}

func write(rel, content string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) { writeFile(t, dir, rel, content) }
}

func TestNewSkillGovernanceShapes(t *testing.T) {
	cases := map[string]struct{ governance, want string }{
		"block scalar before the last rule": {
			"org: acme\nskills:\n  docs:old:\n    status: deprecated\n    message: >\n      Use docs:pdf\n      instead.\n  docs:pdf: available\n",
			"  docs:pdf: available\n  docs:guide: required\n",
		},
		"block scalar as the last rule": {
			"org: acme\nskills:\n  docs:old:\n    status: deprecated\n    message: |\n      Use docs:pdf.\n\n      Really.\n# end\n",
			"      Really.\n  docs:guide: required\n# end\n",
		},
		"inline rule before the last rule": {
			"org: acme\nskills:\n  docs:old: {status: deprecated, replaced_by: docs:pdf}\n  docs:pdf: available\n",
			"  docs:pdf: available\n  docs:guide: required\n",
		},
		"inline rule over several lines": {
			"org: acme\nskills:\n  docs:old: {status: deprecated,\n    replaced_by: docs:pdf}\n",
			"    replaced_by: docs:pdf}\n  docs:guide: required\n",
		},
		"value wrapped over two lines": {
			"org: acme\nskills:\n  docs:old:\n    status: deprecated\n    message: use\n      docs:pdf\n",
			"      docs:pdf\n  docs:guide: required\n",
		},
		"CRLF line endings": {
			"org: acme\r\nskills:\r\n  docs:pdf: available\r\n",
			"org: acme\r\nskills:\r\n  docs:pdf: available\r\n  docs:guide: required\r\n",
		},
		"inline mapping with a space": {
			"org: acme\nskills: { }\n",
			"skills:\n  docs:guide: required\n",
		},
		"new top-level key after a comment": {
			"org: acme\n# skills:\n#   x:y: required\n",
			"#   x:y: required\n\nskills:\n  docs:guide: required\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name":"acme","plugins":[{"name":"docs","source":"./plugins/docs"}]}`)
			writeFile(t, dir, "plugins/docs/skills/pdf/SKILL.md", skillMD("pdf"))
			writeFile(t, dir, "plugins/docs/skills/old/SKILL.md", skillMD("old"))
			writeFile(t, dir, "skillcrew.yaml", tc.governance)
			newSkill(t, dir, scaffold.SkillOptions{ID: "docs:guide", Description: "Writes guides. Use it for docs.", Status: registry.Required})
			if got := readFile(t, dir, "skillcrew.yaml"); !strings.Contains(got, tc.want) {
				t.Errorf("skillcrew.yaml:\n%q\nwant it to contain:\n%q", got, tc.want)
			}
			assertValid(t, dir)
		})
	}
}

func TestNewSkillNextToAnInlineTeam(t *testing.T) {
	dir := newRegistry(t)
	writeFile(t, dir, "skillcrew.yaml", "org: acme\nteams:\n  web: {skills: {example:hello-world: required}}\n")
	newSkill(t, dir, scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: registry.Required, Team: "backend"})
	if got := assertValid(t, dir).Governance.Teams["backend"].Skills["git:commit"].Status; got != registry.Required {
		t.Errorf("status = %q", got)
	}
}

func TestNewSkillQuotesSpecialTeamNames(t *testing.T) {
	for _, team := range []string{"null", "true", "No", "1"} {
		t.Run(team, func(t *testing.T) {
			dir := newRegistry(t)
			newSkill(t, dir, scaffold.SkillOptions{ID: "git:commit", Description: "D.", Status: registry.Required, Team: team})
			if got := assertValid(t, dir).Governance.Teams[team].Skills["git:commit"].Status; got != registry.Required {
				t.Errorf("status = %q:\n%s", got, readFile(t, dir, "skillcrew.yaml"))
			}
		})
	}
}

func TestNewSkillKeepsTheMarketplaceFormatting(t *testing.T) {
	dir := newRegistry(t)
	marketplace := "{\n    \"name\": \"acme\",\n    \"owner\": {\"name\": \"Acme\", \"email\": \"dev@acme.test\"},\n    \"plugins\": [\n        {\n            \"name\": \"example\",\n            \"source\": \"./plugins/example\"\n        }\n    ]\n}\n"
	writeFile(t, dir, ".claude-plugin/marketplace.json", marketplace)
	newSkill(t, dir, scaffold.SkillOptions{ID: "git:commit", Description: "D."})
	want := "{\n    \"name\": \"acme\",\n    \"owner\": {\"name\": \"Acme\", \"email\": \"dev@acme.test\"},\n    \"plugins\": [\n        {\n            \"name\": \"example\",\n            \"source\": \"./plugins/example\"\n        },\n        {\n            \"name\": \"git\",\n            \"source\": \"./plugins/git\"\n        }\n    ]\n}\n"
	if got := readFile(t, dir, ".claude-plugin/marketplace.json"); got != want {
		t.Errorf("marketplace.json:\n%s\nwant:\n%s", got, want)
	}

	writeFile(t, dir, ".claude-plugin/marketplace.json", `{"name": "acme", "plugins": [{"name": "example", "source": "./plugins/example"}]}`)
	newSkill(t, dir, scaffold.SkillOptions{ID: "docs:guide", Description: "D."})
	if got := readFile(t, dir, ".claude-plugin/marketplace.json"); got != `{"name": "acme", "plugins": [{"name": "example", "source": "./plugins/example"}, {"name": "docs", "source": "./plugins/docs"}]}` {
		t.Errorf("one-line marketplace.json: %s", got)
	}
	assertValid(t, dir)
}

func TestNewSkillReportsAnExistingRule(t *testing.T) {
	dir := newRegistry(t)
	writeFile(t, dir, "skillcrew.yaml", "org: acme\nskills:\n  git:commit: blocked\n")
	res := newSkill(t, dir, scaffold.SkillOptions{ID: "git:commit", Description: "D."})
	if !res.RuleExists || res.OrgStatus != registry.Blocked {
		t.Errorf("result = %+v", res)
	}
}

func TestNewSkillChecksTheInstallName(t *testing.T) {
	dir := newRegistry(t)
	writeFile(t, dir, "skillcrew.yaml", "org: acme\nskills:\n  git:hello:\n    status: available\n    install_as: hello-world\n")
	before := listFiles(t, dir)
	_, err := scaffold.NewSkill(dir, scaffold.SkillOptions{ID: "git:hello", Description: "D."})
	if err == nil || !strings.Contains(err.Error(), "example:hello-world") {
		t.Fatalf("err = %v", err)
	}
	if after := listFiles(t, dir); !reflect.DeepEqual(before, after) {
		t.Error("the registry changed although the command failed")
	}
}
