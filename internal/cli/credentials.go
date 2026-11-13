package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/keychain"
)

func newCredentialsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "credentials",
		Aliases: []string{"creds"},
		Short:   "Update or remove the password stored in the Keychain",
	}

	set := &cobra.Command{
		Use:               "set <share>",
		Short:             "Store or replace the password of a share in the Keychain",
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
			if s.Guest() {
				return fmt.Errorf("%s uses guest access and needs no password", s.ID)
			}
			if err := keychain.Available(); err != nil {
				return fmt.Errorf("%w.\n  %s", err, keychainUnavailableHint())
			}
			if !interactive() {
				return errors.New("a terminal is required to enter the password")
			}
			pw, err := askPassword(fmt.Sprintf("Password for %s@%s", s.Username, s.Host))
			if err != nil {
				return err
			}
			if pw == "" {
				return errors.New("empty password; nothing was changed")
			}
			account := s.KeychainAccount
			if account == "" {
				account = s.ID
			}
			if err := keychain.Set(account, s.URL(), pw); err != nil {
				return err
			}
			if s.KeychainAccount != account {
				s.KeychainAccount = account
				if err := cfg.Save(); err != nil {
					return err
				}
			}
			clearAuthFailed(s)
			success("Password for %s saved to the Keychain", boldStyle.Render(s.ID))
			return nil
		},
	}

	remove := &cobra.Command{
		Use:               "remove <share>",
		Aliases:           []string{"rm"},
		Short:             "Delete the stored password; it is asked for on each connect instead",
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
			if s.KeychainAccount == "" {
				fmt.Println(dimStyle.Render("No password is stored for " + s.ID + "."))
				return nil
			}
			if err := keychain.Delete(s.KeychainAccount); err != nil {
				return err
			}
			s.KeychainAccount = ""
			if err := cfg.Save(); err != nil {
				return err
			}
			success("Password for %s removed from the Keychain", boldStyle.Render(s.ID))
			if s.AutoConnect {
				warn("%s has auto-connect enabled but can no longer connect unattended.", s.ID)
			}
			return nil
		},
	}

	cmd.AddCommand(set, remove)
	return cmd
}
