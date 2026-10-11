package install_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/install"
	"github.com/yoanbernabeu/skillcrew/internal/paths"
	"github.com/yoanbernabeu/skillcrew/internal/state"
)

type env struct {
	t     *testing.T
	paths paths.Paths
	in    *install.Installer
	lock  state.Lock
}

func newEnv(t *testing.T, withClaude bool) *env {
	t.Helper()
	p := paths.Paths{Home: t.TempDir()}
	if withClaude {
		mkdir(t, filepath.Join(p.Home, ".claude"))
	}
	in := install.New(p)
	clock := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	in.Now = func() time.Time { clock = clock.Add(time.Second); return clock }
	return &env{t: t, paths: p, in: in, lock: state.Lock{Skills: map[string]state.LockedSkill{}}}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// source creates a skill directory as exported from the registry.
func (e *env) source(name, body string) string {
	dir := filepath.Join(e.t.TempDir(), name)
	writeFile(e.t, filepath.Join(dir, "SKILL.md"), "---\nname: "+name+"\ndescription: d\n---\n"+body)
	return dir
}

func (e *env) apply(items ...install.Item) install.Report {
	e.t.Helper()
	lock, report, err := e.in.Apply(context.Background(), e.lock, items)
	if err != nil {
		e.t.Fatalf("apply: %v", err)
	}
	e.lock = lock
	return report
}

func (e *env) skillDir(name string) string { return filepath.Join(e.paths.AgentsSkills(), name) }
func (e *env) claudeLink(name string) string {
	return filepath.Join(e.paths.ClaudeSkills(), name)
}

func (e *env) backups(name string) []string {
	entries, _ := os.ReadDir(filepath.Join(e.paths.Backups(), name))
	var out []string
	for _, d := range entries {
		out = append(out, filepath.Join(e.paths.Backups(), name, d.Name()))
	}
	return out
}

func item(id, name, source string) install.Item {
	return install.Item{ID: id, Name: name, Commit: "c1", Source: source}
}

func TestFreshInstall(t *testing.T) {
	e := newEnv(t, true)
	r := e.apply(item("p:review", "review", e.source("review", "v1")))

	if !slices.Equal(r.Installed, []string{"review"}) {
		t.Fatalf("report: %+v", r)
	}
	if got := readFile(t, filepath.Join(e.skillDir("review"), "SKILL.md")); !strings.HasSuffix(got, "v1") {
		t.Fatalf("content: %q", got)
	}
	target, err := os.Readlink(e.claudeLink("review"))
	if err != nil || target != e.skillDir("review") {
		t.Fatalf("claude symlink: %q %v", target, err)
	}
	l := e.lock.Skills["review"]
	if l.ID != "p:review" || l.Commit != "c1" || l.Hash == "" || l.Hash != l.SourceHash {
		t.Fatalf("lock: %+v", l)
	}
}

func TestNoClaudeDirectoryMeansNoSymlink(t *testing.T) {
	e := newEnv(t, false)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	if _, err := os.Lstat(e.claudeLink("review")); !os.IsNotExist(err) {
		t.Fatalf("symlink should not exist: %v", err)
	}
}

func TestUnchangedAndUpdate(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("p:review", "review", e.source("review", "v1")))

	if r := e.apply(item("p:review", "review", e.source("review", "v1"))); len(r.Installed)+len(r.Updated)+len(r.Removed)+len(r.Notices) != 0 {
		t.Fatalf("expected no change, got %+v", r)
	}
	r := e.apply(install.Item{ID: "p:review", Name: "review", Commit: "c2", Source: e.source("review", "v2")})
	if !slices.Equal(r.Updated, []string{"review"}) {
		t.Fatalf("report: %+v", r)
	}
	if got := readFile(t, filepath.Join(e.skillDir("review"), "SKILL.md")); !strings.HasSuffix(got, "v2") {
		t.Fatalf("content: %q", got)
	}
	if e.lock.Skills["review"].Commit != "c2" {
		t.Fatalf("lock not updated: %+v", e.lock.Skills["review"])
	}
	if len(e.backups("review")) != 0 {
		t.Fatal("a clean update must not create a backup")
	}
}

func TestLocalEditIsBackedUpAndRestored(t *testing.T) {
	e := newEnv(t, true)
	src := e.source("review", "v1")
	e.apply(item("p:review", "review", src))
	writeFile(t, filepath.Join(e.skillDir("review"), "SKILL.md"), "edited")

	if c := e.in.Check(e.lock)["review"]; c != install.Modified {
		t.Fatalf("check: %v", c)
	}
	r := e.apply(item("p:review", "review", src))
	if len(r.Notices) != 1 || !strings.Contains(r.Notices[0], "local changes") {
		t.Fatalf("notices: %v", r.Notices)
	}
	if got := readFile(t, filepath.Join(e.skillDir("review"), "SKILL.md")); !strings.HasSuffix(got, "v1") {
		t.Fatalf("team version not restored: %q", got)
	}
	b := e.backups("review")
	if len(b) != 1 || readFile(t, filepath.Join(b[0], "SKILL.md")) != "edited" {
		t.Fatalf("backup: %v", b)
	}
	if latest, ok := e.in.LatestBackup("review"); !ok || latest != b[0] {
		t.Fatalf("latest backup: %q %v", latest, ok)
	}
}

func TestPersonalSkillCollision(t *testing.T) {
	e := newEnv(t, true)
	writeFile(t, filepath.Join(e.skillDir("review"), "SKILL.md"), "mine")
	writeFile(t, filepath.Join(e.claudeLink("review"), "SKILL.md"), "mine in claude")
	writeFile(t, filepath.Join(e.skillDir("personal"), "SKILL.md"), "untouched")

	r := e.apply(item("p:review", "review", e.source("review", "v1")))
	if len(r.Notices) != 2 {
		t.Fatalf("expected one notice per backed up copy, got %v", r.Notices)
	}
	if target, _ := os.Readlink(e.claudeLink("review")); target != e.skillDir("review") {
		t.Fatalf("claude entry not replaced by the symlink: %q", target)
	}
	if len(e.backups("review")) != 2 {
		t.Fatalf("backups: %v", e.backups("review"))
	}
	if readFile(t, filepath.Join(e.skillDir("personal"), "SKILL.md")) != "untouched" {
		t.Fatal("unrelated personal skill modified")
	}
}

func TestRemoval(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("p:review", "review", e.source("review", "v1")), item("p:old", "old", e.source("old", "v1")))
	writeFile(t, filepath.Join(e.skillDir("personal"), "SKILL.md"), "mine")

	r := e.apply(item("p:review", "review", e.source("review", "v1")))
	if !slices.Equal(r.Removed, []string{"old"}) {
		t.Fatalf("report: %+v", r)
	}
	for _, p := range []string{e.skillDir("old"), e.claudeLink("old")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed", p)
		}
	}
	if _, ok := e.lock.Skills["old"]; ok {
		t.Fatal("lock still lists the removed skill")
	}
	if _, err := os.Stat(e.skillDir("personal")); err != nil {
		t.Fatal("personal skill removed")
	}
}

func TestDeletedSkillIsReinstalled(t *testing.T) {
	e := newEnv(t, true)
	src := e.source("review", "v1")
	e.apply(item("p:review", "review", src))
	if err := os.RemoveAll(e.skillDir("review")); err != nil {
		t.Fatal(err)
	}
	if c := e.in.Check(e.lock)["review"]; c != install.Missing {
		t.Fatalf("check: %v", c)
	}
	r := e.apply(item("p:review", "review", src))
	if len(r.Notices) != 1 || !strings.Contains(r.Notices[0], "reinstalled") {
		t.Fatalf("notices: %v", r.Notices)
	}
	if c := e.in.Check(e.lock)["review"]; c != install.OK {
		t.Fatalf("check after sync: %v", c)
	}
}

func TestAliasRewritesName(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("q:review", "q-review", e.source("review", "v1")))
	got := readFile(t, filepath.Join(e.skillDir("q-review"), "SKILL.md"))
	if !strings.Contains(got, "name: q-review") {
		t.Fatalf("frontmatter not renamed: %q", got)
	}
	if l := e.lock.Skills["q-review"]; l.Hash == l.SourceHash {
		t.Fatal("aliased skill must have a different installed hash")
	}
	if r := e.apply(item("q:review", "q-review", e.source("review", "v1"))); len(r.Updated)+len(r.Notices) != 0 {
		t.Fatalf("aliased skill should be stable, got %+v", r)
	}
}

func TestInvalidSkillDoesNotBlockOthers(t *testing.T) {
	e := newEnv(t, true)
	bad := filepath.Join(t.TempDir(), "bad")
	writeFile(t, filepath.Join(bad, "SKILL.md"), "no frontmatter")

	lock, r, err := e.in.Apply(context.Background(), e.lock, []install.Item{item("p:bad", "bad", bad), item("p:good", "good", e.source("good", "v1"))})
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("expected an error about the bad skill, got %v", err)
	}
	if !slices.Equal(r.Installed, []string{"good"}) {
		t.Fatalf("report: %+v", r)
	}
	if _, ok := lock.Skills["bad"]; ok {
		t.Fatal("invalid skill must not be locked")
	}
	if _, err := os.Stat(e.skillDir("bad")); !os.IsNotExist(err) {
		t.Fatal("invalid skill must not be installed")
	}
}

func TestBackupRetention(t *testing.T) {
	e := newEnv(t, false)
	src := e.source("review", "v1")
	e.apply(item("p:review", "review", src))
	for i := 0; i < 7; i++ {
		writeFile(t, filepath.Join(e.skillDir("review"), "SKILL.md"), "edit")
		e.apply(item("p:review", "review", src))
	}
	if n := len(e.backups("review")); n != 5 {
		t.Fatalf("kept %d backups, want 5", n)
	}
}

func TestUninstall(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	writeFile(t, filepath.Join(e.skillDir("personal"), "SKILL.md"), "mine")
	if _, err := e.in.Uninstall(e.lock); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{e.skillDir("review"), e.claudeLink("review")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed", p)
		}
	}
	if _, err := os.Stat(e.skillDir("personal")); err != nil {
		t.Fatal("personal skill removed")
	}
}

func TestStagingIsCleanedUp(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	e.apply(item("p:review", "review", e.source("review", "v2")))
	entries, _ := os.ReadDir(e.paths.Staging())
	if len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
}

func TestKeptSkillsAreNotRemoved(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	lock, r, err := e.in.Apply(context.Background(), e.lock, nil, "review")
	if err != nil || len(r.Removed) != 0 {
		t.Fatalf("kept skill removed: %+v %v", r, err)
	}
	if _, ok := lock.Skills["review"]; !ok {
		t.Fatal("kept skill dropped from the lock")
	}
}

func TestLinkFailureKeepsOwnership(t *testing.T) {
	e := newEnv(t, true)
	// A file where the Claude skills directory should be makes linking fail.
	writeFile(t, e.paths.ClaudeSkills(), "not a directory")
	lock, r, _ := e.in.Apply(context.Background(), e.lock, []install.Item{item("p:review", "review", e.source("review", "v1"))})
	if _, ok := lock.Skills["review"]; !ok {
		t.Fatal("an installed skill must be locked even if linking failed")
	}
	if len(r.Notices) == 0 {
		t.Fatal("the link failure must be reported")
	}
}

func TestUnownedIdenticalSkillIsAdopted(t *testing.T) {
	e := newEnv(t, false)
	src := e.source("review", "v1")
	e.apply(item("p:review", "review", src))
	e.lock = state.Lock{Skills: map[string]state.LockedSkill{}} // lost lock
	r := e.apply(item("p:review", "review", src))
	if len(r.Notices) != 0 || len(e.backups("review")) != 0 {
		t.Fatalf("an identical copy must be adopted silently: %+v", r)
	}
	if _, ok := e.lock.Skills["review"]; !ok {
		t.Fatal("adopted skill not locked")
	}
}

func TestForeignClaudeSymlinkIsReported(t *testing.T) {
	e := newEnv(t, true)
	mkdir(t, e.paths.ClaudeSkills())
	if err := os.Symlink(t.TempDir(), e.claudeLink("review")); err != nil {
		t.Fatal(err)
	}
	r := e.apply(item("p:review", "review", e.source("review", "v1")))
	if len(r.Notices) != 1 || !strings.Contains(r.Notices[0], "symlink") {
		t.Fatalf("replacing a foreign symlink must be reported: %v", r.Notices)
	}
}

func TestRemovingEditedSkillBacksItUp(t *testing.T) {
	e := newEnv(t, false)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	writeFile(t, filepath.Join(e.skillDir("review"), "SKILL.md"), "edited")
	r := e.apply()
	if !slices.Equal(r.Removed, []string{"review"}) || len(r.Notices) != 1 || len(e.backups("review")) != 1 {
		t.Fatalf("report: %+v backups: %v", r, e.backups("review"))
	}
}

func TestPersonalBackupsAreNeverPruned(t *testing.T) {
	e := newEnv(t, false)
	writeFile(t, filepath.Join(e.skillDir("review"), "SKILL.md"), "my personal skill")
	src := e.source("review", "v1")
	e.apply(item("p:review", "review", src))
	for i := 0; i < 7; i++ {
		writeFile(t, filepath.Join(e.skillDir("review"), "SKILL.md"), "edit")
		e.apply(item("p:review", "review", src))
	}
	found := false
	for _, b := range e.backups("review") {
		found = found || readFile(t, filepath.Join(b, "SKILL.md")) == "my personal skill"
	}
	if !found {
		t.Fatal("the backup of a personal skill must never be pruned")
	}
	latest, ok := e.in.LatestBackup("review")
	if !ok || readFile(t, filepath.Join(latest, "SKILL.md")) != "edit" {
		t.Fatalf("LatestBackup must return the latest edit, got %q", latest)
	}
}

func TestLatestBackupIgnoresPersonalSkills(t *testing.T) {
	e := newEnv(t, false)
	writeFile(t, filepath.Join(e.skillDir("review"), "SKILL.md"), "mine")
	e.apply(item("p:review", "review", e.source("review", "v1")))
	if b, ok := e.in.LatestBackup("review"); ok {
		t.Fatalf("a personal skill backup is not a proposal source: %s", b)
	}
}

func TestSymlinkInInstalledSkillIsRepaired(t *testing.T) {
	e := newEnv(t, false)
	src := e.source("review", "v1")
	e.apply(item("p:review", "review", src))
	if err := os.Symlink(t.TempDir(), filepath.Join(e.skillDir("review"), "notes")); err != nil {
		t.Fatal(err)
	}
	if c := e.in.Check(e.lock)["review"]; c != install.Modified {
		t.Fatalf("check: %v", c)
	}
	r := e.apply(item("p:review", "review", src))
	if len(r.Notices) != 1 {
		t.Fatalf("notices: %v", r.Notices)
	}
	if _, err := os.Lstat(filepath.Join(e.skillDir("review"), "notes")); !os.IsNotExist(err) {
		t.Fatal("the team version must be restored")
	}
}

func TestInterruptedUpdateIsAdoptedSilently(t *testing.T) {
	e := newEnv(t, false)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	stale := e.lock
	v2 := install.Item{ID: "p:review", Name: "review", Commit: "c2", Source: e.source("review", "v2")}
	e.apply(v2)
	e.lock = stale // the lock of the update was never saved
	r := e.apply(v2)
	if len(r.Notices) != 0 || len(e.backups("review")) != 0 {
		t.Fatalf("an already applied update is not a local edit: %+v", r)
	}
}

func TestLeftoverStagingIsCleaned(t *testing.T) {
	e := newEnv(t, false)
	writeFile(t, filepath.Join(e.paths.Staging(), "review-123", "review", "SKILL.md"), "half written")
	e.apply(item("p:review", "review", e.source("review", "v1")))
	entries, _ := os.ReadDir(e.paths.Staging())
	if len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
}

func TestInvalidInstallNameIsRejected(t *testing.T) {
	e := newEnv(t, false)
	for _, name := range []string{"..", "a/b", "Bad"} {
		_, _, err := e.in.Apply(context.Background(), e.lock, []install.Item{item("p:review", name, e.source("review", "v1"))})
		if err == nil {
			t.Errorf("install name %q must be rejected", name)
		}
	}
	entries, _ := os.ReadDir(e.paths.Staging())
	if len(entries) != 0 {
		t.Fatalf("rejected names must not leave files: %v", entries)
	}
}

func TestProvidedSourceHashAndIDRefresh(t *testing.T) {
	e := newEnv(t, false)
	src := e.source("review", "v1")
	e.apply(install.Item{ID: "p:review", Name: "review", Commit: "c1", Source: src, SourceHash: "tree1"})
	if e.lock.Skills["review"].SourceHash != "tree1" {
		t.Fatalf("lock: %+v", e.lock.Skills["review"])
	}
	e.apply(install.Item{ID: "q:review", Name: "review", Commit: "c2", Source: src, SourceHash: "tree1"})
	if l := e.lock.Skills["review"]; l.ID != "q:review" || l.Commit != "c2" {
		t.Fatalf("fast path must refresh id and commit: %+v", l)
	}
}

func TestReservedNameIsRejected(t *testing.T) {
	e := newEnv(t, true)
	if _, _, err := e.in.Apply(context.Background(), e.lock, []install.Item{item("p:x", "synced", e.source("synced", "v1"))}); err == nil {
		t.Fatal("synced is reserved by Claude Code")
	}
}

func TestFailedUpdateKeepsOwnership(t *testing.T) {
	e := newEnv(t, false)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	before := e.lock.Skills["review"]
	bad := filepath.Join(t.TempDir(), "review")
	writeFile(t, filepath.Join(bad, "SKILL.md"), "no frontmatter")

	lock, _, err := e.in.Apply(context.Background(), e.lock, []install.Item{{ID: "p:review", Name: "review", Commit: "c2", Source: bad}})
	if err == nil {
		t.Fatal("an invalid update must fail")
	}
	if got, ok := lock.Skills["review"]; !ok || got != before {
		t.Fatalf("a failed update must keep the previous lock entry: %+v", got)
	}
	if got := readFile(t, filepath.Join(e.skillDir("review"), "SKILL.md")); !strings.HasSuffix(got, "v1") {
		t.Fatalf("installed copy changed: %q", got)
	}
	// Still owned: the next update is a clean update, not a personal backup.
	e.lock = lock
	r := e.apply(install.Item{ID: "p:review", Name: "review", Commit: "c3", Source: e.source("review", "v3")})
	if !slices.Equal(r.Updated, []string{"review"}) || len(r.Notices) != 0 || len(e.backups("review")) != 0 {
		t.Fatalf("the skill must still be owned: %+v", r)
	}
}

func TestFailedRemovalKeepsOwnership(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	e := newEnv(t, false)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	locked := filepath.Join(e.skillDir("review"), "private")
	mkdir(t, locked)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	lock, r, err := e.in.Apply(context.Background(), e.lock, nil)
	if err == nil || len(r.Removed) != 0 {
		t.Fatalf("removal must fail: %+v %v", r, err)
	}
	if got, ok := lock.Skills["review"]; !ok || got != e.lock.Skills["review"] {
		t.Fatalf("a skill that could not be removed must stay owned: %+v", lock.Skills)
	}
}

func TestSameSizeEditIsDetected(t *testing.T) {
	e := newEnv(t, false)
	src := e.source("review", "v1")
	e.apply(item("p:review", "review", src))
	file := filepath.Join(e.skillDir("review"), "SKILL.md")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(readFile(t, file), "v1", "v9", 1)
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	later := info.ModTime().Add(time.Hour)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	if c := e.in.Check(e.lock)["review"]; c != install.Modified {
		t.Fatalf("a same-size edit must be detected, got %v", c)
	}
	e.apply(item("p:review", "review", src))
	if got := readFile(t, file); !strings.HasSuffix(got, "v1") {
		t.Fatalf("team version not restored: %q", got)
	}
}

func TestUnchangedSkillIsNotRestaged(t *testing.T) {
	e := newEnv(t, true)
	src := e.source("review", "v1")
	e.apply(item("p:review", "review", src))
	before, err := os.Stat(filepath.Join(e.skillDir("review"), "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	e.apply(item("p:review", "review", src))
	if _, err := os.Stat(e.paths.Staging()); !os.IsNotExist(err) {
		t.Fatalf("an unchanged skill must not be staged again: %v", err)
	}
	after, err := os.Stat(filepath.Join(e.skillDir("review"), "SKILL.md"))
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("an unchanged skill must not be rewritten")
	}
}

func TestClaudeBackupsAreTaggedAndNoticeNamesTheSkill(t *testing.T) {
	e := newEnv(t, true)
	writeFile(t, filepath.Join(e.claudeLink("review"), "SKILL.md"), "mine in claude")
	e.apply(item("p:review", "review", e.source("review", "v1")))
	b := e.backups("review")
	if len(b) != 1 || !strings.HasSuffix(b[0], "-personal-claude") {
		t.Fatalf("a Claude Code personal skill must be tagged for restoration there: %v", b)
	}
	writeFile(t, filepath.Join(e.skillDir("review"), "SKILL.md"), "edited")
	r := e.apply(item("p:review", "review", e.source("review", "v1")))
	if len(r.Notices) != 1 || !strings.Contains(r.Notices[0], `skillcrew propose review`) {
		t.Fatalf("the notice must name the command: %v", r.Notices)
	}
}

// Past the deadline (an agent hook's budget), no new install starts: the
// skills keep their previous state, stay owned, and are reported as
// deferred to the next sync rather than as errors.
func TestApplyDefersInstallsPastTheDeadline(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("p:review", "review", e.source("review", "v1")), item("p:same", "same", e.source("same", "v1")))
	done, cancel := context.WithCancel(context.Background())
	cancel()
	lock, r, err := e.in.Apply(done, e.lock, []install.Item{
		item("p:review", "review", e.source("review", "v2")),
		item("p:same", "same", e.source("same", "v1")),
		item("p:new", "new", e.source("new", "v1")),
	})
	if err != nil {
		t.Fatalf("deferring is not an error: %v", err)
	}
	if !slices.Equal(r.Deferred, []string{"review", "new"}) || len(r.Installed)+len(r.Updated) != 0 {
		t.Fatalf("report: %+v", r)
	}
	if got := readFile(t, filepath.Join(e.skillDir("review"), "SKILL.md")); !strings.HasSuffix(got, "v1") {
		t.Fatalf("a deferred update must keep the installed version, got %q", got)
	}
	if lock.Skills["review"] != e.lock.Skills["review"] || lock.Skills["same"] != e.lock.Skills["same"] {
		t.Fatalf("deferred and unchanged skills must keep their lock entry: %+v", lock.Skills)
	}
	if _, err := os.Stat(e.skillDir("new")); !os.IsNotExist(err) {
		t.Fatal("a new skill must not be installed past the deadline")
	}
	if _, ok := lock.Skills["new"]; ok {
		t.Fatal("a deferred new skill must not be locked")
	}
}

// Removals (e.g. a skill blocked by the kill switch) still happen past the
// deadline: they are a rename.
func TestApplyRemovesPastTheDeadline(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	done, cancel := context.WithCancel(context.Background())
	cancel()
	lock, r, err := e.in.Apply(done, e.lock, nil)
	if err != nil || !slices.Equal(r.Removed, []string{"review"}) || len(lock.Skills) != 0 {
		t.Fatalf("removal: %+v %+v %v", r, lock, err)
	}
	if _, err := os.Stat(e.skillDir("review")); !os.IsNotExist(err) {
		t.Fatal("the skill must be gone from the skills directory")
	}
	e.lock = lock
	e.apply()
	if entries, _ := os.ReadDir(e.paths.Staging()); len(entries) != 0 {
		t.Fatalf("the next apply must delete what was set aside: %v", entries)
	}
}

// An installed copy whose metadata stamp matches the lock is not hashed:
// the lock's hash stands for its content. A wrong hash in the lock reveals
// whether the copy was hashed (it would look edited and be backed up).
func TestUpdateTrustsTheStamp(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	locked := e.lock.Skills["review"]
	locked.Hash = "sha256:not-the-content"
	e.lock.Skills["review"] = locked
	r := e.apply(item("p:review", "review", e.source("review", "v2")))
	if !slices.Equal(r.Updated, []string{"review"}) || len(e.backups("review")) != 0 {
		t.Fatalf("an untouched copy must be updated without hashing it: %+v, backups %v", r, e.backups("review"))
	}
}

func TestUninstallTrustsTheStamp(t *testing.T) {
	e := newEnv(t, true)
	e.apply(item("p:review", "review", e.source("review", "v1")))
	locked := e.lock.Skills["review"]
	locked.Hash = "sha256:not-the-content"
	e.lock.Skills["review"] = locked
	if _, err := e.in.Uninstall(e.lock); err != nil {
		t.Fatal(err)
	}
	if len(e.backups("review")) != 0 {
		t.Fatalf("an untouched copy must be removed without hashing it: backups %v", e.backups("review"))
	}
}
