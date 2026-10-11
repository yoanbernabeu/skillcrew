package cli

import (
	"bufio"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
	"github.com/yoanbernabeu/skillcrew/internal/state"
)

func (a *App) uninstallCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove hooks, timer, team skills and Skillcrew's state",
		Long: "Remove the team skills, the agent hooks, the fallback timer and ~/.skillcrew.\n" +
			"Personal skills that team skills replaced are restored when their name is free;\n" +
			"backups of your local changes to team skills are kept.",
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := state.LoadConfig(a.Paths.Config()); errors.Is(err, state.ErrNotInitialized) {
				a.printf("Skillcrew is not initialized; there is nothing to uninstall.\n")
				return nil
			}
			if !yes {
				a.printPlan()
				if !a.confirm("Uninstall Skillcrew? [y/N] ") {
					a.printf("Aborted.\n")
					return nil
				}
			}
			unlock, err := a.lock(cmd.Context(), a.syncer())
			if err != nil {
				return err
			}
			defer unlock()
			lock, err := state.LoadLock(a.Paths.Lock())
			if err != nil {
				return err
			}
			// Remove the skills first: on failure, keep the state so uninstall can be retried.
			report, err := a.syncer().Installer.Uninstall(lock)
			for _, n := range report.Notices {
				a.printf("! %s\n", n)
			}
			if err != nil {
				return err
			}
			var errs []error
			for _, ag := range a.Agents {
				errs = append(errs, ag.UninstallHook(a.Paths.Home))
			}
			if s, err := a.Scheduler(a.Paths.Home); err == nil {
				errs = append(errs, s.Uninstall())
			}
			for _, p := range []string{a.Paths.Config(), a.Paths.Lock(), a.Paths.State(), a.Paths.Registry(), a.Paths.Cache(), a.Paths.Staging(), a.Paths.Log(), a.Paths.SyncLock()} {
				errs = append(errs, os.RemoveAll(p))
			}
			errs = append(errs, a.restorePersonalSkills())
			if err := errors.Join(errs...); err != nil {
				return err
			}
			a.printf("Skillcrew is uninstalled.")
			if entries, _ := os.ReadDir(a.Paths.Backups()); len(entries) > 0 {
				a.printf(" Backups of your local changes are kept in %s.", a.Paths.Backups())
			}
			a.printf("\n")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

// printPlan lists what uninstall removes.
func (a *App) printPlan() {
	a.printf("This removes:\n")
	if lock, err := state.LoadLock(a.Paths.Lock()); err == nil && len(lock.Skills) > 0 {
		names := slices.Sorted(maps.Keys(lock.Skills))
		a.printf("  - %s: %s\n", plural(len(names), "team skill"), strings.Join(names, ", "))
	}
	var hooks []string
	for _, ag := range a.Agents {
		if ok, err := ag.HookInstalled(a.Paths.Home); err == nil && ok {
			hooks = append(hooks, ag.Name())
		}
	}
	if len(hooks) > 0 {
		a.printf("  - the session-start hooks of %s\n", strings.Join(hooks, ", "))
	}
	if s, err := a.Scheduler(a.Paths.Home); err == nil && s.Installed() {
		a.printf("  - the fallback timer\n")
	}
	a.printf("  - the registry clone, configuration and state in %s\n", a.Paths.Root())
	a.printf("Personal skills replaced by team skills are restored when their name is free.\n")
}

// restorePersonalSkills puts back the latest backup of each personal skill
// a team skill replaced, when nothing holds its name any more.
func (a *App) restorePersonalSkills() error {
	names, err := os.ReadDir(a.Paths.Backups())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range names {
		dir := filepath.Join(a.Paths.Backups(), n.Name())
		backups, _ := os.ReadDir(dir)
		latest := map[string]string{} // destination directory -> latest backup
		for _, b := range backups {   // sorted by name, so by date
			switch {
			case strings.HasSuffix(b.Name(), "-personal-claude"):
				latest[a.Paths.ClaudeSkills()] = filepath.Join(dir, b.Name())
			case strings.HasSuffix(b.Name(), "-personal"):
				latest[a.Paths.AgentsSkills()] = filepath.Join(dir, b.Name())
			}
		}
		for _, into := range slices.Sorted(maps.Keys(latest)) {
			backup, dest := latest[into], filepath.Join(into, n.Name())
			if _, err := os.Lstat(dest); err == nil {
				a.printf("Your personal skill %s is kept in %s: %s already exists.\n", n.Name(), backup, dest)
				continue
			}
			if err := os.MkdirAll(into, 0o755); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := moveDir(backup, dest); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", dest, err))
				continue
			}
			a.printf("Restored your personal skill %s to %s.\n", n.Name(), dest)
		}
		_ = os.Remove(dir) // only if empty
	}
	_ = os.Remove(a.Paths.Backups())
	return errors.Join(errs...)
}

// moveDir renames src to dst, copying across filesystems.
func moveDir(src, dst string) error {
	err := os.Rename(src, dst)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := fsx.CopyDir(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func (a *App) confirm(prompt string) bool {
	a.printf("%s", prompt)
	answer, _ := bufio.NewReader(a.In).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func (a *App) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  usage(cobra.NoArgs),
		Run:   func(*cobra.Command, []string) { a.printf("skillcrew %s\n", a.Version) },
	}
}
