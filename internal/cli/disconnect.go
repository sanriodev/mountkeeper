package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
)

func newDisconnectCmd() *cobra.Command {
	var all, force bool
	var profile string
	cmd := &cobra.Command{
		Use:     "disconnect [share...]",
		Aliases: []string{"unmount"},
		Short:   "Disconnect shares",
		Long: `Disconnect shares by name, all shares of a profile, or all shares.

Apps that have files open on a share keep it busy; the error names them.
--force disconnects anyway (unsaved changes in those files may be lost). It is
also the way to get rid of a share whose server no longer answers — "Not
responding" in "mountkeeper status" — which otherwise stays in the mount table
and cannot be connected again.`,
		Example: `  mountkeeper disconnect projects
  mountkeeper disconnect --profile office
  mountkeeper disconnect --all
  mountkeeper disconnect projects --force`,
		ValidArgsFunction: completeShares,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			targets, err := resolveTargets(cfg, args, all, profile)
			if err != nil {
				return err
			}
			if targets == nil {
				return withHint(errors.New("name a share to disconnect"), "Or disconnect everything: mountkeeper disconnect --all")
			}
			named := len(args) > 0

			failed, disconnected := 0, 0
			for _, s := range targets {
				was, err := unmountShare(s, force)
				switch {
				case err != nil && len(targets) == 1:
					return err
				case err != nil:
					failed++
					printShareError(s, err)
				case was:
					disconnected++
					success("Disconnected %s", boldStyle.Render(s.ID))
				case named:
					fmt.Println(dimStyle.Render("○ " + s.ID + " is not connected"))
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d shares could not be disconnected", failed, len(targets))
			}
			if !named && disconnected == 0 {
				fmt.Println(dimStyle.Render("Nothing to disconnect: none of these shares is connected."))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&all, "all", "a", false, "disconnect all registered shares")
	f.StringVarP(&profile, "profile", "p", "", "disconnect all shares of a profile")
	f.BoolVarP(&force, "force", "f", false, "force the unmount even if files are still open (may lose unsaved data)")
	cmd.MarkFlagsMutuallyExclusive("all", "profile")
	cmd.RegisterFlagCompletionFunc("profile", completeProfiles)
	return cmd
}
