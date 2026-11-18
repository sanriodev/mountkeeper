package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/macos"
)

func newFindCmd() *cobra.Command {
	var user string
	var guest bool
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "find [host]",
		Short: "Find SMB servers on the network, or the shares of a server",
		Long: `Without arguments, list the SMB servers that announce themselves on the
local network (Bonjour). Servers in a corporate domain usually do not
announce themselves; query those directly by host name.

With a host, list the shares that server offers. macOS asks for the
password itself if the server requires one.`,
		Example: `  mountkeeper find
  mountkeeper find nas.local
  mountkeeper find fileserver.example.com --user alice`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				target := args[0]
				if user != "" {
					target = user + "@" + target
				}
				if err := macos.ViewShares(target, guest); err != nil {
					return fmt.Errorf("could not list the shares of %s", args[0])
				}
				fmt.Println(dimStyle.Render("Register one with: mountkeeper add smb://" + args[0] + "/<share>"))
				return nil
			}

			servers, err := macos.Browse(wait)
			if err != nil {
				return err
			}
			if len(servers) == 0 {
				fmt.Println("No SMB servers announce themselves on this network.")
				fmt.Println(dimStyle.Render("Query a known server directly: mountkeeper find <host>"))
				return nil
			}
			rows := [][]string{{dimStyle.Render("SERVER"), dimStyle.Render("ADDRESS")}}
			for _, s := range servers {
				addr := dimStyle.Render("(not resolved)")
				if s.Host != "" {
					addr = "smb://" + s.Host
				}
				rows = append(rows, []string{boldStyle.Render(s.Name), addr})
			}
			table(rows)
			fmt.Println(dimStyle.Render("List a server's shares with: mountkeeper find <host>"))
			return nil
		},
	}
	cmd.Flags().StringVarP(&user, "user", "u", "", `user name for listing shares, optionally as "DOMAIN;user"`)
	cmd.Flags().BoolVar(&guest, "guest", false, "list shares as guest")
	cmd.Flags().DurationVar(&wait, "wait", 2*time.Second, "how long to listen for servers")
	return cmd
}
