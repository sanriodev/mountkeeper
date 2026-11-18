// Package cli defines the mountkeeper command line: argument handling,
// prompts and output. The work itself lives in the other internal packages.
package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
)

// Execute runs the CLI and returns the process exit code.
func Execute(version string) int {
	root := newRootCmd(version)
	if err := root.Execute(); err != nil {
		if !errors.Is(err, errSilent) {
			printError("Error:", err)
		}
		return 1
	}
	return 0
}

// errSilent ends a command with exit status 1 after it has already said
// everything there is to say.
var errSilent = errors.New("failed")

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "mountkeeper",
		Short: "Native macOS SMB network share manager",
		Long: `Mountkeeper saves your SMB network shares and connects, disconnects and
opens them from the terminal. Passwords live in the macOS Keychain only; in an
Active Directory domain a share can sign in with your Kerberos ticket instead
("mountkeeper credentials use <share> kerberos").

"mountkeeper doctor" checks everything Mountkeeper depends on.

Run "mountkeeper" without a command for the interactive interface, in which
everything is reachable with the arrow keys, Enter and Esc.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !interactive() {
				return cmd.Help()
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return runApp(cfg, false)
		},
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,

		PersistentPreRun: warnSessionMismatch,
	}
	root.CompletionOptions.HiddenDefaultCmd = true

	root.AddCommand(
		newAddCmd(),
		newRemoveCmd(),
		newListCmd(),
		newInfoCmd(),
		newStatusCmd(),
		newConnectCmd(),
		newDisconnectCmd(),
		newOpenCmd(),
		newAutoconnectCmd(),
		newFindCmd(),
		newCredentialsCmd(),
		newEditCmd(),
		newProfileCmd(),
		newNetworkCmd(),
		newHistoryCmd(),
		newDoctorCmd(),
	)
	return root
}

// completeShares offers registered share ids for shell completion.
func completeShares(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cfg.IDs(), cobra.ShellCompDirectiveNoFileComp
}

// completeOneShare is completeShares for commands taking a single share.
func completeOneShare(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeShares(cmd, args, toComplete)
}

// completeProfiles offers profile names for shell completion.
func completeProfiles(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cfg.ProfileNames(), cobra.ShellCompDirectiveNoFileComp
}
