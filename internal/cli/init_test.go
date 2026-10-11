package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestRefuseCredentials(t *testing.T) {
	for _, tc := range []struct {
		url     string
		refused bool
	}{
		{"https://github.com/acme/skills.git", false},
		{"https://bob:s3cret@github.com/acme/skills.git", true},
		{"https://ghp_TOKEN@github.com/acme/skills.git", true},
		{"http://token@example.com/skills.git", true},
		{"HTTPS://token@example.com/skills.git", true},
		{"ssh://git@github.com/acme/skills.git", false},
		{"git@github.com:acme/skills.git", false},
		{"/srv/git/skills", false},
	} {
		if err := checkURL(tc.url); (err != nil) != tc.refused {
			t.Errorf("checkURL(%q) = %v, refused want %v", tc.url, err, tc.refused)
		}
	}
}

func TestRefusePlaintextTransports(t *testing.T) {
	for _, u := range []string{"http://example.com/acme/skills.git", "git://example.com/acme/skills.git"} {
		if err := checkURL(u); err == nil {
			t.Errorf("%s must be refused", u)
		}
	}
	for _, u := range []string{"https://github.com/acme/skills.git", "git@github.com:acme/skills.git", "ssh://git@host/x.git", "/srv/skills.git"} {
		if err := checkURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestAuthHint(t *testing.T) {
	for _, msg := range []string{
		"git ls-remote origin HEAD: fatal: could not read Username for 'https://github.com': terminal prompts disabled",
		"git@github.com: Permission denied (publickey). fatal: Could not read from remote repository.",
	} {
		hint := authHint(errors.New(msg))
		for _, want := range []string{"gh auth setup-git", "SSH", "docs/guide.md"} {
			if !strings.Contains(hint, want) {
				t.Errorf("hint for %q lacks %q: %q", msg, want, hint)
			}
		}
	}
	if hint := authHint(errors.New("fatal: repository not found")); hint != "" {
		t.Errorf("unexpected hint %q", hint)
	}
}

func TestSameRegistry(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{dir, "file://" + dir, true},
		{dir, dir + "/", true},
		{"https://github.com/acme/skills.git", "https://github.com/acme/skills", true},
		{"https://github.com/acme/skills.git", "https://github.com/acme/other.git", false},
		{dir, t.TempDir(), false},
	} {
		if got := sameRegistry(tc.a, tc.b); got != tc.same {
			t.Errorf("sameRegistry(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestSuggest(t *testing.T) {
	names := []string{"security:review", "review", "docs:pdf", "pdf"}
	if got := suggest("revew", names); len(got) == 0 || got[0] != "review" {
		t.Errorf("suggest = %v", got)
	}
	if got := suggest("security:", names); len(got) == 0 || got[0] != "security:review" {
		t.Errorf("suggest = %v", got)
	}
	if got := suggest("zzzzzz", names); len(got) != 0 {
		t.Errorf("suggest = %v", got)
	}
}
