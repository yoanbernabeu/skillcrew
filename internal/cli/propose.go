package cli

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/install"
	"github.com/yoanbernabeu/skillcrew/internal/propose"
)

func (a *App) proposeCommand() *cobra.Command {
	var plugin, title string
	cmd := &cobra.Command{
		Use:   "propose <skill>",
		Short: "Open a pull request on the registry with your changes to a skill",
		Long: "Propose your local changes to a team skill (or their backup, if a sync already\n" +
			"restored the team version), or contribute one of your personal skills with --plugin.\n\n" +
			skillArgHelp,
		Example: "  skillcrew propose review\n  skillcrew propose my-skill --plugin security",
		Args:    usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			v, err := a.load(ctx)
			if err != nil {
				return err
			}
			req, err := a.proposal(v, args[0], plugin)
			if err != nil {
				return err
			}
			if title != "" {
				req.Title = title
			}
			req.Base = v.cfg.Ref
			req.Branch = fmt.Sprintf("skillcrew/%s-%s", req.Name, a.Now().UTC().Format("20060102-150405"))
			req.Body = "Proposed with `skillcrew propose`."

			repo := gitx.Open(a.Paths.Registry())
			if err := repo.Fetch(ctx, v.cfg.Ref); err != nil {
				return err
			}
			res, err := propose.Propose(ctx, repo, req, a.Opener)
			if errors.Is(err, propose.ErrNoPullRequest) {
				a.printf("Branch %s pushed. Open the pull request manually: %v\n", res.Branch, err)
				return nil
			}
			if err != nil {
				return err
			}
			a.printf("Pull request opened: %s\n", res.URL)
			return nil
		},
	}
	cmd.Flags().StringVar(&plugin, "plugin", "", "registry plugin to add a personal skill to")
	cmd.Flags().StringVar(&title, "title", "", "pull request title")
	return cmd
}

// proposal finds what to propose: a team skill's local changes or backup, or
// a personal skill to add to a plugin.
func (a *App) proposal(v *view, arg, plugin string) (propose.Request, error) {
	if plugin == "" {
		e, err := v.entry(arg)
		if err != nil {
			return propose.Request{}, fmt.Errorf("%w (use --plugin to propose a personal skill)", err)
		}
		in := a.syncer().Installer
		source := filepath.Join(a.Paths.AgentsSkills(), e.InstallName)
		if in.Check(v.lock)[e.InstallName] != install.Modified {
			backup, ok := in.LatestBackup(e.InstallName)
			if !ok {
				return propose.Request{}, fmt.Errorf("no local changes to %s", e.Skill.ID)
			}
			source = backup
		}
		return propose.Request{
			SkillDir: e.Skill.Dir, Name: e.Skill.Name, Source: source,
			Title: "Update " + e.Skill.ID + " skill",
		}, nil
	}

	pluginDir, ok := v.reg.PluginDir(plugin)
	if !ok {
		return propose.Request{}, fmt.Errorf("unknown plugin %q", plugin)
	}
	for _, dir := range []string{a.Paths.AgentsSkills(), a.Paths.ClaudeSkills()} {
		source := filepath.Join(dir, arg)
		if _, owned := v.lock.Skills[arg]; !owned && isDir(source) {
			return propose.Request{
				SkillDir: path.Join(pluginDir, "skills", arg), Name: arg, Source: source,
				Title: "Add " + plugin + ":" + arg + " skill",
			}, nil
		}
	}
	return propose.Request{}, fmt.Errorf("no personal skill named %q", arg)
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
