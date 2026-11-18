package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

func dot(st mount.Status) string {
	if st.Mounted() {
		return okStyle.Render("●")
	}
	return dimStyle.Render("○")
}

func noShares() {
	fmt.Println("No network shares registered yet.")
	fmt.Println(dimStyle.Render("Add one with: mountkeeper add smb://server/share"))
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List registered shares",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if len(cfg.Shares) == 0 {
				noShares()
				return nil
			}
			sts, err := statuses(cfg)
			if err != nil {
				return err
			}
			rows := [][]string{{"  " + dimStyle.Render("NAME"), dimStyle.Render("URL"), dimStyle.Render("MOUNT POINT"), dimStyle.Render("AUTO")}}
			for _, s := range cfg.Shares {
				var auto []string
				if s.AutoConnect {
					auto = append(auto, "connect")
				}
				if s.AutoOpen {
					auto = append(auto, "open")
				}
				if len(auto) == 0 {
					auto = []string{dimStyle.Render("–")}
				}
				rows = append(rows, []string{dot(sts[s.ID]) + " " + boldStyle.Render(s.ID), s.URL(), s.MountPoint, strings.Join(auto, ", ")})
			}
			table(rows)
			return nil
		},
	}
}

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "info <share>",
		Short:             "Show the details of a share",
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
			reports, err := inspect(cfg, []*share.Share{s}, true)
			if err != nil {
				return err
			}
			r, st := reports[0], reports[0].Status

			user := accountText(s)
			password := "not stored (asked on connect)"
			switch {
			case s.Guest():
				password = "not needed"
			case s.KeychainAccount != "" && keychain.Has(s.KeychainAccount):
				password = "stored in Keychain"
			case s.KeychainAccount != "":
				password = "not stored yet (saved to Keychain on next connect)"
			}

			rows := [][]string{
				{"Name", boldStyle.Render(s.Name)},
				{"ID", s.ID},
				{"URL", s.URL()},
				{"User", user},
				{"Sign-in", signInText(s)},
				{"Password", password},
				{"Mount point", s.MountPoint},
				{"Open in Finder", yesNo(s.AutoOpen)},
				{"Auto-connect", yesNo(s.AutoConnect)},
			}
			var member []string
			for _, p := range cfg.Profiles {
				if p.Has(s.ID) {
					member = append(member, p.Name)
				}
			}
			if len(member) > 0 {
				rows = append(rows, []string{"Profiles", strings.Join(member, ", ")})
			}
			state, detail := r.describe()
			rows = append(rows, []string{"Status", dot(st) + " " + state})
			if detail != "" {
				rows = append(rows, []string{"", detail})
			}
			if r.Paused {
				rows = append(rows, []string{"", warnStyle.Render("resume with: " + pauseHint(s, r.PauseReason))})
			}
			for _, r := range rows {
				r[0] = dimStyle.Render(r[0])
			}
			table(rows)
			return nil
		},
	}
}
