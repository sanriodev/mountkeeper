package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/mount"
)

func newRemoveCmd() *cobra.Command {
	var yes, force bool
	cmd := &cobra.Command{
		Use:               "remove <share>",
		Aliases:           []string{"rm"},
		Short:             "Remove a registered share and its stored password",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeOneShare,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			s, err := cfg.Get(args[0])
			if err != nil {
				return err
			}

			st, err := mount.StatusNow(s)
			if err != nil {
				return err
			}
			// Only a mount on our own mount point is ours to tear down.
			if st.State == mount.Connected && !force {
				return fmt.Errorf("%s is connected — disconnect it first or pass --force", s.ID)
			}

			if !yes && interactive() {
				ok, err := confirm(fmt.Sprintf("Remove %s (%s)?", boldStyle.Render(s.ID), s.URL()), false)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
			}

			if st.State == mount.Connected {
				if err := mount.Unmount(st.Path, false); err != nil {
					return err
				}
			}
			if s.KeychainAccount != "" {
				if err := keychain.Delete(s.KeychainAccount); err != nil {
					warn("Could not delete the Keychain item: %v", err)
				}
			}
			clearAuthFailed(s)
			cfg.Remove(s.ID)
			if err := cfg.Save(); err != nil {
				return err
			}
			if err := syncAgent(cfg); err != nil {
				warn("Could not update automatic connecting: %v", err)
			}
			success("Removed %s", s.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "disconnect the share first if it is connected")
	return cmd
}
