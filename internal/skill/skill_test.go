package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yoanbernabeu/skillcrew/internal/skill"
)

const validSkill = `---
name: security-review
description: Review code for security issues.
---

# Security review
`

func TestParseFrontmatter(t *testing.T) {
	fm, err := skill.ParseFrontmatter([]byte(validSkill))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Name != "security-review" || fm.Description != "Review code for security issues." {
		t.Fatalf("unexpected frontmatter: %+v", fm)
	}
}

func TestParseFrontmatterErrors(t *testing.T) {
	cases := map[string]string{
		"missing":      "# no frontmatter\n",
		"unterminated": "---\nname: x\n",
		"invalid yaml": "---\nname: [\n---\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := skill.ParseFrontmatter([]byte(content)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	valid := []string{"a", "pdf", "security-review", "v2-tools", strings.Repeat("a", 64)}
	for _, n := range valid {
		if err := skill.ValidateName(n); err != nil {
			t.Errorf("%q should be valid: %v", n, err)
		}
	}
	invalid := []string{"", "Upper", "under_score", "-lead", "trail-", "dou--ble", "sp ace", "a/b", strings.Repeat("a", 65)}
	for _, n := range invalid {
		if err := skill.ValidateName(n); err == nil {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestValidate(t *testing.T) {
	fsys := fstest.MapFS{
		"ok/security-review/SKILL.md": {Data: []byte(validSkill)},
		"bad/other-name/SKILL.md":     {Data: []byte(validSkill)},
		"nodesc/x/SKILL.md":           {Data: []byte("---\nname: x\n---\n")},
		"empty/x/README.md":           {Data: []byte("hi")},
	}
	if errs := skill.Validate(fsys, "ok/security-review"); len(errs) != 0 {
		t.Fatalf("expected valid skill, got %v", errs)
	}
	for _, dir := range []string{"bad/other-name", "nodesc/x", "empty/x"} {
		if errs := skill.Validate(fsys, dir); len(errs) == 0 {
			t.Errorf("%s: expected validation errors", dir)
		}
	}
}

func TestRenameFrontmatter(t *testing.T) {
	out, err := skill.RenameFrontmatter([]byte(validSkill), "acme-review")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(validSkill, "name: security-review", "name: acme-review", 1)
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if _, err := skill.RenameFrontmatter([]byte("---\ndescription: d\n---\n"), "x"); err == nil {
		t.Fatal("expected an error when name is missing")
	}
}

func TestHashDir(t *testing.T) {
	base := fstest.MapFS{
		"s/SKILL.md":       {Data: []byte("a")},
		"s/scripts/run.sh": {Data: []byte("b"), Mode: 0o755},
	}
	h1, err := skill.HashDir(base, "s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h1, "sha256:") {
		t.Fatalf("unexpected hash format %q", h1)
	}
	same := fstest.MapFS{
		"other/SKILL.md":       {Data: []byte("a")},
		"other/scripts/run.sh": {Data: []byte("b"), Mode: 0o755},
	}
	if h2, _ := skill.HashDir(same, "other"); h2 != h1 {
		t.Fatal("hash must not depend on the root name")
	}
	variants := []fstest.MapFS{
		{"s/SKILL.md": {Data: []byte("a")}, "s/scripts/run.sh": {Data: []byte("c"), Mode: 0o755}},
		{"s/SKILL.md": {Data: []byte("a")}, "s/scripts/run.sh": {Data: []byte("b"), Mode: 0o644}},
		{"s/SKILL.md": {Data: []byte("a")}, "s/scripts/other.sh": {Data: []byte("b"), Mode: 0o755}},
	}
	for i, v := range variants {
		if h, _ := skill.HashDir(v, "s"); h == h1 {
			t.Errorf("variant %d should change the hash", i)
		}
	}
}

func TestHashDirRootIndependent(t *testing.T) {
	nested := fstest.MapFS{"s/SKILL.md": {Data: []byte("a")}}
	flat := fstest.MapFS{"SKILL.md": {Data: []byte("a")}}
	h1, _ := skill.HashDir(nested, "s")
	h2, _ := skill.HashDir(flat, ".")
	if h1 != h2 {
		t.Fatalf("hash depends on the root: %s != %s", h1, h2)
	}
}

func TestHashDirDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := skill.HashDir(os.DirFS(dir), ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "notes")); err != nil {
		t.Fatal(err)
	}
	h2, err := skill.HashDir(os.DirFS(dir), ".")
	if err != nil {
		t.Fatalf("a symlink to a directory must be hashed, not read: %v", err)
	}
	if h1 == h2 {
		t.Fatal("adding a symlink must change the hash")
	}
}

func TestCopyAs(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(validSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "acme-review")
	if err := skill.CopyAs(src, dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if !strings.Contains(string(b), "name: acme-review") {
		t.Fatalf("not renamed: %s", b)
	}
	if err := skill.CopyAs(src, filepath.Join(t.TempDir(), "Bad_Name")); err == nil {
		t.Fatal("an invalid target name must fail")
	}
}

func TestHashDirIgnoresFinderMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, _ := skill.HashDir(os.DirFS(dir), ".")
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("finder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h2, _ := skill.HashDir(os.DirFS(dir), "."); h1 != h2 {
		t.Fatal("browsing a skill in the Finder must not count as an edit")
	}
}

func TestDescriptionLimitCountsCharacters(t *testing.T) {
	desc := strings.Repeat("é", 1000) // 2000 bytes, 1000 characters
	fsys := fstest.MapFS{"s/SKILL.md": {Data: []byte("---\nname: s\ndescription: " + desc + "\n---\n")}}
	if errs := skill.Validate(fsys, "s"); len(errs) != 0 {
		t.Fatalf("1000 characters are allowed: %v", errs)
	}
}
