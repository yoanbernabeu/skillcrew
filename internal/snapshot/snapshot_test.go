package snapshot_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/snapshot"
	tu "github.com/yoanbernabeu/skillcrew/internal/testutil"
)

func newStore(t *testing.T) (*snapshot.Store, *tu.Origin, string) {
	t.Helper()
	origin := tu.NewOrigin(t, map[string]string{
		".claude-plugin/marketplace.json":  tu.Marketplace("security"),
		"skillcrew.yaml":                   "org: acme\n",
		tu.SkillPath("security", "review"): tu.SkillMD("review", "v1"),
		"assets/big.bin":                   "not needed to read the registry",
	})
	repo, err := gitx.Clone(context.Background(), origin.Dir, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	return &snapshot.Store{Repo: repo, Dir: filepath.Join(t.TempDir(), "cache")}, origin, origin.Git("rev-parse", "HEAD")
}

func TestRegistryReadsGitObjectsWithoutExporting(t *testing.T) {
	store, origin, head := newStore(t)
	snap, err := store.Registry(context.Background(), head)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := snap.Registry.Skill("security:review")
	if !ok || snap.Registry.Governance.Org != "acme" {
		t.Fatalf("registry not loaded: %+v", snap.Registry)
	}
	if oid, ok := snap.TreeID(s.Dir); !ok || oid != origin.Git("rev-parse", "HEAD:"+s.Dir) {
		t.Fatalf("tree id of %s = %q", s.Dir, oid)
	}
	if _, err := os.Stat(store.Dir); !os.IsNotExist(err) {
		t.Fatal("loading the registry must not export anything")
	}
}

func TestSkillIsExportedOnceAndPruned(t *testing.T) {
	ctx := context.Background()
	store, origin, head := newStore(t)
	snap, _ := store.Registry(ctx, head)
	s, _ := snap.Registry.Skill("security:review")
	oid, _ := snap.TreeID(s.Dir)

	dir, err := store.Skill(ctx, oid)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err != nil || len(b) == 0 {
		t.Fatalf("skill not exported: %v", err)
	}
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if again, err := store.Skill(ctx, oid); err != nil || again != dir {
		t.Fatalf("second export: %q %v", again, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("a cached skill must not be exported again")
	}

	other := origin.Git("rev-parse", "HEAD:assets")
	if _, err := store.Skill(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := store.Prune(ctx, oid); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(store.Dir)
	if len(entries) != 1 || entries[0].Name() != oid {
		t.Fatalf("prune kept %v", entries)
	}
}

func TestSkillRejectsNonObjectIDs(t *testing.T) {
	store, _, _ := newStore(t)
	for _, bad := range []string{"main", "../x", ""} {
		if _, err := store.Skill(context.Background(), bad); err == nil {
			t.Errorf("Skill(%q) must fail", bad)
		}
	}
}

// Two syncs (e.g. a hook and the timer) may export the same tree at once.
func TestConcurrentExportsOfTheSameTree(t *testing.T) {
	ctx := context.Background()
	store, origin, _ := newStore(t)
	oid := origin.Git("rev-parse", "HEAD:plugins/security/skills/review")
	for range 5 {
		if err := store.Prune(ctx); err != nil {
			t.Fatal(err)
		}
		const n = 4
		var wg sync.WaitGroup
		dirs, errs := make([]string, n), make([]error, n)
		start := make(chan struct{})
		for i := range n {
			wg.Go(func() {
				<-start
				dirs[i], errs[i] = store.Skill(ctx, oid)
			})
		}
		close(start)
		wg.Wait()
		for i := range n {
			if errs[i] != nil || dirs[i] != dirs[0] {
				t.Fatalf("export %d: %q %v (first: %q)", i, dirs[i], errs[i], dirs[0])
			}
		}
		if b, err := os.ReadFile(filepath.Join(dirs[0], "SKILL.md")); err != nil || len(b) == 0 {
			t.Fatalf("skill not exported: %v", err)
		}
	}
}

// Loading a registry is linear in its size: a 10,000-plugin registry loads
// in well under a second (it took seconds when each directory listing
// scanned the whole tree).
func TestLargeRegistryLoadsQuickly(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 10,000-plugin registry")
	}
	ctx := context.Background()
	origin := tu.NewLargeOrigin(t, 10000, "org: acme\n")
	repo, err := gitx.Clone(ctx, origin.Dir, filepath.Join(t.TempDir(), "reg.git"))
	if err != nil {
		t.Fatal(err)
	}
	store := &snapshot.Store{Repo: repo, Dir: filepath.Join(t.TempDir(), "cache")}
	start := time.Now()
	snap, err := store.Registry(ctx, origin.Git("rev-parse", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("loading a 10,000-plugin registry took %s", d)
	}
	if n := len(snap.Registry.Skills); n != 10000 {
		t.Fatalf("%d skills discovered, want 10000", n)
	}
}

func TestExportFillsTheCacheInOneBatch(t *testing.T) {
	ctx := context.Background()
	store, origin, head := newStore(t)
	wants := []snapshot.Want{
		{Commit: head, Dir: "plugins/security/skills/review", Tree: origin.Git("rev-parse", "HEAD:plugins/security/skills/review")},
		{Commit: head, Dir: "assets", Tree: origin.Git("rev-parse", "HEAD:assets")},
	}
	dirs, errs := store.Export(ctx, wants)
	for i, w := range wants {
		if errs[i] != nil || dirs[i] != filepath.Join(store.Dir, w.Tree) {
			t.Fatalf("export of %s: %q %v", w.Dir, dirs[i], errs[i])
		}
	}
	if b, err := os.ReadFile(filepath.Join(dirs[1], "big.bin")); err != nil || string(b) != "not needed to read the registry" {
		t.Fatalf("content: %q %v", b, err)
	}
	commands := tu.CountGit(t)
	if _, errs := store.Export(ctx, wants); errs[0] != nil || errs[1] != nil {
		t.Fatal(errs)
	}
	if c := commands(); len(c) > 0 {
		t.Fatalf("cached trees must not run git: %v", c)
	}
}

// A tree over the export limits fails the same way every time: it is
// remembered, so syncs do not list it again at every agent start.
func TestRejectedTreesAreRemembered(t *testing.T) {
	ctx := context.Background()
	store, origin, head := newStore(t)
	want := []snapshot.Want{{Commit: head, Dir: "assets", Tree: origin.Git("rev-parse", "HEAD:assets")}}
	defer func(b int64) { gitx.ExportMaxBytes = b }(gitx.ExportMaxBytes)
	gitx.ExportMaxBytes = 3

	_, errs := store.Export(ctx, want)
	var rejected *gitx.RejectedError
	if !errors.As(errs[0], &rejected) {
		t.Fatalf("expected a rejection, got %v", errs[0])
	}
	if err := store.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	commands := tu.CountGit(t)
	again := &snapshot.Store{Repo: store.Repo, Dir: store.Dir}
	if _, errs := again.Export(ctx, want); !errors.As(errs[0], &rejected) || !strings.Contains(errs[0].Error(), "bytes") {
		t.Fatalf("expected the remembered rejection, got %v", errs[0])
	}
	if c := commands(); len(c) > 0 {
		t.Fatalf("a rejected tree must not be exported again: %v", c)
	}

	gitx.ExportMaxBytes = 1 << 20 // e.g. a new Skillcrew release raises the limits
	if _, errs := again.Export(ctx, want); errs[0] != nil {
		t.Fatalf("new limits must give the tree another chance: %v", errs[0])
	}
}

func TestPruneStopsAtTheDeadline(t *testing.T) {
	ctx := context.Background()
	store, origin, _ := newStore(t)
	oid := origin.Git("rev-parse", "HEAD:assets")
	if _, err := store.Skill(ctx, oid); err != nil {
		t.Fatal(err)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Prune(done); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, oid)); err != nil {
		t.Fatal("a prune past the deadline must leave the cache for the next sync")
	}
	if err := store.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(store.Dir); len(entries) != 0 {
		t.Fatalf("prune left %v", entries)
	}
}
