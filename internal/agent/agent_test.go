package agent_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/yoanbernabeu/skillcrew/internal/agent"
)

const (
	bin    = "/usr/local/bin/skillcrew"
	newBin = "/opt/homebrew/bin/skillcrew"
)

// spec describes where and how each agent stores its hook, from the test's
// point of view.
type spec struct {
	name    string
	dir     string // config dir under home
	file    string // hook file relative to home
	cmds    string // gjson path listing every hook command
	arg     string // expected sync argument
	shared  bool   // the file may hold user content
	foreign string // existing user content (shared files only)
	// stillThere lists gjson paths that must survive install and uninstall.
	stillThere []string
}

var specs = []spec{
	{
		name: "claude-code", dir: ".claude", file: ".claude/settings.json",
		cmds: "hooks.SessionStart.#.hooks.#.command", arg: "--hook", shared: true,
		foreign: `{
  "model": "opus",
  "hooks": {
    "SessionStart": [{"matcher": "resume", "hooks": [{"type": "command", "command": "echo hi"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}]}]
  },
  "permissions": {"allow": ["Bash(ls)"]}
}`,
		stillThere: []string{"model", "permissions.allow.0", "hooks.SessionStart.0.matcher", "hooks.Stop.0.hooks.0.command"},
	},
	{
		name: "codex", dir: ".codex", file: ".codex/hooks.json",
		cmds: "hooks.SessionStart.#.hooks.#.command", arg: "--hook", shared: true,
		foreign: `{
  "zeta": 1,
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "echo hi"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}]}]
  },
  "alpha": true
}`,
		stillThere: []string{"zeta", "alpha", "hooks.SessionStart.0.hooks.0.command", "hooks.Stop.0.hooks.0.command"},
	},
	{
		name: "copilot-cli", dir: ".copilot", file: ".copilot/hooks/skillcrew.json",
		cmds: "hooks.sessionStart.#.bash", arg: "--hook",
	},
	{
		name: "cursor", dir: ".cursor", file: ".cursor/hooks.json",
		cmds: "hooks.sessionStart.#.command", arg: "--hook", shared: true,
		foreign: `{
  "version": 2,
  "hooks": {
    "sessionStart": [{"command": "echo hi"}],
    "stop": [{"command": "say done"}]
  },
  "extra": "x"
}`,
		stillThere: []string{"extra", "hooks.sessionStart.0.command", "hooks.stop.0.command"},
	},
	{
		name: "gemini-cli", dir: ".gemini", file: ".gemini/settings.json",
		cmds: "hooks.SessionStart.#.hooks.#.command", arg: "--hook=json", shared: true,
		foreign: `{
  "theme": "Dracula",
  "hooks": {
    "SessionStart": [{"matcher": "resume", "hooks": [{"type": "command", "command": "echo hi"}]}],
    "AfterTool": [{"hooks": [{"type": "command", "command": "say done"}]}]
  },
  "general": {"vimMode": true}
}`,
		stillThere: []string{"theme", "general.vimMode", "hooks.SessionStart.0.matcher", "hooks.AfterTool.0.hooks.0.command"},
	},
}

func get(t *testing.T, name string) agent.Agent {
	t.Helper()
	for _, a := range agent.All() {
		if a.Name() == name {
			return a
		}
	}
	t.Fatalf("no agent %q", name)
	return nil
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ours returns the Skillcrew hook commands found in a hook file.
func ours(t *testing.T, s spec, content string) []string {
	t.Helper()
	if !gjson.Valid(content) {
		t.Fatalf("invalid JSON written:\n%s", content)
	}
	var out []string
	var walk func(r gjson.Result)
	walk = func(r gjson.Result) {
		if r.IsArray() {
			r.ForEach(func(_, v gjson.Result) bool { walk(v); return true })
			return
		}
		if strings.Contains(r.String(), "skillcrew") {
			out = append(out, r.String())
		}
	}
	walk(gjson.Get(content, s.cmds))
	return out
}

func topKeys(content string) []string {
	var keys []string
	gjson.Parse(content).ForEach(func(k, _ gjson.Result) bool {
		keys = append(keys, k.String())
		return true
	})
	return keys
}

func TestAll(t *testing.T) {
	var names []string
	for _, a := range agent.All() {
		names = append(names, a.Name())
	}
	want := []string{"claude-code", "codex", "copilot-cli", "cursor", "gemini-cli"}
	if !slices.Equal(names, want) {
		t.Fatalf("All() = %v, want %v", names, want)
	}
}

func TestDetected(t *testing.T) {
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			a := get(t, s.name)
			if a.Detected(home) {
				t.Fatal("detected without config dir")
			}
			if err := os.Mkdir(filepath.Join(home, s.dir), 0o755); err != nil {
				t.Fatal(err)
			}
			if !a.Detected(home) {
				t.Fatal("not detected with config dir")
			}
		})
	}
}

func TestInstallMissingFile(t *testing.T) {
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			a := get(t, s.name)
			if ok, err := a.HookInstalled(home); err != nil || ok {
				t.Fatalf("HookInstalled before install = %v, %v", ok, err)
			}
			if err := a.InstallHook(home, bin); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, s.file)
			content := read(t, path)
			want := bin + " sync " + s.arg
			if got := ours(t, s, content); !slices.Equal(got, []string{want}) {
				t.Fatalf("commands = %v, want [%s]\n%s", got, want, content)
			}
			if ok, err := a.HookInstalled(home); err != nil || !ok {
				t.Fatalf("HookInstalled after install = %v, %v", ok, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o644 {
				t.Errorf("mode = %v, want 0644", info.Mode().Perm())
			}
		})
	}
}

func TestInstallFormats(t *testing.T) {
	tests := map[string]string{
		"claude-code": `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"/b/skillcrew sync --hook"}]}]}}`,
		"codex":       `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/b/skillcrew sync --hook"}]}]}}`,
		"copilot-cli": `{"version":1,"hooks":{"sessionStart":[{"type":"command","bash":"/b/skillcrew sync --hook"}]}}`,
		"cursor":      `{"version":1,"hooks":{"sessionStart":[{"command":"/b/skillcrew sync --hook","timeout":10}]}}`,
		"gemini-cli":  `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"/b/skillcrew sync --hook=json"}]}]}}`,
	}
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			if err := get(t, s.name).InstallHook(home, "/b/skillcrew"); err != nil {
				t.Fatal(err)
			}
			got := gjson.Parse(read(t, filepath.Join(home, s.file))).Raw
			if compact(got) != tests[s.name] {
				t.Fatalf("got %s\nwant %s", compact(got), tests[s.name])
			}
		})
	}
}

func compact(s string) string {
	var b strings.Builder
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' && (i == 0 || s[i-1] != '\\') {
			inString = !inString
		}
		if !inString && (c == ' ' || c == '\n' || c == '\t' || c == '\r') {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func TestInstallPreservesExisting(t *testing.T) {
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			a := get(t, s.name)
			if !s.shared {
				other := filepath.Join(home, filepath.Dir(s.file), "other.json")
				write(t, other, `{"keep":true}`)
				if err := a.InstallHook(home, bin); err != nil {
					t.Fatal(err)
				}
				if err := a.UninstallHook(home); err != nil {
					t.Fatal(err)
				}
				if read(t, other) != `{"keep":true}` {
					t.Fatal("sibling hook file changed")
				}
				return
			}
			path := filepath.Join(home, s.file)
			write(t, path, s.foreign)
			keys := topKeys(s.foreign)

			if err := a.InstallHook(home, bin); err != nil {
				t.Fatal(err)
			}
			content := read(t, path)
			if got := ours(t, s, content); len(got) != 1 {
				t.Fatalf("our commands = %v\n%s", got, content)
			}
			if got := topKeys(content); !slices.Equal(got, keys) {
				t.Errorf("top-level keys = %v, want %v", got, keys)
			}
			for _, p := range s.stillThere {
				if gjson.Get(content, p).Raw != gjson.Get(s.foreign, p).Raw {
					t.Errorf("%s changed: %s\n%s", p, gjson.Get(content, p).Raw, content)
				}
			}
			if !strings.Contains(content, "\n  \"") {
				t.Errorf("not indented with 2 spaces:\n%s", content)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0o600 {
				t.Errorf("mode = %v, want 0600 preserved", info.Mode().Perm())
			}

			if err := a.UninstallHook(home); err != nil {
				t.Fatal(err)
			}
			content = read(t, path)
			if got := ours(t, s, content); len(got) != 0 {
				t.Fatalf("still installed: %v", got)
			}
			if got := topKeys(content); !slices.Equal(got, keys) {
				t.Errorf("top-level keys after uninstall = %v, want %v", got, keys)
			}
			for _, p := range s.stillThere {
				if gjson.Get(content, p).Raw != gjson.Get(s.foreign, p).Raw {
					t.Errorf("%s changed after uninstall:\n%s", p, content)
				}
			}
		})
	}
}

func TestCursorKeepsVersion(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".cursor", "hooks.json")
	write(t, path, `{"version": 2}`)
	if err := get(t, "cursor").InstallHook(home, bin); err != nil {
		t.Fatal(err)
	}
	if v := gjson.Get(read(t, path), "version").Int(); v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
}

func TestInstallIdempotent(t *testing.T) {
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			a := get(t, s.name)
			path := filepath.Join(home, s.file)
			if s.shared {
				write(t, path, s.foreign)
			}
			if err := a.InstallHook(home, bin); err != nil {
				t.Fatal(err)
			}
			first := read(t, path)
			before, _ := os.Stat(path)
			if err := a.InstallHook(home, bin); err != nil {
				t.Fatal(err)
			}
			if second := read(t, path); second != first {
				t.Fatalf("second install changed the file:\n%s\n---\n%s", first, second)
			}
			after, _ := os.Stat(path)
			if !os.SameFile(before, after) {
				t.Error("second install rewrote the file")
			}
			if got := ours(t, s, first); len(got) != 1 {
				t.Fatalf("our commands = %v", got)
			}
		})
	}
}

func TestInstallReplacesOtherBin(t *testing.T) {
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			a := get(t, s.name)
			path := filepath.Join(home, s.file)
			if s.shared {
				write(t, path, s.foreign)
			}
			if err := a.InstallHook(home, bin); err != nil {
				t.Fatal(err)
			}
			if err := a.InstallHook(home, newBin); err != nil {
				t.Fatal(err)
			}
			content := read(t, path)
			want := newBin + " sync " + s.arg
			if got := ours(t, s, content); !slices.Equal(got, []string{want}) {
				t.Fatalf("commands = %v, want [%s]\n%s", got, want, content)
			}
			if s.shared {
				for _, p := range s.stillThere {
					if gjson.Get(content, p).Raw != gjson.Get(s.foreign, p).Raw {
						t.Errorf("%s changed:\n%s", p, content)
					}
				}
			}
		})
	}
}

func TestInstallQuotesBin(t *testing.T) {
	home := t.TempDir()
	a := get(t, "claude-code")
	if err := a.InstallHook(home, "/Users/Jane Doe/bin/skillcrew"); err != nil {
		t.Fatal(err)
	}
	got := ours(t, specs[0], read(t, filepath.Join(home, ".claude", "settings.json")))
	want := []string{`'/Users/Jane Doe/bin/skillcrew' sync --hook`}
	if !slices.Equal(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	// A plain path replaces the quoted one instead of duplicating it.
	if err := a.InstallHook(home, bin); err != nil {
		t.Fatal(err)
	}
	got = ours(t, specs[0], read(t, filepath.Join(home, ".claude", "settings.json")))
	if !slices.Equal(got, []string{bin + " sync --hook"}) {
		t.Fatalf("commands = %v", got)
	}
}

func TestUninstallCleansEmpties(t *testing.T) {
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			a := get(t, s.name)
			path := filepath.Join(home, s.file)
			if s.shared {
				write(t, path, `{"keep": 1}`)
			}
			if err := a.InstallHook(home, bin); err != nil {
				t.Fatal(err)
			}
			if err := a.UninstallHook(home); err != nil {
				t.Fatal(err)
			}
			if ok, err := a.HookInstalled(home); err != nil || ok {
				t.Fatalf("HookInstalled after uninstall = %v, %v", ok, err)
			}
			if !s.shared {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("owned file still present: %v", err)
				}
				return
			}
			content := read(t, path)
			if gjson.Get(content, "hooks").Exists() {
				t.Fatalf("empty hooks left behind:\n%s", content)
			}
			if !gjson.Get(content, "keep").Exists() {
				t.Fatalf("user key lost:\n%s", content)
			}
		})
	}
}

func TestUninstallKeepsSiblingInnerHook(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "gemini-cli"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			s := specs[slices.IndexFunc(specs, func(s spec) bool { return s.name == name })]
			path := filepath.Join(home, s.file)
			write(t, path, `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[
  {"type":"command","command":"echo before"},
  {"type":"command","command":"/old/skillcrew sync --hook"},
  {"type":"command","command":"echo after"}]}]}}`)
			a := get(t, name)
			if ok, err := a.HookInstalled(home); err != nil || !ok {
				t.Fatalf("HookInstalled = %v, %v", ok, err)
			}
			if err := a.UninstallHook(home); err != nil {
				t.Fatal(err)
			}
			content := read(t, path)
			got := gjson.Get(content, "hooks.SessionStart.0.hooks.#.command").Raw
			if compact(got) != `["echo before","echo after"]` {
				t.Fatalf("inner hooks = %s\n%s", got, content)
			}
			if n := gjson.Get(content, "hooks.SessionStart.#").Int(); n != 1 {
				t.Fatalf("groups = %d", n)
			}
		})
	}
}

func TestUninstallAbsent(t *testing.T) {
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			a := get(t, s.name)
			if err := a.UninstallHook(home); err != nil {
				t.Fatalf("uninstall without file: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, s.file)); !os.IsNotExist(err) {
				t.Fatal("uninstall created a file")
			}
			if !s.shared {
				return
			}
			path := filepath.Join(home, s.file)
			write(t, path, s.foreign)
			if err := a.UninstallHook(home); err != nil {
				t.Fatal(err)
			}
			if read(t, path) != s.foreign {
				t.Fatal("uninstall rewrote a file without our hook")
			}
		})
	}
}

func TestEmptyFile(t *testing.T) {
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, s.file)
			write(t, path, "  \n")
			if err := get(t, s.name).InstallHook(home, bin); err != nil {
				t.Fatal(err)
			}
			if got := ours(t, s, read(t, path)); len(got) != 1 {
				t.Fatalf("commands = %v", got)
			}
		})
	}
}

func TestInvalidJSONUntouched(t *testing.T) {
	for _, s := range specs {
		for _, bad := range []string{`{"hooks": `, `[1, 2]`} {
			t.Run(s.name, func(t *testing.T) {
				home := t.TempDir()
				path := filepath.Join(home, s.file)
				write(t, path, bad)
				a := get(t, s.name)
				if err := a.InstallHook(home, bin); err == nil {
					t.Error("install: want error")
				}
				if _, err := a.HookInstalled(home); err == nil {
					t.Error("HookInstalled: want error")
				}
				if s.shared {
					if err := a.UninstallHook(home); err == nil {
						t.Error("uninstall: want error")
					}
				}
				if _, err := os.Stat(path); err == nil && read(t, path) != bad {
					t.Fatalf("file changed: %s", read(t, path))
				}
				entries, _ := os.ReadDir(filepath.Dir(path))
				for _, e := range entries {
					if strings.HasPrefix(e.Name(), ".") {
						t.Errorf("temp file left: %s", e.Name())
					}
				}
			})
		}
	}
}

// TestInstallFollowsSymlink keeps a hook file symlinked from a dotfiles
// repository a symlink, and writes the hook into its target.
func TestInstallFollowsSymlink(t *testing.T) {
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			target := filepath.Join(t.TempDir(), "dotfiles", filepath.Base(s.file))
			write(t, target, "{}")
			path := filepath.Join(home, s.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if err := get(t, s.name).InstallHook(home, bin); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("the symlink was replaced by a file")
			}
			if got := ours(t, s, read(t, target)); len(got) != 1 {
				t.Fatalf("commands in target = %v", got)
			}
		})
	}
}

// Another tool's "sync --hook" command is not Skillcrew's.
func TestForeignSyncHookIsKept(t *testing.T) {
	const theirs = "/usr/local/bin/other-tool sync --hook"
	for _, s := range specs {
		if !s.shared {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, s.file)
			list := strings.Split(strings.TrimPrefix(s.cmds, "hooks."), ".")[0]
			entry := `{"command":"` + theirs + `"}`
			if strings.Contains(s.cmds, ".hooks.#") {
				entry = `{"hooks":[{"type":"command","command":"` + theirs + `"}]}`
			}
			original := `{"hooks":{"` + list + `":[` + entry + `]}}`
			write(t, path, original)
			a := get(t, s.name)
			if ok, err := a.HookInstalled(home); err != nil || ok {
				t.Fatalf("HookInstalled = %v, %v", ok, err)
			}
			if err := a.UninstallHook(home); err != nil {
				t.Fatal(err)
			}
			if read(t, path) != original {
				t.Fatalf("uninstall changed another tool's hook:\n%s", read(t, path))
			}
			if err := a.InstallHook(home, bin); err != nil {
				t.Fatal(err)
			}
			if err := a.UninstallHook(home); err != nil {
				t.Fatal(err)
			}
			if content := read(t, path); !strings.Contains(content, theirs) || strings.Contains(content, bin) {
				t.Fatalf("only our hook must be removed:\n%s", content)
			}
		})
	}
}
