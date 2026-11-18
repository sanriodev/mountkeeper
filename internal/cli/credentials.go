package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/kerberos"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/share"
)

func newCredentialsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "credentials",
		Aliases: []string{"creds"},
		Short:   "Manage how shares sign in: Keychain passwords or Kerberos tickets",
		Long: `Manage how shares sign in. Without a subcommand, show for every share how
it signs in and whether a password is stored. Passwords themselves are never
displayed.

Shares sign in with a password from the macOS Keychain by default. Shares in
an Active Directory domain can sign in with a Kerberos ticket instead — the
one you get from "kinit", the Kerberos SSO extension or Platform SSO:

  mountkeeper credentials scan projects             # tickets found, and which suit the share
  mountkeeper credentials use projects kerberos     # sign in with the suitable ticket
  mountkeeper credentials use projects password     # back to the Keychain password

With Kerberos there is no fallback to the password: without a valid ticket,
connecting fails and says how to get one. The stored password is kept, so
switching back needs nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return listCredentials() },
	}

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show which shares have a stored password",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return listCredentials() },
	}

	var fromStdin bool

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
				return withHint(err, "%s", keychainUnavailableHint())
			}
			var pw string
			switch {
			case fromStdin:
				// For scripts and password managers: the secret arrives on a
				// pipe, never on a command line.
				line, err := stdin.ReadString('\n')
				if err != nil && line == "" {
					return errors.New("no password received on standard input")
				}
				pw = strings.TrimRight(line, "\r\n")
			case !interactive():
				return withHint(errors.New("a terminal is required to enter the password"),
					"In a script, pipe it in: <command printing the password> | mountkeeper credentials set %s --stdin", s.ID)
			default:
				if pw, err = askPassword(fmt.Sprintf("Password for %s@%s", s.Username, s.Host)); err != nil {
					return err
				}
			}
			if err := storePassword(cfg, s, pw); err != nil {
				return err
			}
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
			had, err := forgetPassword(cfg, s)
			if err != nil {
				return err
			}
			if !had {
				fmt.Println(dimStyle.Render("No password is stored for " + s.ID + "."))
				return nil
			}
			success("Password for %s removed from the Keychain", boldStyle.Render(s.ID))
			if s.AutoConnect {
				warn("%s has auto-connect enabled but can no longer connect unattended.", s.ID)
			}
			return nil
		},
	}

	set.Flags().BoolVar(&fromStdin, "stdin", false, "read the password from standard input instead of asking")

	scan := &cobra.Command{
		Use:   "scan [share]",
		Short: "List your Kerberos tickets, and which ones suit a share",
		Long: `List the Kerberos tickets of the logged-in user (as "klist -A" does): the
principal, until when it is valid, and whether a ticket for a share's server
is held already. With a share, mark the tickets that suit it (same user name
and domain) and the one it is set to use.

Tickets come from "kinit user@REALM", the macOS Kerberos SSO extension or
Platform SSO; Mountkeeper only reads them.`,
		Example: `  mountkeeper credentials scan
  mountkeeper credentials scan projects`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeOneShare,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			var s *share.Share
			if len(args) == 1 {
				if s, err = cfg.Get(args[0]); err != nil {
					return err
				}
			}
			return printTickets(s)
		},
	}

	var principal string
	use := &cobra.Command{
		Use:   "use <share> kerberos|password",
		Short: "Choose whether a share signs in with a Kerberos ticket or its password",
		Long: `Choose how a share signs in.

kerberos  sign in with a Kerberos ticket (Active Directory). Without
          --principal, the one ticket that suits the share's user and domain
          is chosen; "mountkeeper credentials scan <share>" lists them.
password  sign in with the password from the Keychain (the default).

The stored password stays in the Keychain either way. A pause of automatic
connecting ends with the switch.`,
		Example: `  mountkeeper credentials use projects kerberos
  mountkeeper credentials use projects kerberos --principal alice@CORP.EXAMPLE.COM
  mountkeeper credentials use projects password`,
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 1 {
				return []string{"kerberos", "password"}, cobra.ShellCompDirectiveNoFileComp
			}
			return completeOneShare(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			s, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			method := strings.ToLower(args[1])
			if method != share.AuthKerberos && principal != "" {
				return errors.New("--principal only goes with kerberos")
			}
			notes, err := setSignIn(cfg, s, method, principal)
			if err != nil {
				return err
			}
			success("%s now signs in with the %s", boldStyle.Render(s.ID), signInText(s))
			for _, n := range notes {
				fmt.Println("  " + dimStyle.Render(n))
			}
			if st, err := statusNow(s); err == nil && st.Mounted() {
				fmt.Println("  " + dimStyle.Render("It is connected now; the change applies from the next connect."))
			}
			return nil
		},
	}
	use.Flags().StringVar(&principal, "principal", "", "the Kerberos principal to sign in as, e.g. alice@CORP.EXAMPLE.COM")

	cmd.AddCommand(list, set, remove, scan, use)
	return cmd
}

func listCredentials() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Shares) == 0 {
		noShares()
		return nil
	}
	available := keychain.Available() == nil
	rows := [][]string{{dimStyle.Render("SHARE"), dimStyle.Render("ACCOUNT"), dimStyle.Render("PASSWORD")}}
	for _, s := range cfg.Shares {
		var password string
		switch {
		case usesKerberos(s):
			password = okStyle.Render("Kerberos") + dimStyle.Render(" · "+kerberosPrincipalHint(s))
		case s.Guest():
			password = dimStyle.Render("not needed")
		case s.KeychainAccount == "":
			password = dimStyle.Render("asked on every connect")
		case !available:
			password = warnStyle.Render("Keychain not available")
		case keychain.Has(s.KeychainAccount):
			password = okStyle.Render("stored in Keychain")
		default:
			password = warnStyle.Render("not stored yet") + dimStyle.Render(" (saved on next connect)")
		}
		if reason, _, paused := pausedNow(cfg, s); paused {
			password += warnStyle.Render(" · auto-connect paused: " + pauseText(reason))
		}
		rows = append(rows, []string{boldStyle.Render(s.ID), accountText(s) + dimStyle.Render("@"+s.Host), password})
	}
	table(rows)
	if !available {
		warn("%s", keychainUnavailableHint())
	}
	return nil
}

// printTickets lists the user's Kerberos tickets; with a share, marks the
// ones that suit it and the one it uses.
func printTickets(s *share.Share) error {
	creds, err := scanTickets()
	if err != nil {
		return err
	}
	if len(creds) == 0 {
		fmt.Println("No Kerberos tickets found.")
		fmt.Println(dimStyle.Render("Sign in with: kinit user@REALM (or your company sign-in), then scan again."))
		return nil
	}
	now := time.Now()
	var rows [][]string
	for _, c := range creds {
		mark, validity := okStyle.Render("●"), "valid until "+c.Expires.Format("2006-01-02 15:04")
		if !c.Valid(now) {
			mark, validity = dimStyle.Render("○"), warnStyle.Render("expired "+c.Expires.Format("2006-01-02 15:04"))
		}
		var notes []string
		if s != nil {
			if usesKerberos(s) && kerberos.SamePrincipal(c.Principal, s.KerberosPrincipal) {
				notes = append(notes, okStyle.Render("used by "+s.ID))
			} else if kerberos.Matches(c, s.Username, s.Domain) {
				notes = append(notes, "suits "+s.ID)
			}
			if c.HasServiceTicket(s.Host) {
				notes = append(notes, "already signed in to "+s.Host)
			}
		}
		rows = append(rows, []string{mark + " " + boldStyle.Render(c.Principal), validity, dimStyle.Render(strings.Join(notes, " · "))})
	}
	table(rows)
	if s != nil && !usesKerberos(s) {
		fmt.Println()
		fmt.Println("  " + dimStyle.Render("Sign in with a ticket: mountkeeper credentials use "+s.ID+" kerberos"))
	}
	return nil
}
