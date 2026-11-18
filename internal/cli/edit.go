package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/share"
)

func newEditCmd() *cobra.Command {
	var name, user, domain, mountPoint, rawURL string
	var open bool
	cmd := &cobra.Command{
		Use:   "edit <share>",
		Short: "Change a registered share (mount point, address, user, …)",
		Long: `Change the settings of a share. Without flags every setting is asked
interactively, with the current value as default.

The id of a share never changes, so profiles, the Keychain item and scripts
keep working when you rename it. The address, user and mount point can only
be changed while the share is disconnected.`,
		Example: `  mountkeeper edit projects
  mountkeeper edit projects --mount-point ~/Work/projects
  mountkeeper edit projects --url smb://newserver.example.com/projects
  mountkeeper edit projects --user "CORP;alice" --open=false`,
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
			flags := cmd.Flags()
			n := *s // edited copy; only stored if everything checks out

			setUser := func(v string) { applyUser(&n, v) }
			setURL := func(v string) error {
				p, err := share.ParseURL(v)
				if err != nil {
					return err
				}
				n.Host, n.Port, n.Path = p.Host, p.Port, p.Path
				if p.Username != "" {
					n.Username, n.Domain = p.Username, p.Domain
				}
				return nil
			}

			if flags.NFlag() == 0 {
				if !interactive() {
					return withHint(errors.New("nothing to change"), "Pass what to change, for example: mountkeeper edit %s --mount-point <dir>", s.ID)
				}
				if n.Name, err = ask("Name", s.Name); err != nil {
					return err
				}
				answer, err := ask("Address", s.URL())
				if err != nil {
					return err
				}
				if answer != s.URL() {
					if err := setURL(answer); err != nil {
						return err
					}
				}
				current := userField(&n)
				fmt.Println(dimStyle.Render(`Enter "-" as user name for guest access.`))
				if answer, err = ask("Username", current); err != nil {
					return err
				}
				setUser(answer)
				if n.MountPoint, err = ask("Mount point", s.MountPoint); err != nil {
					return err
				}
				if n.AutoOpen, err = confirm("Open in Finder after connecting?", s.AutoOpen); err != nil {
					return err
				}
			} else {
				if flags.Changed("name") {
					n.Name = name
				}
				if flags.Changed("url") {
					if err := setURL(rawURL); err != nil {
						return err
					}
				}
				if flags.Changed("user") {
					setUser(user)
				}
				if flags.Changed("domain") {
					n.Domain = domain
				}
				if flags.Changed("mount-point") {
					n.MountPoint = mountPoint
				}
				if flags.Changed("open") {
					n.AutoOpen = open
				}
			}

			changes, notes, err := applyEdit(cfg, s, n)
			if err != nil {
				return err
			}
			if len(changes) == 0 {
				fmt.Println(dimStyle.Render("Nothing changed."))
				return nil
			}
			success("Updated %s", boldStyle.Render(s.ID))
			var rows [][]string
			for _, c := range changes {
				rows = append(rows, []string{dimStyle.Render(c.What), c.From + dimStyle.Render(" → ") + c.To})
			}
			table(rows)
			for _, n := range notes {
				warn("%s", n)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&name, "name", "n", "", "friendly name (the id stays the same)")
	f.StringVar(&rawURL, "url", "", "new address, e.g. smb://server/share")
	f.StringVarP(&user, "user", "u", "", `user name, optionally as "DOMAIN;user"; "-" for guest access`)
	f.StringVar(&domain, "domain", "", "Windows domain / workgroup")
	f.StringVarP(&mountPoint, "mount-point", "m", "", "local mount point")
	f.BoolVar(&open, "open", false, "open Finder after connecting (--open=false to switch off)")
	return cmd
}
