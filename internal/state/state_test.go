package state_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/state"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := state.LoadConfig(path); !errors.Is(err, state.ErrNotInitialized) {
		t.Fatalf("missing config: got %v", err)
	}
	cfg := state.Config{
		Registry:  "https://github.com/acme/skills.git",
		Ref:       "main",
		Teams:     []string{"backend"},
		Disabled:  []string{"p:a"},
		Installed: []string{"p:b"},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := state.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("got %+v, want %+v", got, cfg)
	}
	if got.SyncDebounce() != 60*time.Second || got.SyncInterval() != time.Hour {
		t.Fatalf("unexpected defaults: %v %v", got.SyncDebounce(), got.SyncInterval())
	}
	p := got.Prefs()
	if !reflect.DeepEqual(p.Teams, cfg.Teams) || !reflect.DeepEqual(p.Disabled, cfg.Disabled) || !reflect.DeepEqual(p.Installed, cfg.Installed) {
		t.Fatalf("unexpected prefs %+v", p)
	}
}

func TestConfigDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := state.Config{Registry: "r", Debounce: 5 * time.Second, Interval: 30 * time.Minute}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := state.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SyncDebounce() != 5*time.Second || got.SyncInterval() != 30*time.Minute {
		t.Fatalf("got %v %v", got.SyncDebounce(), got.SyncInterval())
	}
}

func TestListHelpers(t *testing.T) {
	l := state.Add(nil, "b")
	l = state.Add(l, "a")
	l = state.Add(l, "b")
	if !reflect.DeepEqual(l, []string{"a", "b"}) {
		t.Fatalf("Add: %v", l)
	}
	if l = state.Remove(l, "a"); !reflect.DeepEqual(l, []string{"b"}) {
		t.Fatalf("Remove: %v", l)
	}
}

func TestLockAndStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	lockPath, statePath := filepath.Join(dir, "lock.json"), filepath.Join(dir, "state.json")

	empty, err := state.LoadLock(lockPath)
	if err != nil || len(empty.Skills) != 0 {
		t.Fatalf("missing lock should be empty: %+v %v", empty, err)
	}
	lock := state.Lock{Commit: "abc", Skills: map[string]state.LockedSkill{
		"review": {ID: "p:review", Commit: "abc", SourceHash: "sha256:1", Hash: "sha256:2"},
	}}
	if err := lock.Save(lockPath); err != nil {
		t.Fatal(err)
	}
	if got, _ := state.LoadLock(lockPath); !reflect.DeepEqual(got, lock) {
		t.Fatalf("lock: got %+v", got)
	}

	st := state.State{LastSync: time.Unix(1700000000, 0).UTC(), Notices: []string{"n"}}
	if err := st.Save(statePath); err != nil {
		t.Fatal(err)
	}
	if got, _ := state.LoadState(statePath); !reflect.DeepEqual(got, st) {
		t.Fatalf("state: got %+v", got)
	}
}

func TestCorruptStateIsRecoverable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := state.LoadState(path)
	if err != nil || !st.LastSync.IsZero() {
		t.Fatalf("an empty or corrupt state must load as a zero state: %+v %v", st, err)
	}
}

func TestZeroFetchFailureIsOmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := (state.State{LastSync: time.Unix(1, 0)}).Save(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "fetch_failed_at") {
		t.Fatalf("a zero time must be omitted: %s", b)
	}
}
