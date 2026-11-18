package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/macos"
)

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "true", "on", "yes", "1":
		return true, nil
	case "false", "off", "no", "0":
		return false, nil
	}
	return false, fmt.Errorf("expected true or false, got %q", v)
}

func newAutoconnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "autoconnect [share] [true|false]",
		Short: "Connect a share automatically at login",
		Long: `Enable or disable automatic connecting for a share.

While at least one share has it enabled, a per-user LaunchAgent connects
those shares at login and re-checks them every few minutes (after sleep or
a network change). There is no daemon; the agent is removed again as soon
as no share uses it. It never prompts: the password must be in the Keychain,
or the share signs in with a Kerberos ticket.

When a password is rejected (or missing), the share is paused instead of
retried, which could lock the account; a macOS notification says so once.
A Kerberos share without a valid ticket waits and connects as soon as there
is one. Connections the agent made whose server stops answering are
force-disconnected after a minute, so that they can connect again later.
Its log is ~/Library/Logs/mountkeeper.log; "mountkeeper doctor" checks it.

Without arguments the current settings are shown.`,
		Example: `  mountkeeper autoconnect
  mountkeeper autoconnect projects true
  mountkeeper autoconnect projects false`,
		Args: cobra.MaximumNArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 1 {
				return []string{"true", "false"}, cobra.ShellCompDirectiveNoFileComp
			}
			return completeOneShare(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if len(args) == 0 {
				if len(cfg.Shares) == 0 {
					noShares()
					return nil
				}
				var rows [][]string
				for _, s := range cfg.Shares {
					state := dimStyle.Render("false")
					if s.AutoConnect {
						state = okStyle.Render("true")
						if reason, _, paused := pauseOf(s); paused {
							state += " " + warnStyle.Render("(paused: "+pauseText(reason)+")")
						}
					}
					rows = append(rows, []string{boldStyle.Render(s.ID), state})
				}
				table(rows)
				if cfg.AnyAutoConnect() && !macos.AgentInstalled() {
					warn("The login agent is not installed; re-run: mountkeeper autoconnect <share> true")
				}
				return nil
			}

			s, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			if len(args) == 1 {
				fmt.Println(s.AutoConnect)
				return nil
			}
			enable, err := parseBool(args[1])
			if err != nil {
				return err
			}

			if err := setAutoConnect(cfg, s, enable); err != nil {
				return err
			}
			if !enable {
				success("Auto-connect disabled for %s", boldStyle.Render(s.ID))
				return nil
			}
			success("Auto-connect enabled for %s", boldStyle.Render(s.ID))
			if !s.Guest() && !(s.KeychainAccount != "" && keychain.Has(s.KeychainAccount)) {
				warn("No password is stored for %s yet, so it cannot connect unattended.\n  Save one with: mountkeeper credentials set %s", s.ID, s.ID)
			}
			return nil
		},
	}
}
