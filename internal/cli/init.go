package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	neturl "net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/state"
)

func (a *App) initCommand() *cobra.Command {
	var (
		ref              string
		teams            []string
		noHooks, noTimer bool
	)
	cmd := &cobra.Command{
		Use:   "init <registry>",
		Short: "Join an organization: clone its registry, install hooks and timer, sync",
		Long: "Join an organization from its registry repository (any Git URL, local path, or\n" +
			"host/owner/repo shorthand). Skillcrew reuses your existing Git credentials.",
		Example: "  skillcrew init github.com/acme/skills --team backend",
		Args:    usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			url := gitx.NormalizeURL(args[0])
			if isLocalPath(url) {
				abs, err := filepath.Abs(url)
				if err != nil {
					return err
				}
				url = abs // git resolves relative URLs from the clone, not from here
			}
			cfg, reg, err := a.setup(ctx, url, ref, teams)
			if err != nil {
				return err
			}
			hooks := 0
			if noHooks {
				a.printf("  - session-start hooks were not installed (--no-hooks)\n")
			} else if hooks, err = a.installHooks(); err != nil {
				return err
			} else if hooks == 0 {
				a.printf("  - no agent detected: no session-start hook installed\n")
			}
			timer := false
			if noTimer {
				a.printf("  - fallback timer was not installed (--no-timer)\n")
			} else {
				timer = a.installTimer(cfg)
			}
			if hooks == 0 && !timer {
				a.printf("  ! Nothing will sync your skills automatically: run \"skillcrew sync\" yourself, or re-run\n" +
					"    \"skillcrew init\" once an agent is installed (without --no-hooks or --no-timer).\n")
			}
			s := a.syncer()
			s.NoFetch = true // setup just fetched the registry
			if err := a.runSync(ctx, s, true, false); err != nil {
				return err
			}
			if names := slices.Sorted(maps.Keys(reg.Governance.Teams)); len(names) > 0 && len(cfg.Teams) == 0 {
				a.printf("Your organization has teams (%s): join yours with \"skillcrew team join <team>\".\n", strings.Join(names, ", "))
			}
			a.printf("Start a new agent session to load the skills. \"skillcrew list --available\" shows the skills you can add.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "registry branch to follow (default: the registry's default branch)")
	cmd.Flags().StringSliceVar(&teams, "team", nil, "team to join (repeatable)")
	cmd.Flags().BoolVar(&noHooks, "no-hooks", false, "do not install agent session-start hooks")
	cmd.Flags().BoolVar(&noTimer, "no-timer", false, "do not install the fallback timer")
	return cmd
}

// setup clones the registry and writes the configuration. It is idempotent
// for the same registry and leaves nothing behind on failure.
func (a *App) setup(ctx context.Context, url, ref string, teams []string) (state.Config, *registry.Registry, error) {
	if err := checkURL(url); err != nil {
		return state.Config{}, nil, err
	}
	if err := os.MkdirAll(a.Paths.Root(), 0o700); err != nil {
		return state.Config{}, nil, err
	}
	if err := os.Chmod(a.Paths.Root(), 0o700); err != nil {
		return state.Config{}, nil, err
	}
	cfg, err := state.LoadConfig(a.Paths.Config())
	switch {
	case err == nil && !sameRegistry(cfg.Registry, url):
		return cfg, nil, fmt.Errorf("already initialized with %s; run \"skillcrew uninstall\" first", gitx.Redact(cfg.Registry))
	case err == nil && ref != "" && ref != cfg.Ref:
		return cfg, nil, fmt.Errorf("already following branch %s; run \"skillcrew uninstall\" first to follow %s", cfg.Ref, ref)
	case err == nil:
		url = cfg.Registry // the same repository, maybe spelled differently
		cfg.Teams = mergeTeams(cfg.Teams, teams)
	case errors.Is(err, state.ErrNotInitialized):
		cfg = state.Config{Registry: url, Ref: ref, Teams: mergeTeams(nil, teams)}
	default:
		return cfg, nil, err
	}

	fresh := false
	repo := gitx.Open(a.Paths.Registry())
	if _, err := os.Stat(a.Paths.Registry()); errors.Is(err, os.ErrNotExist) {
		a.printf("Cloning %s...\n", gitx.Redact(url))
		if repo, err = gitx.Clone(ctx, url, a.Paths.Registry()); err != nil {
			os.RemoveAll(a.Paths.Registry())
			return cfg, nil, gitError(err)
		}
		fresh = true
	} else if remote, err := repo.RemoteURL(ctx); err != nil || !sameRegistry(remote, url) {
		return cfg, nil, fmt.Errorf("%s holds a clone of another repository; run \"skillcrew uninstall\" first", a.Paths.Registry())
	}
	cleanup := func(err error) (state.Config, *registry.Registry, error) {
		if fresh {
			os.RemoveAll(a.Paths.Registry())
		}
		return cfg, nil, err
	}
	if cfg.Ref == "" {
		if cfg.Ref, err = repo.DefaultBranch(ctx); err != nil {
			return cleanup(err)
		}
	}
	// A fresh clone has just fetched the default branch: no second round trip.
	if _, err := repo.Branch(ctx, cfg.Ref); !fresh || err != nil {
		if err := repo.Fetch(ctx, cfg.Ref); err != nil {
			return cleanup(gitError(err))
		}
	}
	commit, err := repo.Branch(ctx, cfg.Ref)
	if err != nil {
		return cleanup(err)
	}
	snap, err := a.syncer().Store().Registry(ctx, commit)
	if err != nil {
		return cleanup(fmt.Errorf("%s is not a Skillcrew registry: %w", gitx.Redact(url), err))
	}
	reg := snap.Registry
	for _, t := range cfg.Teams {
		if _, ok := reg.Governance.Teams[t]; !ok {
			return cleanup(unknownTeam(t, reg))
		}
	}
	if err := cfg.Save(a.Paths.Config()); err != nil {
		return cleanup(err)
	}
	a.printf("Joined organization %s (registry %s, branch %s).\n", reg.Governance.Org, gitx.Redact(url), cfg.Ref)
	return cfg, reg, nil
}

// gitError puts a git error on one line and adds a hint for authentication
// failures.
func gitError(err error) error {
	msg := oneLine(err.Error())
	if hint := authHint(err); hint != "" {
		msg += "\n" + hint
	}
	return errors.New(msg)
}

// authHint suggests how to set up Git credentials when err is an
// authentication failure.
func authHint(err error) string {
	msg := err.Error()
	for _, s := range []string{"could not read Username", "could not read Password", "Permission denied (publickey", "Authentication failed", "terminal prompts disabled"} {
		if strings.Contains(msg, s) {
			return "Git could not authenticate to the registry. For HTTPS, set up a credential helper\n" +
				"(\"gh auth setup-git\" for GitHub, \"glab auth setup-git\" for GitLab); for SSH, load your key\n" +
				"(ssh-add) and check \"ssh -T git@<host>\". See https://github.com/yoanbernabeu/skillcrew/blob/main/docs/guide.md"
		}
	}
	return ""
}

// isLocalPath reports whether a registry URL is a path on this machine.
func isLocalPath(url string) bool {
	return filepath.IsAbs(url) || strings.HasPrefix(url, "./") || strings.HasPrefix(url, "../")
}

// sameRegistry reports whether two registry URLs name the same repository:
// a local path and its file:// URL, with or without a trailing slash or
// (for remote URLs) ".git".
func sameRegistry(a, b string) bool {
	return canonicalURL(a) == canonicalURL(b)
}

func canonicalURL(url string) string {
	if p, ok := strings.CutPrefix(url, "file://"); ok {
		url = p
	}
	if isLocalPath(url) {
		if abs, err := filepath.Abs(url); err == nil {
			url = abs
		}
		if resolved, err := filepath.EvalSymlinks(url); err == nil {
			url = resolved
		}
		return filepath.Clean(url)
	}
	return strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
}

func mergeTeams(current, more []string) []string {
	for _, t := range more {
		current = state.Add(current, t)
	}
	return slices.Clip(current)
}

// installHooks installs the session-start hook of every detected agent and
// returns how many are installed.
func (a *App) installHooks() (int, error) {
	bin, err := a.Executable()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, ag := range a.Agents {
		if !ag.Detected(a.Paths.Home) {
			continue
		}
		if err := ag.InstallHook(a.Paths.Home, bin); err != nil {
			a.printf("  ! %s: cannot install the session-start hook: %v\n", ag.Name(), err)
			continue
		}
		n++
		a.printf("  ✓ %s: session-start hook installed\n", ag.Name())
	}
	return n, nil
}

// installTimer installs the fallback timer and reports whether it did.
func (a *App) installTimer(cfg state.Config) bool {
	err := func() error {
		bin, err := a.Executable()
		if err != nil {
			return err
		}
		s, err := a.Scheduler(a.Paths.Home)
		if err != nil {
			return err
		}
		return s.Install(bin, cfg.SyncInterval())
	}()
	if err != nil {
		a.printf("  ! fallback timer not installed (%v); skills still update when an agent starts\n", err)
		return false
	}
	a.printf("  ✓ fallback timer installed (every %s)\n", cfg.SyncInterval())
	return true
}

// checkURL rejects HTTP(S) URLs embedding credentials, whether a
// password or a token given as the user name: Skillcrew stores the URL in
// plain text and relies on Git credential helpers instead (F-REG-02). SSH
// user names (ssh://git@host/..., git@host:...) are not secrets.
func checkURL(raw string) error {
	if scheme, _, ok := strings.Cut(raw, "://"); ok && (strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "git")) {
		return fmt.Errorf("%s:// is not encrypted; use an https:// or ssh URL", strings.ToLower(scheme))
	}
	u, err := neturl.Parse(raw)
	if err != nil || u.User == nil {
		return nil
	}
	if s := strings.ToLower(u.Scheme); s == "http" || s == "https" {
		return errors.New("the registry URL embeds credentials; remove them and use a Git credential helper (e.g. gh auth setup-git)")
	}
	return nil
}
