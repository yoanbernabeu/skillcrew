package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) syncCommand() *cobra.Command {
	var (
		hook              string
		quiet, background bool
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Update skills from the registry now",
		Long: "Fetch the registry and update skills now.\n\n" +
			"--hook is used by agent session-start hooks: it returns at once, printing\n" +
			"nothing (or {} with --hook=json), and starts a sync in the background when\n" +
			"the last one is older than the debounce window, so an agent never waits.\n" +
			"--quiet is used by the fallback timer.",
		Args: usage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch {
			case hook != "":
				if a.syncDue() {
					_ = a.Background("sync", "--background")
				}
				if hook == "json" {
					a.printf("{}\n")
				}
				return nil
			case background:
				s := a.syncer()
				s.FetchBackoff = backgroundFetchBackoff
				s.TrustLocalTags = true // tags are immutable: checking them waits for the timer
				_, _ = s.Sync(cmd.Context(), false)
				return nil
			}
			return a.runSync(cmd.Context(), a.syncer(), !quiet, quiet)
		},
	}
	cmd.Flags().StringVar(&hook, "hook", "", "run from an agent hook (text or json output)")
	cmd.Flags().Lookup("hook").NoOptDefVal = "text"
	cmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing and respect the debounce window")
	cmd.Flags().BoolVar(&background, "background", false, "the sync started by a hook")
	_ = cmd.Flags().MarkHidden("background")
	cmd.Flags().BoolVar(&a.json, "json", false, "print JSON")
	return cmd
}
