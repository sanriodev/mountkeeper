package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/share"
)

func newAddCmd() *cobra.Command {
	var (
		name, user, domain, mountPoint  string
		useKeychain, autoOpen, autoConn bool
		yes                             bool
	)
	cmd := &cobra.Command{
		Use:   "add <smb-url>",
		Short: "Register a network share",
		Long: `Register an SMB share so it can be connected by name.

Anything not given as a flag is asked interactively; pass --yes to accept
the defaults instead. Passwords are never accepted in the URL or as a flag.`,
		Example: `  mountkeeper add smb://fileserver.example.com/projects
  mountkeeper add smb://alice@nas.local/media --name media --autoconnect
  mountkeeper add //nas.local/backup --user alice --mount-point ~/Backup --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := share.ParseURL(args[0])
			if err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			flags := cmd.Flags()
			prompt := interactive() && !yes

			// Name and id.
			if flags.Changed("name") {
				s.Name = name
			} else if prompt {
				if s.Name, err = ask("Name", s.Name); err != nil {
					return err
				}
			}
			if s.ID = share.Slug(s.Name); s.ID == "" {
				return fmt.Errorf("the name %q contains no usable characters", s.Name)
			}
			if cfg.Find(s.ID) != nil {
				return fmt.Errorf("a share named %q already exists (choose another with --name)", s.ID)
			}

			// Account.
			if flags.Changed("user") {
				d, u := share.SplitUser(user)
				s.Username = u
				if d != "" {
					s.Domain = d
				}
			} else if prompt {
				if s.Username == "" {
					fmt.Println(dimStyle.Render("Leave the user name empty for guest access."))
				}
				answer, err := ask("Username", s.Username)
				if err != nil {
					return err
				}
				if d, u := share.SplitUser(answer); d != "" {
					s.Domain, s.Username = d, u
				} else {
					s.Username = u
				}
			}
			if flags.Changed("domain") {
				s.Domain = domain
			}

			// Mount point.
			s.MountPoint = share.DefaultMountRoot + "/" + s.ID
			if flags.Changed("mount-point") {
				s.MountPoint = mountPoint
			} else if prompt {
				if s.MountPoint, err = ask("Mount point", s.MountPoint); err != nil {
					return err
				}
			}
			abs, err := s.MountPath()
			if err != nil {
				return fmt.Errorf("mount point: %w", err)
			}
			s.MountPoint = share.CollapsePath(abs)

			// Keychain.
			password := ""
			if !s.Guest() {
				store := true
				if flags.Changed("keychain") {
					store = useKeychain
				}
				if store && keychain.Available() != nil {
					// Do not offer something that cannot work, and do not let
					// macOS pop up its "keychain cannot be found" dialog.
					warn("The password cannot be stored in the Keychain; it will be asked on each connect.\n  %s", keychainUnavailableHint())
					store = false
				} else if !flags.Changed("keychain") && prompt {
					if store, err = confirm("Store the password in the macOS Keychain?", true); err != nil {
						return err
					}
				}
				if store {
					s.KeychainAccount = s.ID
					if prompt {
						password, err = askPassword(fmt.Sprintf("Password for %s@%s %s", s.Username, s.Host, dimStyle.Render("(empty: ask on first connect)")))
						if err != nil {
							return err
						}
					}
				}
			}

			// Finder and auto-connect.
			if flags.Changed("open") {
				s.AutoOpen = autoOpen
			} else if prompt {
				if s.AutoOpen, err = confirm("Open in Finder after connecting?", false); err != nil {
					return err
				}
			}
			if flags.Changed("autoconnect") {
				s.AutoConnect = autoConn
			} else if prompt {
				if s.AutoConnect, err = confirm("Connect automatically at login?", false); err != nil {
					return err
				}
			}

			if err := cfg.Add(s); err != nil {
				return err
			}
			if password != "" {
				if err := keychain.Set(s.KeychainAccount, s.URL(), password); err != nil {
					return fmt.Errorf("saving the password: %w", err)
				}
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			success("Added %s (%s → %s)", boldStyle.Render(s.ID), s.URL(), s.MountPoint)

			if s.AutoConnect {
				if err := syncAgent(cfg); err != nil {
					warn("Could not set up automatic connecting: %v", err)
				}
				if !s.Guest() && password == "" {
					warn("Automatic connecting needs a stored password: mountkeeper credentials set %s", s.ID)
				}
			}
			fmt.Println(dimStyle.Render("Connect with: mountkeeper connect " + s.ID))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&name, "name", "n", "", "friendly name (default: the share name)")
	f.StringVarP(&user, "user", "u", "", `user name, optionally as "DOMAIN;user"`)
	f.StringVar(&domain, "domain", "", "Windows domain / workgroup")
	f.StringVarP(&mountPoint, "mount-point", "m", "", "local mount point (default: ~/Network/<name>)")
	f.BoolVar(&useKeychain, "keychain", true, "store the password in the macOS Keychain")
	f.BoolVar(&autoOpen, "open", false, "open Finder after connecting")
	f.BoolVar(&autoConn, "autoconnect", false, "connect automatically at login")
	f.BoolVarP(&yes, "yes", "y", false, "do not ask questions; use defaults for everything not given")
	return cmd
}
