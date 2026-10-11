// Package state persists Skillcrew's local files: the user configuration,
// the lockfile and the last sync state.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
	"github.com/yoanbernabeu/skillcrew/internal/policy"
)

// ErrNotInitialized means "skillcrew init" has not been run.
var ErrNotInitialized = errors.New(`skillcrew is not initialized; run "skillcrew init <registry>"`)

const (
	defaultDebounce = 60 * time.Second
	defaultInterval = time.Hour
)

// Config is the developer's configuration (~/.skillcrew/config.yaml).
type Config struct {
	Registry  string        `yaml:"registry"`
	Ref       string        `yaml:"ref,omitempty"`
	Teams     []string      `yaml:"teams,omitempty"`
	Disabled  []string      `yaml:"disabled,omitempty"`  // recommended skills turned off
	Installed []string      `yaml:"installed,omitempty"` // available skills turned on
	Debounce  time.Duration `yaml:"debounce,omitempty"`
	Interval  time.Duration `yaml:"interval,omitempty"`
}

// LoadConfig reads the configuration, returning ErrNotInitialized if it does not exist.
func LoadConfig(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, ErrNotInitialized
	}
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes the configuration.
func (c Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(path, data, 0o600)
}

// SyncDebounce is the minimum delay between two fetches.
func (c Config) SyncDebounce() time.Duration { return orDefault(c.Debounce, defaultDebounce) }

// SyncInterval is the period of the fallback timer.
func (c Config) SyncInterval() time.Duration { return orDefault(c.Interval, defaultInterval) }

// Prefs returns the developer's choices for the resolver.
func (c Config) Prefs() policy.Prefs {
	return policy.Prefs{Teams: c.Teams, Disabled: c.Disabled, Installed: c.Installed}
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Add returns list with s added once, sorted.
func Add(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	list = append(list, s)
	slices.Sort(list)
	return list
}

// Remove returns list without s.
func Remove(list []string, s string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(v string) bool { return v == s })
}

// Lock records what Skillcrew installed (~/.skillcrew/lock.json). A skill
// directory is owned by Skillcrew if and only if it appears here.
type Lock struct {
	Commit string                 `json:"commit"`
	Skills map[string]LockedSkill `json:"skills"` // keyed by install name
}

// LockedSkill is one installed skill.
type LockedSkill struct {
	ID         string `json:"id"`
	Commit     string `json:"commit"`
	SourceHash string `json:"source_hash"` // content in the registry
	Hash       string `json:"hash"`        // content on disk (differs when aliased)
	Stamp      string `json:"stamp"`       // metadata fingerprint of the copy on disk
}

// LoadLock reads the lockfile; a missing file is an empty lock.
func LoadLock(path string) (Lock, error) {
	l := Lock{Skills: map[string]LockedSkill{}}
	if err := loadJSON(path, &l); err != nil {
		return l, err
	}
	if l.Skills == nil {
		l.Skills = map[string]LockedSkill{}
	}
	return l, nil
}

// Save writes the lockfile.
func (l Lock) Save(path string) error { return saveJSON(path, l) }

// State is the outcome of the last sync (~/.skillcrew/state.json).
type State struct {
	LastSync      time.Time `json:"last_sync"`
	LastError     string    `json:"last_error,omitempty"`
	Warnings      []string  `json:"warnings,omitempty"`       // from the last sync
	Notices       []string  `json:"notices,omitempty"`        // kept until shown to the developer
	FetchFailedAt time.Time `json:"fetch_failed_at,omitzero"` // last failed fetch, for backoff
}

// LoadState reads the state. A missing or unreadable file (e.g. truncated by
// a crash) is a zero state: it only holds the outcome of the last sync.
func LoadState(path string) (State, error) {
	var s State
	if err := loadJSON(path, &s); err != nil {
		return State{}, nil
	}
	return s, nil
}

// Save writes the state.
func (s State) Save(path string) error { return saveJSON(path, s) }

func loadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(path, append(data, '\n'), 0o600)
}
