package cli

import (
	"bufio"
	"cmp"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/install"
	"github.com/yoanbernabeu/skillcrew/internal/policy"
	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/state"
)

type skillView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Wanted     bool   `json:"wanted"`
	State      string `json:"state,omitempty"`  // empty when not installed
	Commit     string `json:"commit,omitempty"` // registry commit of the installed copy
	Deprecated string `json:"deprecated,omitempty"`
}

func newSkillView(e policy.Entry, cfg state.Config, lock state.Lock, checks map[string]install.Condition) skillView {
	v := skillView{
		ID: e.Skill.ID, Name: e.InstallName, Status: string(e.Status),
		Wanted: e.Install, State: conditionOf(checks, e, cfg.Disabled),
	}
	if l, ok := lock.Skills[e.InstallName]; ok && l.ID == e.Skill.ID {
		v.Commit = l.Commit
	}
	if d := e.Deprecation; d != nil {
		v.Deprecated = d.Message
		if v.Deprecated == "" {
			v.Deprecated = "deprecated"
		}
		if d.ReplacedBy != "" {
			v.Deprecated += " (replaced by " + d.ReplacedBy + ")"
		}
	}
	return v
}

// printSkills prints a table of skills, then their deprecation notices: in
// the table they would stretch its columns. The output is buffered: the
// table writer writes cell by cell.
func (a *App) printSkills(skills []skillView) {
	out := bufio.NewWriter(a.Out)
	defer out.Flush()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SKILL\tID\tSTATUS\tSTATE\tVERSION")
	for _, s := range skills {
		version := "-"
		if s.Commit != "" {
			version = fmt.Sprintf("%.12s", s.Commit)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.Name, s.ID, s.Status, cmp.Or(s.State, "-"), version)
	}
	w.Flush()
	for _, s := range skills {
		if s.Deprecated != "" {
			fmt.Fprintf(out, "%s is deprecated: %s\n", s.ID, s.Deprecated)
		}
	}
}

func (a *App) statusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show your organization, teams and installed skills",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := a.load(cmd.Context())
			if err != nil {
				return err
			}
			st, err := state.LoadState(a.Paths.State())
			if err != nil {
				return err
			}
			checks := a.syncer().Installer.Check(v.lock)
			out := struct {
				Org       string      `json:"org"`
				Registry  string      `json:"registry"`
				Ref       string      `json:"ref"`
				Commit    string      `json:"commit"`
				Teams     []string    `json:"teams"`
				LastSync  time.Time   `json:"last_sync"`
				LastError string      `json:"last_error,omitempty"`
				Notices   []string    `json:"notices,omitempty"`
				Skills    []skillView `json:"skills"`
			}{
				Org: v.reg.Governance.Org, Registry: gitx.Redact(v.cfg.Registry), Ref: v.cfg.Ref,
				Commit: v.lock.Commit, Teams: nonNil(v.cfg.Teams), LastSync: st.LastSync,
				LastError: st.LastError, Notices: union(v.plan.Warnings, st.Warnings, a.takeNotices()), Skills: []skillView{},
			}
			for _, e := range v.plan.Wanted() {
				out.Skills = append(out.Skills, newSkillView(e, v.cfg, v.lock, checks))
			}
			if asJSON {
				return a.printJSON(out)
			}
			a.printf("Organization  %s\n", out.Org)
			a.printf("Registry      %s (%s @ %.12s)\n", out.Registry, out.Ref, out.Commit)
			if len(out.Teams) > 0 {
				a.printf("Teams         %s\n", strings.Join(out.Teams, ", "))
			}
			if !out.LastSync.IsZero() {
				a.printf("Last sync     %s\n", out.LastSync.Local().Format("2006-01-02 15:04"))
			}
			if out.LastError != "" {
				a.printf("Last error    %s\n", oneLine(out.LastError))
			}
			a.printf("\n")
			a.printSkills(out.Skills)
			for _, n := range out.Notices {
				a.printf("! %s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func (a *App) listCommand() *cobra.Command {
	var asJSON, available bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the registry skills and their status for you",
		Args:  usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := a.load(cmd.Context())
			if err != nil {
				return err
			}
			checks := a.syncer().Installer.Check(v.lock)
			skills := []skillView{}
			for _, e := range v.plan.Entries {
				if available && (e.Install || e.Status != registry.Available) {
					continue
				}
				skills = append(skills, newSkillView(e, v.cfg, v.lock, checks))
			}
			switch {
			case asJSON:
				return a.printJSON(skills)
			case available && len(skills) == 0:
				a.printf("No skills available to install.\n")
			default:
				a.printSkills(skills)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&available, "available", false, "only show skills you can install")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// union concatenates lists in order, dropping duplicates: the last sync's
// notices already contain the plan warnings it saw.
func union(lists ...[]string) []string {
	var out []string
	for _, s := range slices.Concat(lists...) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
