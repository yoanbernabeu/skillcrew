// Package paths centralizes every location Skillcrew reads or writes.
package paths

import "path/filepath"

// Paths resolves locations relative to a home directory, so tests can use a
// temporary home.
type Paths struct {
	Home string
}

// Root is Skillcrew's own state directory.
func (p Paths) Root() string { return filepath.Join(p.Home, ".skillcrew") }

// Config is the developer's configuration file.
func (p Paths) Config() string { return filepath.Join(p.Root(), "config.yaml") }

// Lock records what Skillcrew installed.
func (p Paths) Lock() string { return filepath.Join(p.Root(), "lock.json") }

// State holds the outcome of the last sync.
func (p Paths) State() string { return filepath.Join(p.Root(), "state.json") }

// Registry is the bare clone of the registry.
func (p Paths) Registry() string { return filepath.Join(p.Root(), "registry.git") }

// Backups holds backups of edited team skills and replaced personal skills.
func (p Paths) Backups() string { return filepath.Join(p.Root(), "backups") }

// Cache holds exported skills, one directory per git tree id.
func (p Paths) Cache() string { return filepath.Join(p.Root(), "cache") }

// Log is the sync history.
func (p Paths) Log() string { return filepath.Join(p.Root(), "sync.log") }

// SyncLock serializes syncs and the commands that change installed skills.
func (p Paths) SyncLock() string { return filepath.Join(p.Root(), "sync.lock") }

// AgentsSkills is the cross-agent skills directory holding the real copies.
func (p Paths) AgentsSkills() string { return filepath.Join(p.Home, ".agents", "skills") }

// ClaudeSkills is Claude Code's personal skills directory, holding symlinks.
func (p Paths) ClaudeSkills() string { return filepath.Join(p.Home, ".claude", "skills") }

// Staging is where skills are prepared before being swapped in. It sits next
// to AgentsSkills so the swap is a rename on the same filesystem, and outside
// it so agents never load a half-written skill.
func (p Paths) Staging() string { return filepath.Join(p.Home, ".agents", ".skillcrew-staging") }
