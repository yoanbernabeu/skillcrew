package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/install"
	"github.com/yoanbernabeu/skillcrew/internal/state"
)

type level string

const (
	pass level = "ok"
	warn level = "warning"
	fail level = "error"
)

type check struct {
	Name   string `json:"name"`
	Level  level  `json:"level"`
	Detail string `json:"detail"`
}

func (a *App) doctorCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose hooks, timer, registry and installed skills",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := a.diagnose(cmd)
			if asJSON {
				if err := a.printJSON(checks); err != nil {
					return err
				}
			} else {
				marks := map[level]string{pass: "✓", warn: "!", fail: "✗"}
				for _, c := range checks {
					a.printf("%s %-12s %s\n", marks[c.Level], c.Name, c.Detail)
				}
			}
			for _, c := range checks {
				if c.Level == fail {
					return errors.New("doctor found problems")
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func (a *App) diagnose(cmd *cobra.Command) []check {
	var checks []check
	add := func(name string, l level, format string, args ...any) {
		checks = append(checks, check{Name: name, Level: l, Detail: fmt.Sprintf(format, args...)})
	}

	if _, err := exec.LookPath("git"); err != nil {
		add("git", fail, "git is not installed")
	} else {
		add("git", pass, "git found")
	}
	cfg, err := state.LoadConfig(a.Paths.Config())
	if err != nil {
		add("config", fail, "%v", err)
		return checks
	}
	if commit, err := gitx.Open(a.Paths.Registry()).Branch(cmd.Context(), cfg.Ref); err != nil {
		add("registry", fail, "%v", err)
	} else {
		add("registry", pass, "%s (%s @ %.12s)", gitx.Redact(cfg.Registry), cfg.Ref, commit)
	}

	st, err := state.LoadState(a.Paths.State())
	switch {
	case err != nil:
		add("sync", fail, "%v", err)
	case st.LastSync.IsZero():
		add("sync", warn, "never synced; run \"skillcrew sync\"")
	case st.LastError != "":
		add("sync", warn, "last sync failed: %s", oneLine(st.LastError))
	case a.Now().Sub(st.LastSync) > 2*cfg.SyncInterval():
		add("sync", warn, "last sync on %s", st.LastSync.Local().Format("2006-01-02 15:04"))
	default:
		add("sync", pass, "last sync on %s", st.LastSync.Local().Format("2006-01-02 15:04"))
	}

	if w, offline := offlineWarning(st.Warnings); offline || !st.FetchFailedAt.IsZero() {
		reason := "the last fetch failed"
		if offline {
			reason = strings.TrimSpace(strings.TrimPrefix(w, "offline:"))
		}
		add("fetch", warn, "the last sync used the last fetched registry (offline): %s; run \"git -C %s fetch\" to see the error",
			reason, a.Paths.Registry())
	}

	hooks := 0
	for _, ag := range a.Agents {
		if !ag.Detected(a.Paths.Home) {
			continue
		}
		switch ok, err := ag.HookInstalled(a.Paths.Home); {
		case err != nil:
			add(ag.Name(), fail, "%v", err)
		case !ok:
			add(ag.Name(), fail, "session-start hook missing; run \"skillcrew init %s\"", gitx.Redact(cfg.Registry))
		default:
			hooks++
			add(ag.Name(), pass, "session-start hook installed")
		}
	}

	timer := false
	if s, err := a.Scheduler(a.Paths.Home); err != nil || !s.Installed() {
		add("timer", warn, "fallback timer not installed; skills update only when an agent starts")
	} else {
		timer = true
		add("timer", pass, "fallback timer installed")
	}
	if hooks == 0 && !timer {
		add("triggers", fail, "nothing triggers syncs: no session-start hook and no timer; run \"skillcrew init %s\" without --no-hooks or --no-timer, or \"skillcrew sync\" yourself",
			gitx.Redact(cfg.Registry))
	}

	lock, err := state.LoadLock(a.Paths.Lock())
	if err != nil {
		add("skills", fail, "%v", err)
		return checks
	}
	var drifted []string
	for name, c := range a.syncer().Installer.Check(lock) {
		if c != install.OK {
			drifted = append(drifted, name+" ("+string(c)+")")
		}
	}
	sort.Strings(drifted)
	if len(drifted) > 0 {
		add("skills", warn, "%s; the next sync restores them", strings.Join(drifted, ", "))
	} else {
		add("skills", pass, "%s installed", plural(len(lock.Skills), "team skill"))
	}
	if _, err := os.Stat(filepath.Dir(a.Paths.ClaudeSkills())); err == nil {
		if broken := a.brokenLinks(lock); len(broken) > 0 {
			add("symlinks", warn, "%s not linked from %s; the next sync fixes them",
				strings.Join(broken, ", "), a.Paths.ClaudeSkills())
		} else {
			add("symlinks", pass, "team skills linked from %s", a.Paths.ClaudeSkills())
		}
	}
	if dups := a.duplicates(); len(dups) > 0 {
		add("duplicates", warn, "%s exist in both %s and %s; some agents load them twice",
			strings.Join(dups, ", "), a.Paths.ClaudeSkills(), a.Paths.AgentsSkills())
	}
	return checks
}

// brokenLinks lists the locked skills whose ~/.claude/skills link is missing
// or does not point to their copy in ~/.agents/skills.
func (a *App) brokenLinks(lock state.Lock) []string {
	var broken []string
	for name := range lock.Skills {
		dest, err := os.Readlink(filepath.Join(a.Paths.ClaudeSkills(), name))
		if err != nil || dest != filepath.Join(a.Paths.AgentsSkills(), name) {
			broken = append(broken, name)
		}
	}
	sort.Strings(broken)
	return broken
}

// duplicates lists real directories present in both skills directories.
func (a *App) duplicates() []string {
	entries, _ := os.ReadDir(a.Paths.ClaudeSkills())
	var dups []string
	for _, e := range entries {
		if e.IsDir() && isDir(filepath.Join(a.Paths.AgentsSkills(), e.Name())) {
			dups = append(dups, e.Name())
		}
	}
	return dups
}
