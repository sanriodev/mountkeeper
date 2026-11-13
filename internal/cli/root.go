// Package cli defines the mountkeeper command line: argument handling,
// prompts and output. The work itself lives in the other internal packages.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
)

// Execute runs the CLI and returns the process exit code.
func Execute(version string) int {
	root := newRootCmd(version)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:")+" "+err.Error())
		return 1
	}
	return 0
}

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "mountkeeper",
		Short: "Native macOS SMB network share manager",
		Long: `Mountkeeper saves your SMB network shares and connects, disconnects and
opens them from the terminal. Passwords live in the macOS Keychain only.

Run "mountkeeper connect" without arguments for the interactive manager.`,
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
