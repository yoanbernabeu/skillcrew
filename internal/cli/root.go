// Package cli implements the skillcrew command line.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yoanbernabeu/skillcrew/internal/agent"
	"github.com/yoanbernabeu/skillcrew/internal/fsx"
	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/install"
	"github.com/yoanbernabeu/skillcrew/internal/paths"
	"github.com/yoanbernabeu/skillcrew/internal/policy"
	"github.com/yoanbernabeu/skillcrew/internal/propose"
	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/scheduler"
	"github.com/yoanbernabeu/skillcrew/internal/state"
	"github.com/yoanbernabeu/skillcrew/internal/syncer"
)

// App holds the dependencies of every command, so tests can replace them.
type App struct {
	Paths      paths.Paths
	In         io.Reader
	Out, Err   io.Writer
	Agents     []agent.Agent
	Scheduler  func(home string) (scheduler.Scheduler, error)
	Opener     propose.Opener
	Executable func() (string, error)
	Now        func() time.Time
	Version    string
	// LockTimeout bounds the wait for a running sync (default 30s).
	LockTimeout time.Duration
	// Background starts skillcrew with args detached from the caller (used by
	// agent hooks, so a session start never waits for the network).
	Background func(args ...string) error
	Args       []string // command line arguments; nil means os.Args[1:]

	json bool // set by commands whose --json flag is on
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Default returns an App wired to the real system.
func Default(version string) (*App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &App{
		Paths:      paths.Paths{Home: home},
		In:         os.Stdin,
		Out:        terminal{os.Stdout},
		Err:        terminal{os.Stderr},
		Agents:     agent.All(),
		Scheduler:  func(home string) (scheduler.Scheduler, error) { return scheduler.New(home, scheduler.ExecRunner) },
		Opener:     propose.NewCLIOpener(),
		Executable: executable,
		Background: spawnDetached,
		Now:        time.Now,
		Version:    version,
	}, nil
}

// executable returns a stable path to the skillcrew binary for hooks and
// timers: the one found in PATH (e.g. Homebrew's symlink, which survives
// upgrades), or the running binary.
func executable() (string, error) {
	if p, err := exec.LookPath("skillcrew"); err == nil {
		return p, nil
	}
	return os.Executable()
}

// Command builds the root command.
func (a *App) Command() *cobra.Command {
	root := &cobra.Command{
		Use:   "skillcrew",
		Short: "Distribute and govern AI agent skills across your team",
		Long: "Skillcrew keeps the same Agent Skills, in the same version, in every AI coding agent\n" +
			"of every developer of your organization, with no action from them.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	if _, ok := a.Out.(terminal); !ok {
		a.Out = terminal{a.Out}
	}
	if _, ok := a.Err.(terminal); !ok {
		a.Err = terminal{a.Err}
	}
	root.Version = a.Version
	root.SetVersionTemplate("skillcrew {{.Version}}\n")
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	root.AddCommand(
		a.initCommand(),
		a.syncCommand(),
		a.statusCommand(),
		a.listCommand(),
		a.teamCommand(),
		a.installCommand(),
		a.removeCommand(),
		a.enableCommand(),
		a.disableCommand(),
		a.proposeCommand(),
		a.doctorCommand(),
		a.validateCommand(),
		a.registryCommand(),
		a.skillCommand(),
		a.uninstallCommand(),
		a.versionCommand(),
	)
	return root
}

// Execute runs the command line and returns the process exit code. Errors
// are printed to a.Err, stripped of control characters like any output.
func (a *App) Execute(ctx context.Context) int {
	root := a.Command()
	if a.Args != nil {
		root.SetArgs(a.Args)
	}
	cmd, err := root.ExecuteContextC(ctx)
	if err != nil {
		msg := strings.TrimRight(err.Error(), "\n")
		if isUsageError(msg) {
			msg += fmt.Sprintf("\nRun '%s --help' for usage.", cmd.CommandPath())
		}
		fmt.Fprintln(a.Err, "Error:", msg)
		return 1
	}
	return 0
}

// isUsageError reports whether cobra rejected the command line itself.
func isUsageError(msg string) bool {
	for _, prefix := range []string{"unknown command", "unknown flag", "unknown shorthand flag", "flag needs an argument", "invalid argument"} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

// usage wraps an argument check so its error shows the usage line.
func usage(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			if len(args) == 0 {
				err = errors.New("missing argument")
			}
			return fmt.Errorf("%w\nUsage: %s", err, cmd.UseLine())
		}
		return nil
	}
}

// group is a command that only holds subcommands: an unknown subcommand is
// an error rather than a silent help page.
func group(cmd *cobra.Command) *cobra.Command {
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
		if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
			msg += fmt.Sprintf("; did you mean %q?", s[0])
		}
		return errors.New(msg)
	}
	return cmd
}

// Network policy: hooks never wait for the network (they start a background
// sync), so every sync can afford a slow network.
const (
	// backgroundFetchBackoff skips the network after a failed fetch, so
	// session starts on a dead network do not pile up background fetches.
	backgroundFetchBackoff = 10 * time.Minute
	fetchTimeout           = time.Minute
)

func (a *App) syncer() *syncer.Syncer {
	s := syncer.New(a.Paths)
	s.FetchTimeout = fetchTimeout
	s.Now = a.Now
	s.Installer.Now = a.Now
	return s
}

func (a *App) printf(format string, args ...any) { fmt.Fprintf(a.Out, format, args...) }

func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// view is the registry as of the last sync, resolved for the developer.
type view struct {
	cfg  state.Config
	lock state.Lock
	reg  *registry.Registry
	plan policy.Plan
}

// load reads the local state without touching the network.
func (a *App) load(ctx context.Context) (*view, error) {
	cfg, err := state.LoadConfig(a.Paths.Config())
	if err != nil {
		return nil, err
	}
	lock, err := state.LoadLock(a.Paths.Lock())
	if err != nil {
		return nil, err
	}
	commit := lock.Commit
	if commit == "" {
		if commit, err = gitx.Open(a.Paths.Registry()).Branch(ctx, cfg.Ref); err != nil {
			return nil, err
		}
	}
	snap, err := a.syncer().Store().Registry(ctx, commit)
	if err != nil {
		return nil, err
	}
	reg := snap.Registry
	return &view{cfg: cfg, lock: lock, reg: reg, plan: policy.Resolve(reg, syncer.Prefs(cfg, lock))}, nil
}

// entry finds a skill by id ("plugin:name") or by install name.
func (v *view) entry(arg string) (policy.Entry, error) {
	var matches []policy.Entry
	for _, e := range v.plan.Entries {
		if e.Skill.ID == arg {
			return e, nil
		}
		if e.InstallName == arg {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		return policy.Entry{}, v.unknownSkill(arg)
	case 1:
		return matches[0], nil
	}
	ids := make([]string, len(matches))
	for i, m := range matches {
		ids[i] = m.Skill.ID
	}
	return policy.Entry{}, fmt.Errorf("%q is ambiguous: %s", arg, strings.Join(ids, ", "))
}

// unknownSkill explains that arg names no skill, suggesting close names.
func (v *view) unknownSkill(arg string) error {
	var names []string
	for _, e := range v.plan.Entries {
		names = append(names, e.Skill.ID, e.InstallName)
	}
	msg := fmt.Sprintf("unknown skill %q", arg)
	if s := suggest(arg, names); len(s) > 0 {
		msg += fmt.Sprintf(" (did you mean %q?)", s[0])
	}
	return errors.New(msg + `; "skillcrew list" shows the registry skills, "skillcrew list --available" the ones you can install`)
}

// suggest returns the candidates close to s, closest first: those starting
// with s, then those within a small edit distance.
func suggest(s string, candidates []string) []string {
	type match struct {
		name string
		dist int
	}
	var matches []match
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c] || c == s {
			continue
		}
		seen[c] = true
		d := editDistance(s, c)
		if strings.HasPrefix(c, s) && len(s) >= 2 {
			d = 0
		}
		if d <= 2 && d < len(s) {
			matches = append(matches, match{c, d})
		}
	}
	slices.SortStableFunc(matches, func(x, y match) int { return x.dist - y.dist })
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.name
	}
	return out
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// runSync runs a sync with s and prints its outcome unless quiet.
func (a *App) runSync(ctx context.Context, s *syncer.Syncer, force, quiet bool) error {
	// Check first: a sync before init must neither lock nor create anything.
	if _, err := state.LoadConfig(a.Paths.Config()); err != nil {
		return err
	}
	if quiet { // the timer: skip if a hook sync is running
		_, err := s.Sync(ctx, force)
		return err
	}
	unlock, err := a.lock(ctx, s)
	if err != nil {
		return err
	}
	defer unlock()
	return a.syncLocked(ctx, s, force)
}

// lock waits for a running sync to finish.
func (a *App) lock(ctx context.Context, s *syncer.Syncer) (func(), error) {
	timeout := a.LockTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	unlock, err := s.Lock(ctx)
	if err != nil {
		return nil, errors.New("another sync is still running; try again in a moment")
	}
	return unlock, nil
}

// syncLocked syncs and prints the outcome; the caller holds the sync lock.
func (a *App) syncLocked(ctx context.Context, s *syncer.Syncer, force bool) error {
	before, _ := state.LoadLock(a.Paths.Lock())
	res, err := s.SyncLocked(ctx, force)
	if a.json {
		r := res.Report
		return errors.Join(err, a.printJSON(map[string]any{
			"skipped": res.Skipped, "commit": res.Commit, "installed": nonNil(r.Installed),
			"updated": nonNil(r.Updated), "removed": nonNil(r.Removed),
			"notices": nonNil(slices.Concat(res.Warnings, r.Notices)),
		}))
	}
	if res.Skipped {
		a.printf("Another sync is running or a sync just ran; skipped.\n")
		return err
	}
	if res.Commit == "" {
		return err // nothing was synced
	}
	a.printReport(res, a.removalReasons(ctx, before, res.Report.Removed))
	for _, n := range a.noticesLocked() {
		if !slices.Contains(res.Report.Notices, n) {
			a.printf("  ! %s\n", n)
		}
	}
	return err
}

// takeNotices returns the notices waiting to be shown and clears them. They
// are kept if a sync is running, rather than racing with it.
func (a *App) takeNotices() []string {
	unlock, ok, err := fsx.TryLock(a.Paths.SyncLock())
	if err != nil || !ok {
		return nil
	}
	defer unlock()
	return a.noticesLocked()
}

// noticesLocked is takeNotices for a caller holding the sync lock.
func (a *App) noticesLocked() []string {
	st, err := state.LoadState(a.Paths.State())
	if err != nil || len(st.Notices) == 0 {
		return nil
	}
	notices := st.Notices
	st.Notices = nil
	if st.Save(a.Paths.State()) != nil {
		return nil
	}
	return notices
}

// removalReasons explains why each removed skill was removed.
func (a *App) removalReasons(ctx context.Context, before state.Lock, removed []string) map[string]string {
	if len(removed) == 0 {
		return nil
	}
	v, err := a.load(ctx)
	if err != nil {
		return nil
	}
	reasons := map[string]string{}
	for _, name := range removed {
		id := before.Skills[name].ID
		i := slices.IndexFunc(v.plan.Entries, func(e policy.Entry) bool { return e.Skill.ID == id })
		switch {
		case i < 0:
			reasons[name] = "no longer distributed by the registry"
		case v.plan.Entries[i].Status == registry.Blocked:
			reasons[name] = "blocked by your organization"
		case slices.Contains(v.cfg.Disabled, id):
			reasons[name] = "disabled"
		case v.plan.Entries[i].Install && v.plan.Entries[i].InstallName != name:
			reasons[name] = "now installed as " + v.plan.Entries[i].InstallName
		}
	}
	return reasons
}

// offlineWarning returns the warning of a sync that could not fetch.
func offlineWarning(warnings []string) (string, bool) {
	for _, w := range warnings {
		if strings.HasPrefix(w, "offline") {
			return w, true
		}
	}
	return "", false
}

func (a *App) printReport(res syncer.Result, reasons map[string]string) {
	r := res.Report
	_, offline := offlineWarning(res.Warnings)
	switch {
	case offline:
		a.printf("Using the last fetched registry (offline), at %.12s:\n", res.Commit)
	case len(r.Installed)+len(r.Updated)+len(r.Removed)+len(r.Notices) == 0:
		a.printf("Skills are up to date (registry %.12s).\n", res.Commit)
	default:
		a.printf("Synced registry %.12s:\n", res.Commit)
	}
	list := func(mark string, names []string, what string) {
		for _, n := range names {
			why := what
			if reasons[n] != "" {
				why += ": " + reasons[n]
			}
			a.printf("  %s %s (%s)\n", mark, n, why)
		}
	}
	list("+", r.Installed, "installed")
	list("~", r.Updated, "updated")
	list("-", r.Removed, "removed")
	for _, n := range slices.Concat(res.Warnings, r.Notices) {
		a.printf("  ! %s\n", n)
	}
}

// plural formats a count with a noun: "1 skill", "2 skills".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// conditionOf is the state shown for a skill; empty when not installed.
func conditionOf(checks map[string]install.Condition, e policy.Entry, disabled []string) string {
	if c, ok := checks[e.InstallName]; ok {
		return string(c)
	}
	switch {
	case e.Install:
		return "pending"
	case slices.Contains(disabled, e.Skill.ID) && (e.Status == registry.Recommended || e.Status == registry.Deprecated):
		return "disabled"
	}
	return ""
}

// oneLine joins a multi-line message (such as git's stderr) into one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// terminal drops control characters (except newlines and tabs) so text from
// the registry cannot inject escape sequences into the developer's terminal.
type terminal struct{ w io.Writer }

func (t terminal) Write(p []byte) (int, error) {
	clean := bytes.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f)) {
			return r
		}
		return -1
	}, p)
	if _, err := t.w.Write(clean); err != nil {
		return 0, err
	}
	return len(p), nil
}

// spawnDetached starts this binary with args in a new session, detached from
// the agent that ran the hook: no terminal, stdio on /dev/null, nobody waits.
func spawnDetached(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// syncDue reports whether Skillcrew is initialized and the last sync is
// older than the debounce window: the only case where a hook starts a sync.
func (a *App) syncDue() bool {
	cfg, err := state.LoadConfig(a.Paths.Config())
	if err != nil {
		return false
	}
	st, _ := state.LoadState(a.Paths.State())
	since := a.Now().Sub(st.LastSync)
	return since < 0 || since >= cfg.SyncDebounce()
}
