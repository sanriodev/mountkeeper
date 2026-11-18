package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/macos"
)

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile",
		Aliases: []string{"profiles"},
		Short:   "Group shares into profiles that connect together",
		Long: `A profile is a named group of shares, for example everything you need at
the office. Connect or disconnect the whole group at once:

  mountkeeper connect --profile office
  mountkeeper disconnect --profile office

A share can be part of several profiles. Without a subcommand the
profiles are listed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return listProfiles() },
	}

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List profiles and their shares",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return listProfiles() },
	}

	add := &cobra.Command{
		Use:     "add <profile> <share...>",
		Short:   "Add shares to a profile, creating it if necessary",
		Example: "  mountkeeper profile add office projects development",
		Args:    cobra.MinimumNArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completeProfiles(cmd, args, toComplete)
			}
			return completeShares(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name := strings.TrimSpace(args[0])
			if err := validProfileName(name); err != nil {
				return err
			}
			var ids []string
			for _, a := range args[1:] {
				s, err := cfg.Get(a)
				if err != nil {
					return err
				}
				ids = append(ids, s.ID)
			}
			p, created := cfg.AddToProfile(name, ids...)
			if err := cfg.Save(); err != nil {
				return err
			}
			if err := syncAgent(cfg); err != nil {
				warn("The background agent could not be updated: %v", err)
			}
			verb := "Updated"
			if created {
				verb = "Created"
			}
			success("%s profile %s: %s", verb, boldStyle.Render(p.Name), strings.Join(p.Shares, ", "))
			if created {
				fmt.Println(dimStyle.Render("Connect it with: mountkeeper connect --profile " + p.Name))
			}
			return nil
		},
	}

	remove := &cobra.Command{
		Use:     "remove <profile> [share...]",
		Aliases: []string{"rm"},
		Short:   "Remove shares from a profile, or the whole profile",
		Long: `With share names, take those shares out of the profile. Without, delete
the profile. The shares themselves are never removed or disconnected.`,
		Args: cobra.MinimumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completeProfiles(cmd, args, toComplete)
			}
			cfg, err := config.Load()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			if p := cfg.FindProfile(args[0]); p != nil {
				return p.Shares, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p, err := cfg.GetProfile(args[0])
			if err != nil {
				return err
			}
			if len(args) == 1 {
				cfg.RemoveProfile(p.Name)
				if err := cfg.Save(); err != nil {
					return err
				}
				if err := syncAgent(cfg); err != nil {
					warn("The background agent could not be updated: %v", err)
				}
				success("Removed profile %s", boldStyle.Render(p.Name))
				return nil
			}

			var ids []string
			for _, a := range args[1:] {
				s, err := cfg.Get(a)
				if err != nil {
					return err
				}
				if !p.Has(s.ID) {
					return fmt.Errorf("%s is not part of profile %q", s.ID, p.Name)
				}
				ids = append(ids, s.ID)
			}
			p.Remove(ids...)
			if len(p.Shares) == 0 {
				cfg.RemoveProfile(p.Name)
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			if err := syncAgent(cfg); err != nil {
				warn("The background agent could not be updated: %v", err)
			}
			if len(p.Shares) == 0 {
				success("Removed profile %s (it had no shares left)", boldStyle.Render(p.Name))
			} else {
				success("Updated profile %s: %s", boldStyle.Render(p.Name), strings.Join(p.Shares, ", "))
			}
			return nil
		},
	}

	var here, off bool
	var rules []string
	auto := &cobra.Command{
		Use:   "auto <profile>",
		Short: "Connect a profile automatically while on a certain network",
		Long: `Bind a profile to a network. While the Mac is on that network, the
background agent keeps the profile's shares connected; when you leave, it
disconnects the ones it connected. A share you disconnect by hand stays
disconnected until you connect it again or leave the network.

--here binds the profile to the network you are on right now.
"mountkeeper network" shows the rules that match here; pass one or more
with --rule to choose yourself. Without flags the current binding is shown.`,
		Example: `  mountkeeper profile auto office --here
  mountkeeper profile auto office --rule domain:corp.example.com
  mountkeeper profile auto office --off`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p, err := cfg.GetProfile(args[0])
			if err != nil {
				return err
			}
			switch {
			case off:
				if err := setProfileAuto(cfg, p, nil); err != nil {
					return err
				}
				success("Profile %s no longer connects automatically", boldStyle.Render(p.Name))
				return nil
			case here:
				current := macos.CurrentNetwork().Rules()
				if len(current) == 0 {
					return withHint(fmt.Errorf("this network offers nothing to recognise it by"),
						"Connect to the network the profile is meant for, then try again. \"mountkeeper network\" shows what is known.")
				}
				rules = current[:1] // the most general one: a DNS domain if there is one
			case len(rules) == 0:
				if len(p.Auto) == 0 {
					fmt.Println(dimStyle.Render("Profile " + p.Name + " does not connect automatically."))
				} else {
					fmt.Println("Profile " + boldStyle.Render(p.Name) + " connects automatically on " + describeRules(p.Auto) + ".")
				}
				return nil
			}
			if err := setProfileAuto(cfg, p, rules); err != nil {
				return err
			}
			success("Profile %s connects automatically on %s", boldStyle.Render(p.Name), describeRules(p.Auto))
			if len(p.Shares) == 0 {
				warn("The profile has no shares yet: mountkeeper profile add %s <share...>", p.Name)
			}
			for _, s := range cfg.ProfileShares(p) {
				if !s.Guest() && passwordState(s) != "stored in Keychain" {
					warn("%s has no stored password, so it cannot connect unattended: mountkeeper credentials set %s", s.ID, s.ID)
				}
			}
			return nil
		},
	}
	auto.Flags().BoolVar(&here, "here", false, "bind the profile to the network the Mac is on right now")
	auto.Flags().StringArrayVar(&rules, "rule", nil, "network rule such as domain:corp.example.com (repeatable; any match activates)")
	auto.Flags().BoolVar(&off, "off", false, "stop connecting the profile automatically")
	auto.MarkFlagsMutuallyExclusive("here", "rule", "off")

	cmd.AddCommand(list, add, remove, auto)
	return cmd
}

// validProfileName rejects names that would be awkward on a command line.
func validProfileName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t,") {
		return fmt.Errorf("%q is not a usable profile name (no spaces or commas, not starting with -)", name)
	}
	return nil
}

func listProfiles() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Profiles) == 0 {
		fmt.Println("No profiles yet.")
		fmt.Println(dimStyle.Render("Create one with: mountkeeper profile add <profile> <share...>"))
		return nil
	}
	sts, err := statuses(cfg)
	if err != nil {
		return err
	}
	rows := [][]string{{dimStyle.Render("PROFILE"), dimStyle.Render("CONNECTED"), dimStyle.Render("SHARES"), dimStyle.Render("AUTOMATIC")}}
	for _, p := range cfg.Profiles {
		connected := 0
		var names []string
		for _, id := range p.Shares {
			if sts[id].Mounted() {
				connected++
				names = append(names, okStyle.Render("●")+" "+id)
			} else {
				names = append(names, dimStyle.Render("○")+" "+id)
			}
		}
		count := fmt.Sprintf("%d of %d", connected, len(p.Shares))
		if len(p.Shares) == 0 {
			count, names = dimStyle.Render("–"), []string{dimStyle.Render("(empty)")}
		}
		auto := dimStyle.Render("–")
		if len(p.Auto) > 0 {
			auto = "on " + describeRules(p.Auto)
		}
		rows = append(rows, []string{boldStyle.Render(p.Name), count, strings.Join(names, "  "), auto})
	}
	table(rows)
	return nil
}
