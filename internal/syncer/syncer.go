// Package syncer runs a sync cycle: fetch the registry, resolve
// the developer's skills and apply them.
package syncer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/install"
	"github.com/yoanbernabeu/skillcrew/internal/paths"
	"github.com/yoanbernabeu/skillcrew/internal/policy"
	"github.com/yoanbernabeu/skillcrew/internal/snapshot"
	"github.com/yoanbernabeu/skillcrew/internal/state"
)

const maxLogSize = 1 << 20

// Syncer keeps the installed skills in line with the registry.
type Syncer struct {
	Paths     paths.Paths
	Installer *install.Installer
	Now       func() time.Time
	// FetchTimeout bounds the registry fetch; when it expires the sync goes
	// on offline with the last fetched registry. Zero means no limit.
	FetchTimeout time.Duration
	// FetchBackoff skips the network for this long after a failed fetch, so
	// agent hooks do not wait on a dead network at every session start.
	FetchBackoff time.Duration
	// TrustLocalTags only fetches the pinned tags missing locally, without
	// checking whether known tags moved upstream: tags are immutable here,
	// so agent hooks skip that network round trip. Manual and timer syncs
	// leave it off and report moved tags.
	TrustLocalTags bool
	// NoFetch uses the registry as last fetched, e.g. right after init
	// fetched it.
	NoFetch bool
}

// New returns a syncer for the given paths.
func New(p paths.Paths) *Syncer {
	return &Syncer{Paths: p, Installer: install.New(p), Now: time.Now}
}

// Store returns the registry snapshot store.
func (s *Syncer) Store() *snapshot.Store {
	return &snapshot.Store{Repo: gitx.Open(s.Paths.Registry()), Dir: s.Paths.Cache()}
}

// Result describes a sync run.
type Result struct {
	Skipped  bool // another sync is running, or the last one is too recent
	Commit   string
	Report   install.Report
	Warnings []string
}

// maxNotices bounds the notices kept until a developer sees them.
const maxNotices = 50

// Sync runs a cycle. Unless force is set, it does nothing if the last sync is
// more recent than the configured debounce window.
// It is skipped if another sync holds the lock.
func (s *Syncer) Sync(ctx context.Context, force bool) (Result, error) {
	// Check first, so a hook never recreates state after an uninstall.
	if _, err := state.LoadConfig(s.Paths.Config()); err != nil {
		return Result{}, err
	}
	unlock, ok, err := fsx.TryLock(s.Paths.SyncLock())
	if err != nil || !ok {
		return Result{Skipped: true}, err
	}
	defer unlock()
	return s.SyncLocked(ctx, force)
}

// Lock waits for the sync lock, for commands that change the configuration
// and then sync: the change and the sync it triggers are never interleaved
// with a hook sync.
func (s *Syncer) Lock(ctx context.Context) (unlock func(), err error) {
	return fsx.Lock(ctx, s.Paths.SyncLock())
}

// SyncLocked runs a cycle; the caller holds the sync lock.
func (s *Syncer) SyncLocked(ctx context.Context, force bool) (Result, error) {
	// Read under the lock, so a configuration change is never missed.
	cfg, err := state.LoadConfig(s.Paths.Config())
	if err != nil {
		return Result{}, err
	}
	st, err := state.LoadState(s.Paths.State())
	if err != nil {
		return Result{}, err
	}
	now := s.Now()
	if since := now.Sub(st.LastSync); !force && since >= 0 && since < cfg.SyncDebounce() {
		return Result{Skipped: true}, nil
	}

	offline := s.FetchBackoff > 0 && !st.FetchFailedAt.IsZero() && now.Sub(st.FetchFailedAt) < s.FetchBackoff
	res, fetchErr, err := s.run(ctx, cfg, offline)
	st.LastSync, st.LastError, st.Warnings = now, "", res.Warnings
	if err != nil {
		st.LastError = err.Error()
	}
	switch {
	case fetchErr != nil:
		st.FetchFailedAt = now
	case !offline:
		st.FetchFailedAt = time.Time{}
	}
	// Notices (backups, replaced skills) stay until a developer sees them.
	st.Notices = compact(append(st.Notices, res.Report.Notices...))
	s.log(now, res, err)
	return res, errors.Join(err, st.Save(s.Paths.State()))
}

// compact removes duplicates and keeps the most recent notices.
func compact(notices []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range notices {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > maxNotices {
		out = out[len(out)-maxNotices:]
	}
	return out
}

func (s *Syncer) run(ctx context.Context, cfg state.Config, offline bool) (res Result, fetchErr error, err error) {
	store := s.Store()
	// One deadline for every network operation of this run.
	netCtx, cancel := ctx, context.CancelFunc(func() {})
	if s.FetchTimeout > 0 {
		netCtx, cancel = context.WithTimeout(ctx, s.FetchTimeout)
	}
	defer cancel()

	switch {
	case s.NoFetch:
	case offline:
		res.Warnings = append(res.Warnings, "offline: the last fetch failed recently; using the last fetched registry")
	default:
		if fetchErr = store.Repo.Fetch(netCtx, cfg.Ref); fetchErr != nil {
			res.Warnings = append(res.Warnings, "offline: using the last fetched registry ("+oneLine(fetchErr)+")")
		}
	}
	head, err := store.Repo.Branch(ctx, cfg.Ref)
	if err != nil {
		return res, fetchErr, err
	}
	res.Commit = head
	snap, err := store.Registry(ctx, head)
	if err != nil {
		return res, fetchErr, err
	}
	lock, err := state.LoadLock(s.Paths.Lock())
	if err != nil {
		return res, fetchErr, err
	}
	plan := policy.Resolve(snap.Registry, Prefs(cfg, lock))
	res.Warnings = slices.Concat(res.Warnings, snap.Registry.Warnings, plan.Warnings)

	if tags := pinnedTags(plan); fetchErr == nil && !offline && len(tags) > 0 {
		if err := store.Repo.FetchTags(netCtx, !s.TrustLocalTags, tags...); err != nil {
			if netCtx.Err() != nil {
				fetchErr = err
			}
			res.Warnings = append(res.Warnings, "tags: "+oneLine(err))
		}
	}

	wanted := plan.Wanted()
	targets := s.locate(ctx, store, snap, wanted)
	wants := make([]snapshot.Want, len(wanted))
	for i, t := range targets {
		wants[i] = snapshot.Want{Commit: t.Commit, Dir: wanted[i].Skill.Dir, Tree: t.Tree}
	}
	dirs, exportErrs := store.Export(ctx, wants)
	var (
		items    []install.Item
		keep     []string
		deferred []string
		trees    []string
		errs     []error
	)
	for i, e := range wanted {
		if err := cmp.Or(targets[i].Err, exportErrs[i]); err != nil {
			// Keep whatever is installed rather than removing it.
			keep = append(keep, e.InstallName)
			if ctx.Err() != nil {
				deferred = append(deferred, e.InstallName)
			} else {
				errs = append(errs, fmt.Errorf("%s: %w", e.Skill.ID, err))
			}
			continue
		}
		items = append(items, install.Item{ID: e.Skill.ID, Name: e.InstallName, Commit: targets[i].Commit, Source: dirs[i], SourceHash: targets[i].Tree})
		trees = append(trees, targets[i].Tree)
	}
	lock, res.Report, err = s.Installer.Apply(ctx, lock, items, keep...)
	res.Report.Deferred = append(deferred, res.Report.Deferred...)
	if d := res.Report.Deferred; len(d) > 0 {
		res.Warnings = append(res.Warnings, "time budget reached: "+strings.Join(d, ", ")+" deferred to the next sync")
	}
	lock.Commit = head
	errs = append(errs, err, lock.Save(s.Paths.Lock()), store.Prune(ctx, trees...))
	return res, fetchErr, errors.Join(errs...)
}

// pinnedTags lists the tags the wanted skills are pinned to.
func pinnedTags(plan policy.Plan) []string {
	var tags []string
	for _, e := range plan.Wanted() {
		if e.SHA == "" && e.Ref != "" && !slices.Contains(tags, e.Ref) {
			tags = append(tags, e.Ref)
		}
	}
	return tags
}

// oneLine keeps an error readable in a one-line warning.
func oneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

// Prefs returns the developer's choices plus the skills present on the machine.
func Prefs(cfg state.Config, lock state.Lock) policy.Prefs {
	prefs := cfg.Prefs()
	for _, l := range lock.Skills {
		prefs.Present = append(prefs.Present, l.ID)
	}
	return prefs
}

// locate finds the commit each skill must be installed from (the registry
// head, a pinned commit or a tag) and the tree of its directory there. Pins
// are resolved together, with a fixed number of git commands.
func (s *Syncer) locate(ctx context.Context, store *snapshot.Store, snap *snapshot.Snapshot, wanted []policy.Entry) []gitx.Target {
	out := make([]gitx.Target, len(wanted))
	var pins []gitx.Pin
	var pinned []int
	for i, e := range wanted {
		switch {
		case e.SHA != "":
			pins, pinned = append(pins, gitx.Pin{SHA: e.SHA, Dir: e.Skill.Dir}), append(pinned, i)
			continue
		case e.Ref != "":
			pins, pinned = append(pins, gitx.Pin{Tag: e.Ref, Dir: e.Skill.Dir}), append(pinned, i)
			continue
		}
		out[i].Commit = snap.Commit
		var ok bool
		if out[i].Tree, ok = snap.TreeID(e.Skill.Dir); !ok {
			out[i].Err = fmt.Errorf("%s not found at commit %.12s", e.Skill.Dir, snap.Commit)
		}
	}
	for k, t := range store.Repo.ResolvePins(ctx, pins) {
		out[pinned[k]] = t
	}
	return out
}

// log appends a line per sync to the log file, starting over when it grows too large.
func (s *Syncer) log(now time.Time, res Result, err error) {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, statErr := os.Stat(s.Paths.Log()); statErr == nil && info.Size() > maxLogSize {
		flags |= os.O_TRUNC
	}
	f, openErr := os.OpenFile(s.Paths.Log(), flags, 0o644)
	if openErr != nil {
		return
	}
	defer f.Close()
	r := res.Report
	line := fmt.Sprintf("%s commit=%.12s installed=%v updated=%v removed=%v",
		now.UTC().Format(time.RFC3339), res.Commit, r.Installed, r.Updated, r.Removed)
	for _, msg := range slices.Concat(res.Warnings, r.Notices) {
		line += "\n  notice: " + msg
	}
	if err != nil {
		line += "\n  error: " + strings.ReplaceAll(err.Error(), "\n", "\n  error: ")
	}
	fmt.Fprintln(f, strings.Map(func(r rune) rune {
		if r == '\n' || (r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f)) {
			return r
		}
		return -1
	}, gitx.Redact(line)))
}
