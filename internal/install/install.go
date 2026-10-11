// Package install applies a set of skills to the developer's machine: real
// copies in ~/.agents/skills, symlinks in ~/.claude/skills, backups of
// anything it has to overwrite, and the lockfile that records ownership.
package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
	"github.com/yoanbernabeu/skillcrew/internal/paths"
	"github.com/yoanbernabeu/skillcrew/internal/skill"
	"github.com/yoanbernabeu/skillcrew/internal/state"
)

// Item is a skill to install, as exported from the registry.
type Item struct {
	ID     string
	Name   string // install name
	Commit string
	Source string // directory holding the skill files
	// SourceHash identifies the source content (e.g. its git tree id); it is
	// computed from Source when empty.
	SourceHash string
}

// Report summarizes what Apply changed.
type Report struct {
	Installed []string
	Updated   []string
	Removed   []string
	// Deferred lists the skills left as they were because the deadline
	// passed; the next sync installs them.
	Deferred []string
	Notices  []string
}

func (r *Report) notice(format string, args ...any) {
	r.Notices = append(r.Notices, fmt.Sprintf(format, args...))
}

// Condition is the state of an installed skill on disk.
type Condition string

// Conditions: the copy matches the lock, was edited, or is gone.
const (
	OK       Condition = "ok"
	Modified Condition = "modified"
	Missing  Condition = "missing"
)

// Installer writes skills under a home directory.
type Installer struct {
	Paths       paths.Paths
	Now         func() time.Time
	KeepBackups int
}

// New returns an installer with the default settings.
func New(p paths.Paths) *Installer {
	return &Installer{Paths: p, Now: time.Now, KeepBackups: 5}
}

// Apply makes the installed skills match items. Skills in lock that are
// neither in items nor in keep are removed; keep protects skills the caller
// could not prepare this time. A failing skill keeps its previous state and
// does not prevent the others from being applied.
//
// Once ctx is done, no new install starts: the remaining skills keep their
// previous state and are reported as deferred. Removals still happen (they
// are renames, and a blocked skill must go), and deleting what was set aside
// is left to a later run.
func (in *Installer) Apply(ctx context.Context, lock state.Lock, items []Item, keep ...string) (state.Lock, Report, error) {
	next := state.Lock{Commit: lock.Commit, Skills: map[string]state.LockedSkill{}}
	var report Report
	var errs []error

	wanted := map[string]bool{}
	for _, name := range keep {
		wanted[name] = true
		if prev, ok := lock.Skills[name]; ok {
			next.Skills[name] = prev
		}
	}
	for _, it := range items {
		wanted[it.Name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(lock.Skills)) {
		if wanted[name] {
			continue
		}
		if err := in.remove(name, lock.Skills[name], &report); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", name, err))
			next.Skills[name] = lock.Skills[name]
			continue
		}
		report.Removed = append(report.Removed, name)
	}
	for _, it := range items {
		prev, owned := lock.Skills[it.Name]
		locked, err := in.install(ctx, it, prev, owned, &report)
		if err != nil {
			if errors.Is(err, errDeferred) {
				report.Deferred = append(report.Deferred, it.Name)
			} else {
				errs = append(errs, fmt.Errorf("%s: %w", it.ID, err))
			}
			if owned {
				next.Skills[it.Name] = prev
			}
			continue
		}
		next.Skills[it.Name] = locked
	}
	in.cleanup(ctx)
	return next, report, errors.Join(errs...)
}

// errDeferred means the deadline passed before a skill could be installed.
var errDeferred = errors.New("deferred to the next sync")

// cleanup deletes what was set aside in the staging directory (staged
// copies, previous versions, removed skills), unless the deadline passed: it
// is outside the skills directories, so no agent ever reads it, and the next
// run deletes it.
func (in *Installer) cleanup(ctx context.Context) {
	if ctx.Err() == nil {
		_ = os.RemoveAll(in.Paths.Staging())
	}
}

func (in *Installer) install(ctx context.Context, it Item, prev state.LockedSkill, owned bool, report *Report) (state.LockedSkill, error) {
	if err := skill.ValidateName(it.Name); err != nil {
		return prev, err
	}
	sourceHash := it.SourceHash
	if sourceHash == "" {
		var err error
		if sourceHash, err = hashDir(it.Source); err != nil {
			return prev, err
		}
	}
	target := in.skillDir(it.Name)
	if owned && sourceHash == prev.SourceHash {
		// Fast path: same source as installed and an untouched copy on disk.
		if stamp, ok := in.untouched(it.Name, prev); ok {
			prev.ID, prev.Commit, prev.Stamp = it.ID, it.Commit, stamp
			in.link(it.Name, report)
			return prev, nil
		}
	}
	if ctx.Err() != nil {
		return prev, errDeferred
	}
	// The staged copy is hashed once: it differs from the source when the
	// skill is installed under an alias.
	staged, err := in.stage(it)
	if err != nil {
		return prev, err
	}
	hash, err := hashDir(staged)
	if err != nil {
		return prev, err
	}

	current, err := in.current(target, prev, owned)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return prev, err
	}
	switch {
	case !exists:
		if owned {
			report.notice("%s was missing and has been reinstalled", it.Name)
		} else {
			report.Installed = append(report.Installed, it.Name)
		}
		err = in.replace(staged, target, noBackup)
	case current == hash:
		// Already the right content, e.g. after an interrupted sync: adopt it.
	case owned && current == prev.Hash:
		report.Updated = append(report.Updated, it.Name)
		err = in.replace(staged, target, noBackup)
	case owned:
		err = in.replace(staged, target, editBackup)
		report.notice("local changes to %s were backed up to %s; the team version is restored (use \"skillcrew propose %s\" to suggest them)", it.Name, in.backupDir(it.Name), it.Name)
	default:
		err = in.replace(staged, target, personalBackup)
		report.Installed = append(report.Installed, it.Name)
		report.notice("your personal skill %s was backed up to %s and replaced by the team skill", it.Name, in.backupDir(it.Name))
	}
	if err != nil {
		return prev, err
	}
	stamp, err := stampDir(target)
	if err != nil {
		return prev, err
	}
	in.link(it.Name, report)
	return state.LockedSkill{ID: it.ID, Commit: it.Commit, SourceHash: sourceHash, Hash: hash, Stamp: stamp}, nil
}

// link exposes the skill to Claude Code. It is installed for every agent
// reading ~/.agents/skills anyway, so a failure is reported, not fatal.
func (in *Installer) link(name string, report *Report) {
	if err := in.linkClaude(name, report); err != nil {
		report.notice("%s is not available to Claude Code: %v", name, err)
	}
}

// untouched reports whether the installed copy still matches the lock,
// comparing file metadata first and contents only if they differ. It returns
// the current metadata stamp.
func (in *Installer) untouched(name string, locked state.LockedSkill) (string, bool) {
	dir := in.skillDir(name)
	stamp, err := stampDir(dir)
	if err != nil {
		return "", false
	}
	if stamp == locked.Stamp {
		return stamp, true
	}
	hash, err := hashDir(dir)
	return stamp, err == nil && hash == locked.Hash
}

// current returns the content hash of an installed copy. A copy whose
// metadata stamp matches the lock is not read: the lock's hash stands for it.
func (in *Installer) current(dir string, locked state.LockedSkill, owned bool) (string, error) {
	if owned && locked.Stamp != "" {
		if stamp, err := stampDir(dir); err == nil && stamp == locked.Stamp {
			return locked.Hash, nil
		}
	}
	return hashDir(dir)
}

// stage copies the source into a fresh staging directory, renaming the skill
// if it is installed under an alias, and validates it.
func (in *Installer) stage(it Item) (string, error) {
	if err := os.MkdirAll(in.Paths.Staging(), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(in.Paths.Staging(), it.Name+"-")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(tmp, it.Name)
	if err := skill.CopyAs(it.Source, dir); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return dir, nil
}

// replace swaps staged into target. The previous content is either moved to
// the backups or set aside in the staging directory, deleted by cleanup. If
// the swap fails, the previous content is put back.
func (in *Installer) replace(staged, target string, backup backupKind) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	var old string
	if _, err := os.Lstat(target); err == nil {
		old = staged + ".old"
		if backup != noBackup {
			if old, err = in.newBackupDir(filepath.Base(target), backup); err != nil {
				return err
			}
		}
		if err := move(target, old); err != nil {
			return err
		}
	}
	if err := move(staged, target); err != nil {
		if old != "" {
			_ = move(old, target)
		}
		return err
	}
	if backup == noBackup {
		return nil
	}
	return in.pruneBackups(filepath.Base(target))
}

// remove deletes an owned skill, backing it up first if it was edited
// locally. It is moved out of the skills directory, so agents never read a
// half-deleted skill; cleanup deletes it.
func (in *Installer) remove(name string, locked state.LockedSkill, report *Report) error {
	in.unlinkClaude(name)
	dir := in.skillDir(name)
	current, err := in.current(dir, locked, true)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current == locked.Hash {
		if err := os.MkdirAll(in.Paths.Staging(), 0o755); err != nil {
			return err
		}
		trash, err := os.MkdirTemp(in.Paths.Staging(), "removed-"+name+"-")
		if err != nil {
			return err
		}
		return move(dir, filepath.Join(trash, name))
	}
	backup, err := in.newBackupDir(name, editBackup)
	if err != nil {
		return err
	}
	if err := move(dir, backup); err != nil {
		return err
	}
	report.notice("%s was removed; your local changes to it were backed up to %s", name, backup)
	return in.pruneBackups(name)
}

// linkClaude points ~/.claude/skills/<name> to the real copy, when Claude Code is installed.
func (in *Installer) linkClaude(name string, report *Report) error {
	if _, err := os.Stat(filepath.Dir(in.Paths.ClaudeSkills())); err != nil {
		return nil
	}
	link, target := in.claudeLink(name), in.skillDir(name)
	info, err := os.Lstat(link)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0:
		dest, _ := os.Readlink(link)
		if dest == target {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return err
		}
		report.notice("your symlink %s (to %s) was replaced by the team skill", link, dest)
	default:
		backup, err := in.newBackupDir(name, claudeBackup)
		if err != nil {
			return err
		}
		if err := move(link, backup); err != nil {
			return err
		}
		report.notice("your personal Claude Code skill %s was backed up to %s and replaced by the team skill", name, backup)
		if err := in.pruneBackups(name); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(in.Paths.ClaudeSkills(), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, link)
}

func (in *Installer) unlinkClaude(name string) {
	link := in.claudeLink(name)
	if dest, err := os.Readlink(link); err == nil && dest == in.skillDir(name) {
		_ = os.Remove(link)
	}
}

// Check reports the on-disk condition of every locked skill.
func (in *Installer) Check(lock state.Lock) map[string]Condition {
	out := map[string]Condition{}
	for name, locked := range lock.Skills {
		switch _, ok := in.untouched(name, locked); {
		case ok:
			out[name] = OK
		case exists(in.skillDir(name)):
			out[name] = Modified
		default:
			out[name] = Missing
		}
	}
	return out
}

// Uninstall removes every skill Skillcrew owns, backing up local edits.
func (in *Installer) Uninstall(lock state.Lock) (Report, error) {
	var report Report
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(lock.Skills)) {
		errs = append(errs, in.remove(name, lock.Skills[name], &report))
	}
	in.cleanup(context.Background())
	return report, errors.Join(errs...)
}

// LatestBackup returns the most recent backup of local edits to a skill.
func (in *Installer) LatestBackup(name string) (string, bool) {
	backups := in.listBackups(name, editBackup)
	if len(backups) == 0 {
		return "", false
	}
	return backups[len(backups)-1], true
}

func (in *Installer) skillDir(name string) string {
	return filepath.Join(in.Paths.AgentsSkills(), name)
}
func (in *Installer) claudeLink(name string) string {
	return filepath.Join(in.Paths.ClaudeSkills(), name)
}
func (in *Installer) backupDir(name string) string { return filepath.Join(in.Paths.Backups(), name) }

// backupKind tells local edits of a team skill, which are pruned, from
// personal skills replaced by a team skill, which are kept forever.
type backupKind string

const (
	noBackup       backupKind = ""
	editBackup     backupKind = "edit"
	personalBackup backupKind = "personal"
	claudeBackup   backupKind = "personal-claude" // from ~/.claude/skills
)

func (in *Installer) newBackupDir(name string, kind backupKind) (string, error) {
	dir := in.backupDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, in.Now().UTC().Format("20060102T150405.000000000Z")+"-"+string(kind)), nil
}

// listBackups returns the backups of a kind, oldest first.
func (in *Installer) listBackups(name string, kind backupKind) []string {
	entries, _ := os.ReadDir(in.backupDir(name))
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-"+string(kind)) {
			out = append(out, filepath.Join(in.backupDir(name), e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

func (in *Installer) pruneBackups(name string) error {
	backups := in.listBackups(name, editBackup)
	for len(backups) > in.KeepBackups {
		if err := os.RemoveAll(backups[0]); err != nil {
			return err
		}
		backups = backups[1:]
	}
	return nil
}

// move renames src to dst, falling back to copy and delete across filesystems.
func move(src, dst string) error {
	err := os.Rename(src, dst)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := fsx.CopyDir(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func hashDir(dir string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	return skill.HashDir(os.DirFS(dir), ".")
}

// stampDir fingerprints file metadata (path, size, mode, modification time),
// a cheap way to detect that nothing changed without reading contents.
func stampDir(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || skill.Ignored(d.Name()) {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00%v\x00%d\n", strings.TrimPrefix(p, dir), info.Size(), info.Mode(), info.ModTime().UnixNano())
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
