package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
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
			if st.State == mount.Connected && !force {
				return withHint(fmt.Errorf("%s is connected", s.ID), "Disconnect it first, or remove it anyway: mountkeeper remove %s --force", s.ID)
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

			notes, err := deleteShare(cfg, s, force)
			for _, n := range notes {
				warn("%s", n)
			}
			if err != nil {
				return err
			}
			success("Removed %s", s.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "disconnect the share first if it is connected")
	return cmd
}
