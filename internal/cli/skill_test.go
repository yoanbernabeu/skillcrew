package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillNew(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.mustRun("registry", "init", dir, "--org", "acme")

	out := e.mustRun("skill", "new", "git:commit-message", dir,
		"--description", "Writes commit messages. Use it whenever you create a git commit.",
		"--status", "required", "--team", "backend")
	for _, want := range []string{
		"Created skill git:commit-message.",
		"  + plugins/git/.claude-plugin/plugin.json",
		"  + plugins/git/skills/commit-message/SKILL.md",
		"  ~ .claude-plugin/marketplace.json",
		"  ~ skillcrew.yaml",
		`team "backend" created`,
		"Status: required for team backend, available (the registry default) for the organization.",
		`write the instructions in plugins/git/skills/commit-message/SKILL.md`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if out := e.mustRun("validate", dir); !strings.Contains(out, "valid: 2 skills") {
		t.Errorf("validate:\n%s", out)
	}
}

func TestRegistryInitPointsToSkillNew(t *testing.T) {
	e := newEnv(t)
	out := e.mustRun("registry", "init", t.TempDir(), "--org", "acme")
	if !strings.Contains(out, `Add skills with "skillcrew skill new <plugin:skill>"`) {
		t.Errorf("output:\n%s", out)
	}
}

func TestSkillNewDefaultStatus(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.mustRun("registry", "init", dir, "--org", "acme")
	out := e.mustRun("skill", "new", "example:other", "--description", "Does other things. Use it for tests.", dir)
	if !strings.Contains(out, "Status: available (the registry default) for the organization.") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSkillNewExistingRule(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.mustRun("registry", "init", dir, "--org", "acme")
	if err := os.WriteFile(filepath.Join(dir, "skillcrew.yaml"), []byte("org: acme\nskills:\n  example:other: blocked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := e.mustRun("skill", "new", "example:other", "--description", "Does other things. Use it for tests.", dir)
	if !strings.Contains(out, "Status: blocked (already set in skillcrew.yaml) for the organization.") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSkillNewInTheCurrentDirectory(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.mustRun("registry", "init", dir, "--org", "acme")
	t.Chdir(dir)
	e.mustRun("skill", "new", "example:other", "--description", "Does other things. Use it for tests.", "--status", "recommended")
	if _, err := os.Stat(filepath.Join(dir, "plugins/example/skills/other/SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSkillNewRefusals(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.mustRun("registry", "init", dir, "--org", "acme")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"skill", "new"}, "Usage: skillcrew skill new <plugin:skill> [dir]"},
		{[]string{"skill", "new", "git:x", dir}, "description is required"},
		{[]string{"skill", "new", "example:hello-world", dir, "--description", "D."}, "already exists"},
		{[]string{"skill", "new", "git:x", t.TempDir(), "--description", "D."}, "registry init"},
		{[]string{"skill", "make"}, `unknown command "make"`},
	} {
		out, err := e.run(tc.args...)
		if err == nil || !strings.Contains(err.Error()+out, tc.want) {
			t.Errorf("skillcrew %s: err = %v, want %q\n%s", strings.Join(tc.args, " "), err, tc.want, out)
		}
	}
}
