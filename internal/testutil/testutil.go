// Package testutil builds throwaway Git registries for tests.
//
// Importing it isolates git from the developer's configuration (see IsolateGit).
package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func init() { IsolateGit() }

// IsolateGit makes every git command of the test process ignore the
// developer's global and system configuration (signing, hooks, templates,
// credential helpers…) and use a fixed identity. Test binaries that run git
// without importing this package call it from TestMain.
func IsolateGit() {
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
}

// Origin is a non-bare Git repository acting as a remote registry.
type Origin struct {
	t   *testing.T
	Dir string
}

// NewOrigin creates a repository on branch main with files committed.
func NewOrigin(t *testing.T, files map[string]string) *Origin {
	t.Helper()
	o := &Origin{t: t, Dir: t.TempDir()}
	o.Git("init", "-q", "-b", "main")
	o.Write(files)
	o.Commit("initial")
	return o
}

// Git runs git in the repository.
func (o *Origin) Git(args ...string) string {
	o.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = o.Dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		o.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write creates or overwrites files.
func (o *Origin) Write(files map[string]string) {
	o.t.Helper()
	for name, content := range files {
		p := filepath.Join(o.Dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			o.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			o.t.Fatal(err)
		}
	}
}

// Commit commits everything and returns the commit id.
func (o *Origin) Commit(msg string) string {
	o.t.Helper()
	o.Git("add", "-A")
	o.Git("commit", "-q", "--allow-empty", "-m", msg)
	return o.Git("rev-parse", "HEAD")
}

// Marketplace returns a marketplace.json listing in-repo plugins under plugins/.
func Marketplace(plugins ...string) string {
	type entry struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	m := struct {
		Name    string            `json:"name"`
		Owner   map[string]string `json:"owner"`
		Plugins []entry           `json:"plugins"`
	}{Name: "acme", Owner: map[string]string{"name": "Acme"}}
	for _, p := range plugins {
		m.Plugins = append(m.Plugins, entry{Name: p, Source: "./plugins/" + p})
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return string(b)
}

// SkillMD returns a valid SKILL.md.
func SkillMD(name, body string) string {
	return "---\nname: " + name + "\ndescription: The " + name + " skill.\n---\n" + body
}

// SkillPath returns the path of a skill's SKILL.md inside a registry.
func SkillPath(plugin, name string) string {
	return "plugins/" + plugin + "/skills/" + name + "/SKILL.md"
}

// NewLargeOrigin creates a registry of n plugins holding one skill each
// ("p<i>:s<i>"), with governance as skillcrew.yaml. It uses git fast-import,
// so registries of thousands of plugins are created in a second. The working
// tree is left empty to save disk space: run "git reset --hard" before Write
// and Commit.
func NewLargeOrigin(t *testing.T, n int, governance string) *Origin {
	t.Helper()
	o := &Origin{t: t, Dir: t.TempDir()}
	o.Git("init", "-q", "-b", "main")
	plugins := make([]string, n)
	for i := range plugins {
		plugins[i] = fmt.Sprintf("p%d", i)
	}
	var stream bytes.Buffer
	blob := func(content string) {
		fmt.Fprintf(&stream, "data %d\n%s\n", len(content), content)
	}
	fmt.Fprintf(&stream, "commit refs/heads/main\ncommitter test <test@example.com> 0 +0000\n")
	blob("large registry")
	fmt.Fprintf(&stream, "M 100644 inline .claude-plugin/marketplace.json\n")
	blob(Marketplace(plugins...))
	fmt.Fprintf(&stream, "M 100644 inline skillcrew.yaml\n")
	blob(governance)
	for i, p := range plugins {
		name := fmt.Sprintf("s%d", i)
		fmt.Fprintf(&stream, "M 100644 inline %s\n", SkillPath(p, name))
		blob(SkillMD(name, "v1"))
	}
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir, cmd.Stdin = o.Dir, &stream
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import: %v\n%s", err, out)
	}
	return o
}

// CountGit routes the git commands of the test through a wrapper that
// records them, and returns a function listing the commands run since (their
// arguments, one string per command). It changes PATH: not for parallel tests.
func CountGit(t *testing.T) func() []string {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "git.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + gitBin + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(log)
		_ = os.Remove(log)
		return strings.FieldsFunc(string(b), func(r rune) bool { return r == '\n' })
	}
}

// Network keeps the commands that talk to the remote.
func Network(commands []string) []string {
	var out []string
	for _, c := range commands {
		for _, f := range strings.Fields(c) {
			if f == "ls-remote" || f == "fetch" {
				out = append(out, c)
				break
			}
		}
	}
	return out
}
