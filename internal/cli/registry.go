package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/scaffold"
)

func (a *App) validateCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate [dir]",
		Short: "Check a registry: skills, governance and name collisions (for CI)",
		Long: "Check a registry: skills, governance, plugin layout and name collisions.\n" +
			"In a git checkout, pinned tags (ref) and commits (sha) must also exist, and a\n" +
			"ref must be a tag, never only a branch. Errors make the command fail.",
		Args: usage(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			fsys := os.DirFS(dir)
			reg, err := registry.Load(fsys)
			if err != nil {
				if asJSON {
					return errors.Join(err, a.printJSON([]registry.Issue{{Severity: registry.Error, Message: err.Error()}}))
				}
				return err
			}
			issues := append(reg.Validate(fsys), pinIssues(cmd.Context(), dir, reg)...)
			failed := false
			for _, i := range issues {
				failed = failed || i.Severity == registry.Error
			}
			if asJSON {
				if issues == nil {
					issues = []registry.Issue{}
				}
				if err := a.printJSON(issues); err != nil {
					return err
				}
			} else {
				for _, i := range issues {
					a.printf("%s\n", i)
				}
				if !failed {
					a.printf("Registry %s is valid: %s.\n", reg.Governance.Org, plural(len(reg.Skills), "skill"))
				}
			}
			if failed {
				return errors.New("the registry is invalid")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// pinIssues checks, when dir is a git checkout, that pinned tags and commits
// exist and that no ref is only a branch. It only reads the repository.
func pinIssues(ctx context.Context, dir string, reg *registry.Registry) []registry.Issue {
	pins := map[string]registry.Rule{}
	for id, r := range reg.Governance.Skills {
		// Malformed pins are already reported by Validate.
		if (r.SHA != "" && gitx.IsObjectID(r.SHA)) || (r.Ref != "" && registry.ValidTagName(r.Ref)) {
			pins[id] = r
		}
	}
	repo := gitx.Open(dir)
	if len(pins) == 0 {
		return nil
	}
	if inside, err := repo.Git(ctx, "rev-parse", "--is-inside-work-tree"); err != nil || inside != "true" {
		return nil
	}
	if shallow, _ := repo.Git(ctx, "rev-parse", "--is-shallow-repository"); shallow == "true" {
		return []registry.Issue{{Severity: registry.Warning, Message: "shallow clone: pinned tags and commits were not checked; fetch the full history (actions/checkout: fetch-depth: 0)"}}
	}
	var issues []registry.Issue
	fail := func(id, format string, args ...any) {
		issues = append(issues, registry.Issue{Severity: registry.Error, Message: fmt.Sprintf(format, args...), Skill: id})
	}
	for _, id := range slices.Sorted(maps.Keys(pins)) {
		r := pins[id]
		if r.SHA != "" && gitx.IsObjectID(r.SHA) {
			if _, err := repo.Git(ctx, "cat-file", "-e", r.SHA+"^{commit}"); err != nil {
				fail(id, "skills: %s sha %s does not exist in this repository (or is not a commit)", id, r.SHA)
			}
		}
		if r.Ref == "" || !registry.ValidTagName(r.Ref) {
			continue
		}
		if _, err := repo.Git(ctx, "show-ref", "--verify", "--quiet", "refs/tags/"+r.Ref); err == nil {
			continue
		}
		if isBranch(ctx, repo, r.Ref) {
			fail(id, "skills: %s ref %q is a branch, not a tag; pins must be tags (git tag %s <commit>)", id, r.Ref, r.Ref)
		} else {
			fail(id, "skills: %s ref: tag %q does not exist in this repository (create and push it)", id, r.Ref)
		}
	}
	return issues
}

// isBranch reports whether name is a local or remote-tracking branch.
func isBranch(ctx context.Context, repo *gitx.Repo, name string) bool {
	out, err := repo.Git(ctx, "for-each-ref", "--format=%(refname)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return false
	}
	for _, ref := range strings.Split(out, "\n") {
		if ref == "refs/heads/"+name || (strings.HasPrefix(ref, "refs/remotes/") && strings.HasSuffix(ref, "/"+name)) {
			return true
		}
	}
	return false
}

func (a *App) registryCommand() *cobra.Command {
	var opts scaffold.Options
	initCmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a registry, or turn a Claude Code marketplace into one",
		Long: "In a Claude Code marketplace repository, generate skillcrew.yaml listing every\n" +
			"skill as available (colliding names get an install_as alias, invalid skills are\n" +
			"blocked) and report what \"skillcrew validate\" finds. Anywhere else, create a new\n" +
			"registry with an example skill.",
		Args: usage(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			opts.Version = a.Version
			res, err := scaffold.Init(dir, opts)
			if err != nil {
				return err
			}
			if res.Converted {
				a.printf("Converted the Claude Code marketplace into a Skillcrew registry.\n")
			} else {
				a.printf("Created a new Skillcrew registry.\n")
			}
			for _, f := range res.Created {
				a.printf("  + %s\n", f)
			}
			for _, n := range res.Notices {
				a.printf("  - %s\n", n)
			}
			for _, w := range res.Warnings {
				a.printf("  ! %s\n", w)
			}
			failed := false
			for _, i := range res.Issues {
				a.printf("  %s\n", i)
				failed = failed || i.Severity == registry.Error
			}
			if failed {
				return errors.New(`the registry does not pass "skillcrew validate" yet; fix the errors above, then run it again`)
			}
			a.printf("Edit skillcrew.yaml to set statuses and teams, then commit and push.\n")
			return nil
		},
	}
	initCmd.Flags().StringVar(&opts.Org, "org", "", "organization name")
	initCmd.Flags().StringVar(&opts.Owner, "owner", "", "marketplace owner name (new registries)")
	initCmd.Flags().BoolVar(&opts.Force, "force", false, "overwrite an existing skillcrew.yaml")

	cmd := group(&cobra.Command{Use: "registry", Short: "Manage a registry repository (for administrators)"})
	cmd.AddCommand(initCmd)
	return cmd
}
