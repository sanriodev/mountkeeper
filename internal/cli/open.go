package cli

import (
	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
)

func newOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "open <share>",
		Short:             "Open a share in Finder, connecting it first if needed",
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
			path, err := connectInteractive(s)
			if err != nil {
				return err
			}
			return openInFinder(path)
		},
	}
}
