package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/macos"
)

func newNetworkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "network",
		Short: "Show the current network and the profiles it activates",
		Long: `Show how Mountkeeper recognises the network this Mac is on, and which
profiles connect automatically here.

The listed rules are what "mountkeeper profile auto <profile> --rule <rule>"
accepts. A DNS domain is the most useful one: it also holds on other floors
of an office and over VPN, whereas a router identifies a single subnet.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			n := macos.CurrentNetwork()
			var rows [][]string
			for _, r := range networkRows(n) {
				rows = append(rows, []string{dimStyle.Render(r[0]), r[1]})
			}
			table(rows)

			fmt.Println()
			if rules := n.Rules(); len(rules) == 0 {
				fmt.Println("  " + dimStyle.Render("This network offers nothing to recognise it by."))
			} else {
				fmt.Println("  " + dimStyle.Render("Rules that match here:"))
				for _, r := range rules {
					fmt.Println("    " + r)
				}
			}

			var bound [][]string
			for _, p := range cfg.Profiles {
				if len(p.Auto) == 0 {
					continue
				}
				state := dimStyle.Render("○ not on this network")
				if n.MatchesAny(p.Auto) {
					state = okStyle.Render("● active now")
				}
				bound = append(bound, []string{boldStyle.Render(p.Name), state, dimStyle.Render(describeRules(p.Auto))})
			}
			if len(bound) > 0 {
				fmt.Println()
				fmt.Println("  " + dimStyle.Render("Profiles that connect automatically:"))
				table(bound)
			}
			return nil
		},
	}
}

func describeRules(rules []string) string {
	out := ""
	for i, r := range rules {
		if i > 0 {
			out += " or "
		}
		out += macos.DescribeRule(r)
	}
	return out
}
