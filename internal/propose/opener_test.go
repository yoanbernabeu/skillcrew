package propose

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCLI struct {
	tools map[string]bool
	out   string
	err   error
	name  string
	args  []string
}

func (f *fakeCLI) opener() CLIOpener {
	return CLIOpener{
		LookPath: func(name string) (string, error) {
			if f.tools[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			f.name, f.args = name, args
			return f.out, f.err
		},
	}
}

func TestParseRemote(t *testing.T) {
	cases := map[string][2]string{
		"https://github.com/acme/skills.git":          {"github.com", "acme/skills"},
		"https://user:tok@github.com/acme/skills":     {"github.com", "acme/skills"},
		"git@github.com:acme/skills.git":              {"github.com", "acme/skills"},
		"ssh://git@gitlab.example.com/g/sub/repo.git": {"gitlab.example.com", "g/sub/repo"},
		"ssh://git@gitlab.example.com:2222/g/r":       {"gitlab.example.com", "g/r"},
	}
	for in, want := range cases {
		host, p, err := parseRemote(in)
		if err != nil || host != want[0] || p != want[1] {
			t.Errorf("parseRemote(%q) = %q, %q, %v; want %v", in, host, p, err, want)
		}
	}
	for _, bad := range []string{"/local/path", "https://github.com/", "nonsense"} {
		if _, _, err := parseRemote(bad); err == nil {
			t.Errorf("parseRemote(%q) must fail", bad)
		}
	}
}

func TestCLIOpenerGitHub(t *testing.T) {
	f := &fakeCLI{tools: map[string]bool{"gh": true}, out: "Creating pull request...\nhttps://github.com/acme/skills/pull/7\n"}
	url, err := f.opener().Open(context.Background(), "git@github.com:acme/skills.git", "main", "skillcrew/x", "T", "B")
	if err != nil || url != "https://github.com/acme/skills/pull/7" {
		t.Fatalf("url = %q, %v", url, err)
	}
	want := "pr create --repo github.com/acme/skills --base main --head skillcrew/x --title T --body B"
	if f.name != "gh" || strings.Join(f.args, " ") != want {
		t.Fatalf("ran %s %v", f.name, f.args)
	}
}

func TestCLIOpenerGitLab(t *testing.T) {
	f := &fakeCLI{tools: map[string]bool{"glab": true}, out: "Creating merge request for x into main\n!3 T (x)\n https://gitlab.example.com/g/sub/repo/-/merge_requests/3\n"}
	url, err := f.opener().Open(context.Background(), "https://gitlab.example.com/g/sub/repo.git", "main", "x", "T", "B")
	if err != nil || url != "https://gitlab.example.com/g/sub/repo/-/merge_requests/3" {
		t.Fatalf("url = %q, %v", url, err)
	}
	want := "mr create --repo gitlab.example.com/g/sub/repo --source-branch x --target-branch main --title T --description B --yes"
	if f.name != "glab" || strings.Join(f.args, " ") != want {
		t.Fatalf("ran %s %v", f.name, f.args)
	}
}

func TestCLIOpenerToolFailure(t *testing.T) {
	f := &fakeCLI{tools: map[string]bool{"gh": true}, err: errors.New("auth required")}
	if _, err := f.opener().Open(context.Background(), "https://github.com/acme/skills", "main", "x", "T", "B"); err == nil || !strings.Contains(err.Error(), "auth required") {
		t.Fatalf("err = %v", err)
	}
}

func TestCLIOpenerWithoutTool(t *testing.T) {
	f := &fakeCLI{}
	_, err := f.opener().Open(context.Background(), "https://github.com/acme/skills.git", "main", "skillcrew/x", "T", "B")
	if err == nil || !strings.Contains(err.Error(), "https://github.com/acme/skills/compare/main...skillcrew/x?expand=1") {
		t.Fatalf("err = %v", err)
	}
	_, err = f.opener().Open(context.Background(), "https://git.example.com/a/b.git", "main", "skillcrew/x", "T", "B")
	if err == nil || !strings.Contains(err.Error(), "git.example.com") || !strings.Contains(err.Error(), "skillcrew/x") {
		t.Fatalf("err = %v", err)
	}
	if f.name != "" {
		t.Fatal("no tool must run")
	}
}

func TestCLIOpenerUnparsableRemote(t *testing.T) {
	f := &fakeCLI{tools: map[string]bool{"gh": true}}
	if _, err := f.opener().Open(context.Background(), "/srv/registry", "main", "x", "T", "B"); err == nil || !strings.Contains(err.Error(), "x") {
		t.Fatalf("err = %v", err)
	}
}
