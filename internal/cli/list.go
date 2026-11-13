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

func stateText(st mount.Status) string {
	if st.Mounted() {
		return okStyle.Render(st.String())
	}
	return dimStyle.Render(st.String())
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

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the live connection status of all shares",
		Long: `Show whether each registered share is currently mounted.

The state is read from the macOS mount table, so shares connected through
Finder or other tools are detected as well.`,
		Args: cobra.NoArgs,
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
			var rows [][]string
			for _, s := range cfg.Shares {
				rows = append(rows, []string{dot(sts[s.ID]) + " " + boldStyle.Render(s.ID), stateText(sts[s.ID])})
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
			st, err := mount.StatusNow(s)
			if err != nil {
				return err
			}

			user := s.Username
			switch {
			case s.Guest():
				user = "guest"
			case s.Domain != "":
				user = s.Domain + `\` + user
			}
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
				{"Password", password},
				{"Mount point", s.MountPoint},
				{"Open in Finder", yesNo(s.AutoOpen)},
				{"Auto-connect", yesNo(s.AutoConnect)},
				{"Status", dot(st) + " " + stateText(st)},
			}
			if st.State == mount.Elsewhere {
				rows = append(rows, []string{"", dimStyle.Render("mounted outside Mountkeeper at " + share.CollapsePath(st.Path))})
			}
			if authFailed(s) {
				rows = append(rows, []string{"", warnStyle.Render("auto-connect paused: the stored password was rejected")})
			}
			for _, r := range rows {
				r[0] = dimStyle.Render(r[0])
			}
			table(rows)
			return nil
		},
	}
}
