package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/share"
)

func newDisconnectCmd() *cobra.Command {
	var all, force bool
	cmd := &cobra.Command{
		Use:               "disconnect [share...]",
		Aliases:           []string{"unmount"},
		Short:             "Disconnect shares",
		Example:           "  mountkeeper disconnect projects\n  mountkeeper disconnect --all",
		ValidArgsFunction: completeShares,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			var targets []*share.Share
			switch {
			case all:
				targets = cfg.Shares
			case len(args) == 0:
				return errors.New("name a share to disconnect, or pass --all")
			default:
				for _, a := range args {
					s, err := cfg.Get(a)
					if err != nil {
						return err
					}
					targets = append(targets, s)
				}
			}

			failed := 0
			for _, s := range targets {
				was, err := unmountShare(s, force)
				switch {
				case err != nil:
					failed++
					fmt.Println(errStyle.Render("✗") + " " + boldStyle.Render(s.ID) + ": " + err.Error())
				case was:
					success("Disconnected %s", boldStyle.Render(s.ID))
				case !all:
					fmt.Println(dimStyle.Render("○ " + s.ID + " is not connected"))
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d shares could not be disconnected", failed)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "disconnect all registered shares")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "force the unmount even if files are still open (may lose unsaved data)")
	return cmd
}
