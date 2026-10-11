package syncer_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/paths"
	"github.com/yoanbernabeu/skillcrew/internal/state"
	"github.com/yoanbernabeu/skillcrew/internal/syncer"
	tu "github.com/yoanbernabeu/skillcrew/internal/testutil"
)

const governance = `org: acme
skills:
  security:review: required
  security:secrets: recommended
  docs:pdf: available
`

type env struct {
	t      *testing.T
	origin *tu.Origin
	paths  paths.Paths
	sync   *syncer.Syncer
	now    time.Time
}

func newEnv(t *testing.T, cfg state.Config) *env {
	t.Helper()
	origin := tu.NewOrigin(t, map[string]string{
		".claude-plugin/marketplace.json":   tu.Marketplace("security", "docs"),
		"skillcrew.yaml":                    governance,
		tu.SkillPath("security", "review"):  tu.SkillMD("review", "v1"),
		tu.SkillPath("security", "secrets"): tu.SkillMD("secrets", "v1"),
		tu.SkillPath("docs", "pdf"):         tu.SkillMD("pdf", "v1"),
	})
	p := paths.Paths{Home: t.TempDir()}
	if _, err := gitx.Clone(context.Background(), origin.Dir, p.Registry()); err != nil {
		t.Fatal(err)
	}
	cfg.Registry, cfg.Ref = origin.Dir, "main"
	if err := cfg.Save(p.Config()); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, origin: origin, paths: p, now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	e.sync = syncer.New(p)
	e.sync.Now = func() time.Time { return e.now }
	return e
}

func (e *env) run(force bool) syncer.Result {
	e.t.Helper()
	res, err := e.sync.Sync(context.Background(), force)
	if err != nil {
		e.t.Fatalf("sync: %v", err)
	}
	return res
}

func (e *env) installed(name string) string {
	b, err := os.ReadFile(filepath.Join(e.paths.AgentsSkills(), name, "SKILL.md"))
	if err != nil {
		return ""
	}
	return string(b)
}

func TestFirstSyncInstallsTheResolvedSkills(t *testing.T) {
	e := newEnv(t, state.Config{})
	res := e.run(false)
	if res.Skipped || res.Commit == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.HasSuffix(e.installed("review"), "v1") || !strings.HasSuffix(e.installed("secrets"), "v1") {
		t.Fatal("required and recommended skills must be installed")
	}
	if e.installed("pdf") != "" {
		t.Fatal("available skills must not be installed")
	}
	lock, _ := state.LoadLock(e.paths.Lock())
	if lock.Commit != res.Commit || len(lock.Skills) != 2 {
		t.Fatalf("lock: %+v", lock)
	}
	st, _ := state.LoadState(e.paths.State())
	if !st.LastSync.Equal(e.now) || st.LastError != "" {
		t.Fatalf("state: %+v", st)
	}
}

func TestUserPreferences(t *testing.T) {
	e := newEnv(t, state.Config{Disabled: []string{"security:secrets"}, Installed: []string{"docs:pdf"}})
	e.run(false)
	if e.installed("secrets") != "" || e.installed("pdf") == "" {
		t.Fatal("preferences not applied")
	}
}

func TestDebounce(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "v2")})
	e.origin.Commit("v2")

	e.now = e.now.Add(30 * time.Second)
	if res := e.run(false); !res.Skipped {
		t.Fatal("a sync within the debounce window must be skipped")
	}
	if res := e.run(true); res.Skipped || !strings.HasSuffix(e.installed("review"), "v2") {
		t.Fatal("a forced sync must run")
	}
}

func TestUpdatesAndBlockingPropagate(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)

	e.origin.Write(map[string]string{
		tu.SkillPath("security", "review"): tu.SkillMD("review", "v2"),
		"skillcrew.yaml":                   strings.Replace(governance, "security:secrets: recommended", "security:secrets: blocked", 1),
	})
	e.origin.Commit("update")
	e.now = e.now.Add(2 * time.Minute)
	res := e.run(false)

	if !strings.HasSuffix(e.installed("review"), "v2") {
		t.Fatal("update not propagated")
	}
	if e.installed("secrets") != "" || len(res.Report.Removed) != 1 {
		t.Fatalf("blocked skill not removed: %+v", res.Report)
	}
}

func TestPinnedSkill(t *testing.T) {
	e := newEnv(t, state.Config{})
	first := e.origin.Git("rev-parse", "HEAD")
	e.origin.Write(map[string]string{
		tu.SkillPath("security", "review"): tu.SkillMD("review", "v2"),
		"skillcrew.yaml":                   strings.Replace(governance, "security:review: required", "security:review: {status: required, sha: "+first+"}", 1),
	})
	e.origin.Commit("pin")
	e.run(false)

	if !strings.HasSuffix(e.installed("review"), "v1") {
		t.Fatalf("pinned skill must come from the pinned commit, got %q", e.installed("review"))
	}
	lock, _ := state.LoadLock(e.paths.Lock())
	if lock.Skills["review"].Commit != first {
		t.Fatalf("lock commit: %+v", lock.Skills["review"])
	}
}

func TestOfflineStillRepairs(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	if err := os.RemoveAll(e.origin.Dir); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteFileAtomic(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(2 * time.Minute)
	res := e.run(false)

	if !strings.HasSuffix(e.installed("review"), "v1") {
		t.Fatal("local edits must be restored even offline")
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "offline") {
		t.Fatalf("expected an offline warning, got %v", res.Warnings)
	}
}

func TestInvalidRegistryKeepsInstalledSkills(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	e.origin.Write(map[string]string{"skillcrew.yaml": "org: acme\nskills:\n  security:review: mandatory\n"})
	e.origin.Commit("broken")
	e.now = e.now.Add(2 * time.Minute)

	if _, err := e.sync.Sync(context.Background(), false); err == nil {
		t.Fatal("expected an error")
	}
	if e.installed("review") == "" {
		t.Fatal("installed skills must survive a broken registry")
	}
	st, _ := state.LoadState(e.paths.State())
	if st.LastError == "" {
		t.Fatal("the error must be recorded in the state")
	}
}

func TestNotInitialized(t *testing.T) {
	s := syncer.New(paths.Paths{Home: t.TempDir()})
	if _, err := s.Sync(context.Background(), false); err == nil || !strings.Contains(err.Error(), "init") {
		t.Fatalf("expected a not initialized error, got %v", err)
	}
}

func TestConcurrentSyncIsSkipped(t *testing.T) {
	e := newEnv(t, state.Config{})
	unlock, ok, err := fsx.TryLock(e.paths.SyncLock())
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	defer unlock()
	if res := e.run(true); !res.Skipped {
		t.Fatal("a sync must be skipped while another one holds the lock")
	}
}

func TestCacheHoldsOnlyInstalledSkillVersions(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	oldReview := e.origin.Git("rev-parse", "HEAD:plugins/security/skills/review")
	e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "v2")})
	e.origin.Commit("v2")
	e.run(true)

	want := map[string]bool{
		e.origin.Git("rev-parse", "HEAD:plugins/security/skills/review"):  true,
		e.origin.Git("rev-parse", "HEAD:plugins/security/skills/secrets"): true,
	}
	entries, _ := os.ReadDir(e.paths.Cache())
	for _, entry := range entries {
		if !want[entry.Name()] || entry.Name() == oldReview {
			t.Fatalf("unexpected cache entry %s", entry.Name())
		}
	}
	if len(entries) != len(want) {
		t.Fatalf("cache: %v", entries)
	}
}

func TestNoticesAreKeptUntilShown(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	if err := os.WriteFile(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(2 * time.Minute)
	e.run(false)
	e.now = e.now.Add(2 * time.Minute)
	e.run(false) // a later sync with nothing to report
	st, _ := state.LoadState(e.paths.State())
	if len(st.Notices) != 1 || !strings.Contains(st.Notices[0], "backed up") {
		t.Fatalf("the backup notice must survive later syncs: %v", st.Notices)
	}
}

func TestFetchBackoff(t *testing.T) {
	e := newEnv(t, state.Config{})
	script := filepath.Join(t.TempDir(), "hang.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	if _, err := gitx.Open(e.paths.Registry()).Git(context.Background(), "config", "remote.origin.url", "ssh://example.invalid/x.git"); err != nil {
		t.Fatal(err)
	}
	e.sync.FetchTimeout, e.sync.FetchBackoff = 300*time.Millisecond, 10*time.Minute
	e.run(true)

	e.now = e.now.Add(2 * time.Minute)
	start := time.Now()
	res := e.run(false)
	if time.Since(start) > 200*time.Millisecond || !strings.Contains(strings.Join(res.Warnings, " "), "failed recently") {
		t.Fatalf("a sync within the backoff must not touch the network: %v after %s", res.Warnings, time.Since(start))
	}
}

func TestMovedTagIsAWarningNotOffline(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.origin.Git("tag", "v1")
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "security:review: required", "security:review: {status: required, ref: v1}", 1)})
	e.origin.Commit("pin to v1")
	e.run(false)
	e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "moved")})
	e.origin.Commit("move")
	e.origin.Git("tag", "-f", "v1")
	res := e.run(true)
	w := strings.Join(res.Warnings, " ")
	if strings.Contains(w, "offline") || !strings.Contains(w, "moved upstream") {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	if strings.HasSuffix(e.installed("review"), "moved") {
		t.Fatal("a moved tag must keep its first commit")
	}
}

func TestTagPin(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.origin.Git("tag", "review-v1")
	e.origin.Write(map[string]string{
		tu.SkillPath("security", "review"): tu.SkillMD("review", "v2"),
		"skillcrew.yaml":                   strings.Replace(governance, "security:review: required", "security:review: {status: required, ref: review-v1}", 1),
	})
	e.origin.Commit("pin tag")
	e.run(false)
	if !strings.HasSuffix(e.installed("review"), "v1") {
		t.Fatalf("tag pin not honored: %q", e.installed("review"))
	}
}

func TestFetchTimeoutFallsBackOffline(t *testing.T) {
	e := newEnv(t, state.Config{})
	script := filepath.Join(t.TempDir(), "hang.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	if _, err := gitx.Open(e.paths.Registry()).Git(context.Background(), "config", "remote.origin.url", "ssh://example.invalid/x.git"); err != nil {
		t.Fatal(err)
	}
	e.sync.FetchTimeout = 300 * time.Millisecond
	start := time.Now()
	res := e.run(true)
	if time.Since(start) > 5*time.Second || !strings.Contains(strings.Join(res.Warnings, " "), "offline") {
		t.Fatalf("expected a quick offline sync, got %v after %s", res.Warnings, time.Since(start))
	}
	if e.installed("review") == "" {
		t.Fatal("skills must be installed from the last fetched registry")
	}
}

func TestDeprecatedSkillStaysWhereInstalled(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "security:secrets: recommended", "security:secrets: {status: deprecated, message: old}", 1)})
	e.origin.Commit("deprecate")
	e.run(true)
	if e.installed("secrets") == "" {
		t.Fatal("deprecating a skill must not remove it from machines that have it")
	}
}

func TestSkillMissingAtPinnedCommitIsKept(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	first := e.origin.Git("rev-parse", "HEAD")
	e.origin.Write(map[string]string{
		tu.SkillPath("security", "added"): tu.SkillMD("added", "v1"),
		"skillcrew.yaml":                  governance + "  security:added: {status: required, sha: " + first + "}\n",
	})
	e.origin.Commit("pin a skill to a commit where it does not exist")
	e.now = e.now.Add(2 * time.Minute)
	if _, err := e.sync.Sync(context.Background(), false); err == nil || !strings.Contains(err.Error(), "security:added") {
		t.Fatalf("expected an error about the pinned skill, got %v", err)
	}
	if e.installed("review") == "" {
		t.Fatal("other skills must still be installed")
	}
}

func TestLogHasNoControlCharacters(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.origin.Write(map[string]string{".claude-plugin/marketplace.json": `{"name":"acme","plugins":[
		{"name":"security","source":"./plugins/security"},
		{"name":"docs","source":"./plugins/docs"},
		{"name":"evil","source":"./\u001b]0;PWNED\u0007"}]}`})
	e.origin.Commit("escape")
	e.run(false)
	b, _ := os.ReadFile(e.paths.Log())
	if strings.ContainsAny(string(b), "\x1b\x07") {
		t.Fatalf("control characters in the log: %q", b)
	}
}

func (e *env) state() state.State {
	e.t.Helper()
	st, err := state.LoadState(e.paths.State())
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e *env) saveState(st state.State) {
	e.t.Helper()
	if err := st.Save(e.paths.State()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) pin(spec string) {
	e.t.Helper()
	e.origin.Write(map[string]string{"skillcrew.yaml": strings.Replace(governance, "security:review: required", "security:review: "+spec, 1)})
	e.origin.Commit("pin review")
}

func TestInstalledSkillWithUnresolvablePinIsKept(t *testing.T) {
	tests := map[string]func(e *env){
		"deleted tag": func(e *env) {
			e.origin.Git("tag", "v1")
			e.pin("{status: required, ref: v1}")
			e.run(true)
			e.origin.Git("tag", "-d", "v1")
			if _, err := gitx.Open(e.paths.Registry()).Git(context.Background(), "update-ref", "-d", "refs/tags/v1"); err != nil {
				e.t.Fatal(err)
			}
		},
		"unknown commit": func(e *env) {
			e.run(true)
			e.pin("{status: required, sha: " + strings.Repeat("0123456789", 4) + "}")
		},
		"skill missing at the pinned commit": func(e *env) {
			e.run(true)
			e.origin.Git("rm", "-q", "-r", "plugins/security/skills/review")
			gone := e.origin.Commit("drop review")
			e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "v1")})
			e.pin("{status: required, sha: " + gone + "}")
		},
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, state.Config{})
			setup(e)
			if e.installed("review") == "" {
				t.Fatal("setup: review must be installed")
			}
			_, err := e.sync.Sync(context.Background(), true)
			if err == nil || !strings.Contains(err.Error(), "security:review") {
				t.Fatalf("expected an error about the pinned skill, got %v", err)
			}
			if !strings.HasSuffix(e.installed("review"), "v1") {
				t.Fatal("an installed skill whose pin cannot be resolved must stay installed")
			}
			lock, _ := state.LoadLock(e.paths.Lock())
			if _, ok := lock.Skills["review"]; !ok {
				t.Fatal("an installed skill whose pin cannot be resolved must stay owned")
			}
		})
	}
}

func TestFetchBackoffExpires(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failed  time.Duration // how long ago the last fetch failed
		fetched bool
	}{
		{"within the backoff", time.Minute, false},
		{"after the backoff", 11 * time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, state.Config{})
			e.sync.FetchBackoff = 10 * time.Minute
			e.run(true)
			e.saveState(state.State{LastSync: e.now, FetchFailedAt: e.now.Add(-tc.failed)})
			e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "v2")})
			e.origin.Commit("v2")
			e.run(true)

			if got := strings.HasSuffix(e.installed("review"), "v2"); got != tc.fetched {
				t.Fatalf("fetched = %v, want %v", got, tc.fetched)
			}
			if cleared := e.state().FetchFailedAt.IsZero(); cleared != tc.fetched {
				t.Fatalf("FetchFailedAt cleared = %v, want %v", cleared, tc.fetched)
			}
		})
	}
}

func TestLastSyncInTheFutureDoesNotBlockSyncs(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.saveState(state.State{LastSync: e.now.Add(24 * time.Hour)}) // clock moved back
	if res := e.run(false); res.Skipped {
		t.Fatal("a last sync in the future must not skip syncs")
	}
}

func TestNoticesKeepTheNewest(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	st := e.state()
	for i := range 60 {
		st.Notices = append(st.Notices, fmt.Sprintf("old notice %d", i))
	}
	e.saveState(st)
	if err := os.WriteFile(filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.run(true)
	notices := e.state().Notices
	if len(notices) != 50 || notices[0] != "old notice 11" || !strings.Contains(notices[49], "backed up") {
		t.Fatalf("expected the 50 newest notices, got %d: first %q, last %q", len(notices), notices[0], notices[len(notices)-1])
	}
}

func TestNullSkillsInLockfile(t *testing.T) {
	e := newEnv(t, state.Config{})
	if err := os.MkdirAll(filepath.Dir(e.paths.Lock()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.paths.Lock(), []byte(`{"commit":"","skills":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if lock, err := state.LoadLock(e.paths.Lock()); err != nil || lock.Skills == nil {
		t.Fatalf("null skills must load as an empty map: %+v %v", lock, err)
	}
	e.run(false)
	if e.installed("review") == "" {
		t.Fatal("sync failed with a null skills lockfile")
	}
}

func TestUnchangedSkillIsNotRestaged(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	file := filepath.Join(e.paths.AgentsSkills(), "review", "SKILL.md")
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	e.origin.Write(map[string]string{"unrelated.txt": "x"})
	e.origin.Commit("unrelated change")
	res := e.run(true)
	if r := res.Report; len(r.Installed)+len(r.Updated)+len(r.Removed)+len(r.Notices) != 0 {
		t.Fatalf("nothing to do expected: %+v", r)
	}
	if _, err := os.Stat(e.paths.Staging()); !os.IsNotExist(err) {
		t.Fatalf("an unchanged skill must not be staged again: %v", err)
	}
	after, err := os.Stat(file)
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("an unchanged skill must not be rewritten")
	}
}

// Hooks trust the tags already fetched (tags are immutable here): only the
// branch is fetched, and a tag moved upstream is noticed by manual and timer syncs.
func TestTrustLocalTagsSkipsTheTagCheck(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.origin.Git("tag", "v1")
	e.pin("{status: required, ref: v1}")
	e.run(true)
	e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "moved")})
	e.origin.Commit("move")
	e.origin.Git("tag", "-f", "v1")

	e.sync.TrustLocalTags = true
	commands := tu.CountGit(t)
	res := e.run(true)
	if n := tu.Network(commands()); len(n) != 2 {
		t.Fatalf("a hook sync must only fetch the branch (ls-remote and fetch), ran %v", n)
	}
	if w := strings.Join(res.Warnings, " "); strings.Contains(w, "moved") {
		t.Fatalf("a hook sync must not check tags upstream: %v", res.Warnings)
	}
	e.sync.TrustLocalTags = false
	if w := strings.Join(e.run(true).Warnings, " "); !strings.Contains(w, "moved upstream") {
		t.Fatalf("a manual sync must report the moved tag: %v", w)
	}
	if !strings.HasSuffix(e.installed("review"), "v1") {
		t.Fatal("a moved tag must keep its first commit")
	}
}

// A tag pinned for the first time is fetched even by a hook.
func TestTrustLocalTagsFetchesNewTags(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.sync.TrustLocalTags = true
	e.origin.Git("tag", "v1")
	e.origin.Write(map[string]string{tu.SkillPath("security", "review"): tu.SkillMD("review", "v2")})
	e.origin.Commit("v2")
	e.pin("{status: required, ref: v1}")
	e.run(true)
	if !strings.HasSuffix(e.installed("review"), "v1") {
		t.Fatalf("tag pin not honored: %q", e.installed("review"))
	}
}

// Resolving pins costs a fixed number of git commands, not a few per pin.
func TestPinsCostAFixedNumberOfGitCommands(t *testing.T) {
	const pins = 20
	var gov strings.Builder
	gov.WriteString("org: acme\nskills:\n")
	for i := range 2 * pins {
		fmt.Fprintf(&gov, "  p%d:s%d: required\n", i, i)
	}
	origin := tu.NewLargeOrigin(t, 2*pins, gov.String())
	origin.Git("reset", "-q", "--hard")
	first := origin.Git("rev-parse", "HEAD")
	origin.Git("tag", "v1")
	gov.Reset()
	gov.WriteString("org: acme\nskills:\n")
	for i := range pins {
		fmt.Fprintf(&gov, "  p%d:s%d: {status: required, sha: %s}\n", i, i, first)
		fmt.Fprintf(&gov, "  p%d:s%d: {status: required, ref: v1}\n", pins+i, pins+i)
	}
	origin.Write(map[string]string{"skillcrew.yaml": gov.String()})
	origin.Commit("pin everything")

	p := paths.Paths{Home: t.TempDir()}
	if _, err := gitx.Clone(context.Background(), origin.Dir, p.Registry()); err != nil {
		t.Fatal(err)
	}
	if err := (state.Config{Registry: origin.Dir, Ref: "main"}).Save(p.Config()); err != nil {
		t.Fatal(err)
	}
	s := syncer.New(p)
	s.TrustLocalTags = true
	if _, err := s.Sync(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if lock, _ := state.LoadLock(p.Lock()); len(lock.Skills) != 2*pins {
		t.Fatalf("%d skills installed, want %d", len(lock.Skills), 2*pins)
	}
	commands := tu.CountGit(t)
	if _, err := s.Sync(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	n := commands()
	t.Logf("%d git commands for a no-change sync of %d pinned skills", len(n), 2*pins)
	if len(n) > 20 {
		t.Fatalf("%d git commands for a no-change sync of %d pinned skills:\n%s", len(n), 2*pins, strings.Join(n, "\n"))
	}
}

// expireAfterExport returns a context that expires once skills have been
// exported (a git cat-file --batch ran), so the sync reaches the installs
// past its deadline. Its Done channel never closes: running git commands are
// not killed, as with a deadline passing between two steps.
func expireAfterExport(t *testing.T) context.Context {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "exported")
	script := "#!/bin/sh\ncase \"$*\" in *\"cat-file --batch\") touch '" + marker + "';; esac\nexec '" + gitBin + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return expiring{Context: context.Background(), marker: marker}
}

type expiring struct {
	context.Context
	marker string
}

func (c expiring) Done() <-chan struct{} { return make(chan struct{}) }

func (c expiring) Err() error {
	if _, err := os.Stat(c.marker); err == nil {
		return context.DeadlineExceeded
	}
	return nil
}

// Past the deadline (an agent hook's budget), no new install starts: skills
// keep their previous version, and the sync reports them as deferred, not
// as an error. The next sync finishes the job.
func TestDeadlineDefersInstalls(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.run(false)
	e.origin.Write(map[string]string{
		tu.SkillPath("security", "review"): tu.SkillMD("review", "v2"),
		"skillcrew.yaml":                   strings.Replace(governance, "docs:pdf: available", "docs:pdf: required", 1),
	})
	e.origin.Commit("v2 and pdf")
	before, _ := state.LoadLock(e.paths.Lock())

	res, err := e.sync.Sync(expireAfterExport(t), true)
	if err != nil {
		t.Fatalf("deferring is not an error: %v", err)
	}
	if w := strings.Join(res.Warnings, " "); !strings.Contains(w, "deferred") || !strings.Contains(w, "pdf") || !strings.Contains(w, "review") {
		t.Fatalf("the deferred skills must be reported: %v", res.Warnings)
	}
	if !strings.HasSuffix(e.installed("review"), "v1") || e.installed("pdf") != "" {
		t.Fatal("no install may start past the deadline")
	}
	lock, _ := state.LoadLock(e.paths.Lock())
	if lock.Skills["review"] != before.Skills["review"] {
		t.Fatalf("a deferred skill keeps its lock entry: %+v", lock.Skills["review"])
	}
	if st := e.state(); st.LastError != "" {
		t.Fatalf("deferring is not an error: %q", st.LastError)
	}

	res = e.run(true)
	if !strings.HasSuffix(e.installed("review"), "v2") || e.installed("pdf") == "" {
		t.Fatalf("the next sync must finish the job: %+v", res)
	}
}

// Right after init has fetched the registry, the first sync does not fetch it again.
func TestNoFetch(t *testing.T) {
	e := newEnv(t, state.Config{})
	e.sync.NoFetch = true
	commands := tu.CountGit(t)
	res := e.run(true)
	if n := tu.Network(commands()); len(n) > 0 {
		t.Fatalf("network commands: %v", n)
	}
	if e.installed("review") == "" || len(res.Warnings) != 0 {
		t.Fatalf("sync without fetch: %+v", res)
	}
	if !e.state().FetchFailedAt.IsZero() {
		t.Fatal("not fetching is not a failed fetch")
	}
}
