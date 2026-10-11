package cli

import (
	"bufio"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/yoanbernabeu/skillcrew/internal/policy"
	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/state"
)

// skillArgHelp documents the <skill> argument of every command taking one.
const skillArgHelp = "<skill> (plugin:skill id or installed name), as shown by \"skillcrew list\"."

// prefCommand builds a command that edits the configuration then syncs.
// edit receives the resolved view and the argument, changes v.cfg and
// returns the confirmation printed before the sync report. When it changes
// nothing (e.g. "already installed"), it returns changed=false: the message
// is printed and nothing is saved nor synced.
func (a *App) prefCommand(use, short, long string, edit func(v *view, arg string) (msg string, changed bool, err error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := state.LoadConfig(a.Paths.Config()); err != nil {
				return err
			}
			// Hold the lock across read, edit, save and sync, so a hook sync
			// never applies the old configuration in between.
			s := a.syncer()
			unlock, err := a.lock(cmd.Context(), s)
			if err != nil {
				return err
			}
			defer unlock()
			v, err := a.load(cmd.Context())
			if err != nil {
				return err
			}
			done, changed, err := edit(v, args[0])
			if err != nil {
				return err
			}
			a.printf("%s\n", done)
			if !changed {
				return nil
			}
			if err := v.cfg.Save(a.Paths.Config()); err != nil {
				return err
			}
			return a.syncLocked(cmd.Context(), s, true)
		},
	}
}

func (a *App) disableCommand() *cobra.Command {
	return a.prefCommand("disable <skill>", "Turn off a recommended or deprecated skill",
		"Turn off a recommended or deprecated skill: it is removed and stays off.\n"+
			"Required skills cannot be disabled; remove skills you installed on demand instead.\n\n"+skillArgHelp,
		func(v *view, arg string) (string, bool, error) {
			e, err := v.entry(arg)
			if err != nil {
				return "", false, err
			}
			id := e.Skill.ID
			switch e.Status {
			case registry.Recommended, registry.Deprecated:
				if slices.Contains(v.cfg.Disabled, id) {
					return id + " is already disabled.", false, nil
				}
				v.cfg.Disabled = state.Add(v.cfg.Disabled, id)
				return "Disabled " + id + ".", true, nil
			case registry.Available:
				if slices.Contains(v.cfg.Installed, id) {
					return "", false, fmt.Errorf("%s is available and you installed it on demand; run \"skillcrew remove %s\" instead", id, id)
				}
				return "", false, fmt.Errorf("%s is available and not installed; there is nothing to disable", id)
			}
			return "", false, statusError(e)
		})
}

func (a *App) enableCommand() *cobra.Command {
	return a.prefCommand("enable <skill>", "Turn a disabled skill back on",
		"Turn a skill you disabled back on. A deprecated skill is reinstalled.\n\n"+skillArgHelp,
		func(v *view, arg string) (string, bool, error) {
			e, err := v.entry(arg)
			if err != nil {
				return "", false, err
			}
			id := e.Skill.ID
			if !slices.Contains(v.cfg.Disabled, id) {
				return "", false, fmt.Errorf("%s is not disabled", id)
			}
			v.cfg.Disabled = state.Remove(v.cfg.Disabled, id)
			if e.Status == registry.Deprecated {
				// A deprecated skill is kept only where it is installed or
				// opted in: opt in, or enabling it would install nothing.
				v.cfg.Installed = state.Add(v.cfg.Installed, id)
			}
			return "Enabled " + id + ".", true, nil
		})
}

func (a *App) installCommand() *cobra.Command {
	return a.prefCommand("install <skill>", "Install an available skill",
		"Install an available skill on demand (\"skillcrew list --available\" lists them).\n\n"+skillArgHelp,
		func(v *view, arg string) (string, bool, error) {
			e, err := v.entry(arg)
			if err != nil {
				return "", false, err
			}
			id := e.Skill.ID
			switch {
			case e.Status == registry.Available && slices.Contains(v.cfg.Installed, id):
				return id + " is already installed.", false, nil
			case e.Status == registry.Available:
				v.cfg.Installed = state.Add(v.cfg.Installed, id)
				return "Installed " + id + ".", true, nil
			case e.Install:
				return fmt.Sprintf("%s is already installed (%s).", id, e.Status), false, nil
			case slices.Contains(v.cfg.Disabled, id):
				return "", false, fmt.Errorf("%s is %s and you disabled it; run \"skillcrew enable %s\"", id, e.Status, id)
			case e.Status == registry.Deprecated:
				msg := id + " is deprecated and no longer installed on new machines"
				if d := e.Deprecation; d != nil && d.Message != "" {
					msg += ": " + d.Message
				}
				if d := e.Deprecation; d != nil && d.ReplacedBy != "" {
					msg += fmt.Sprintf("; install %s instead", d.ReplacedBy)
				}
				return "", false, fmt.Errorf("%s", msg)
			case e.Status == registry.Blocked:
				return "", false, statusError(e)
			}
			return "", false, fmt.Errorf("%s is %s but cannot be installed; \"skillcrew status\" shows why", id, e.Status)
		})
}

func (a *App) removeCommand() *cobra.Command {
	return a.prefCommand("remove <skill>", "Remove a skill you installed on demand",
		"Remove a skill you installed with \"skillcrew install\".\n"+
			"Turn off recommended and deprecated skills with \"skillcrew disable\" instead.\n\n"+skillArgHelp,
		func(v *view, arg string) (string, bool, error) {
			e, err := v.entry(arg)
			if err != nil {
				return "", false, err
			}
			id := e.Skill.ID
			switch {
			case slices.Contains(v.cfg.Installed, id):
				v.cfg.Installed = state.Remove(v.cfg.Installed, id)
				return "Removed " + id + ".", true, nil
			case e.Status == registry.Recommended || e.Status == registry.Deprecated:
				return "", false, fmt.Errorf("%s is %s, not installed on demand; run \"skillcrew disable %s\" to turn it off", id, e.Status, id)
			case e.Status == registry.Required || e.Status == registry.Blocked:
				return "", false, statusError(e)
			}
			return "", false, fmt.Errorf("%s was not installed on demand", id)
		})
}

// statusError explains why a required or blocked skill cannot be changed.
func statusError(e policy.Entry) error {
	if e.Status == registry.Blocked {
		return fmt.Errorf("%s is blocked by your organization; it is never installed", e.Skill.ID)
	}
	return fmt.Errorf("%s is %s for you; it cannot be disabled or removed", e.Skill.ID, e.Status)
}

// unknownTeam explains that a team does not exist, listing the teams.
func unknownTeam(name string, reg *registry.Registry) error {
	teams := slices.Sorted(maps.Keys(reg.Governance.Teams))
	if len(teams) == 0 {
		return fmt.Errorf("unknown team %q: the registry defines no teams", name)
	}
	return fmt.Errorf("unknown team %q; teams: %s", name, strings.Join(teams, ", "))
}

func (a *App) teamCommand() *cobra.Command {
	team := group(&cobra.Command{Use: "team", Short: "List, join or leave teams"})
	team.AddCommand(
		a.teamListCommand(),
		a.prefCommand("join <team>", "Join a team and get its skills", "", func(v *view, name string) (string, bool, error) {
			if _, ok := v.reg.Governance.Teams[name]; !ok {
				return "", false, unknownTeam(name, v.reg)
			}
			if slices.Contains(v.cfg.Teams, name) {
				return "You are already a member of team " + name + ".", false, nil
			}
			v.cfg.Teams = state.Add(v.cfg.Teams, name)
			return "Joined team " + name + ".", true, nil
		}),
		a.prefCommand("leave <team>", "Leave a team", "", func(v *view, name string) (string, bool, error) {
			if !slices.Contains(v.cfg.Teams, name) {
				return "", false, fmt.Errorf("you are not a member of team %q", name)
			}
			v.cfg.Teams = state.Remove(v.cfg.Teams, name)
			return "Left team " + name + ".", true, nil
		}),
	)
	return team
}

func (a *App) teamListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the teams of the registry and the ones you joined",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := a.load(cmd.Context())
			if err != nil {
				return err
			}
			type teamView struct {
				Name   string `json:"name"`
				Joined bool   `json:"joined"`
			}
			teams := []teamView{}
			for _, name := range slices.Sorted(maps.Keys(v.reg.Governance.Teams)) {
				teams = append(teams, teamView{Name: name, Joined: slices.Contains(v.cfg.Teams, name)})
			}
			if asJSON {
				return a.printJSON(teams)
			}
			if len(teams) == 0 {
				a.printf("The registry defines no teams.\n")
				return nil
			}
			out := bufio.NewWriter(a.Out) // the table writer writes cell by cell
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "TEAM\tMEMBER")
			for _, t := range teams {
				fmt.Fprintf(w, "%s\t%s\n", t.Name, map[bool]string{true: "joined", false: "-"}[t.Joined])
			}
			w.Flush()
			out.Flush()
			if !slices.ContainsFunc(teams, func(t teamView) bool { return t.Joined }) {
				a.printf("Join a team with \"skillcrew team join <team>\".\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}
