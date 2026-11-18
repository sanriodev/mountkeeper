package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

// resolveTargets turns the three ways of naming shares — positional names,
// --all, --profile — into a list. It returns nil when none was used.
func resolveTargets(cfg *config.Config, args []string, all bool, profile string) ([]*share.Share, error) {
	used := 0
	for _, on := range []bool{len(args) > 0, all, profile != ""} {
		if on {
			used++
		}
	}
	switch {
	case used > 1:
		return nil, errors.New("give either share names, --all or --profile, not a combination")
	case all:
		if len(cfg.Shares) == 0 {
			return nil, withHint(errors.New("no network shares are registered yet"), "Add one with: mountkeeper add smb://server/share")
		}
		return cfg.Shares, nil
	case profile != "":
		p, err := cfg.GetProfile(profile)
		if err != nil {
			return nil, err
		}
		shares := cfg.ProfileShares(p)
		if len(shares) == 0 {
			return nil, withHint(fmt.Errorf("profile %q contains no shares", p.Name), "Add some with: mountkeeper profile add %s <share...>", p.Name)
		}
		return shares, nil
	}
	var targets []*share.Share
	for _, a := range args {
		s, err := cfg.Get(a)
		if err != nil {
			return nil, err
		}
		targets = append(targets, s)
	}
	return targets, nil
}

// printShareError reports a failure for one share of several.
func printShareError(s *share.Share, err error) {
	fmt.Fprintln(os.Stderr, errStyle.Render("✗")+" "+boldStyle.Render(s.ID)+": "+err.Error())
	if hint := hintOf(err); hint != "" {
		for _, line := range strings.Split(hint, "\n") {
			fmt.Fprintln(os.Stderr, "  "+dimStyle.Render(line))
		}
	}
}

func newConnectCmd() *cobra.Command {
	var open, noOpen, all, auto bool
	var profile string
	cmd := &cobra.Command{
		Use:   "connect [share...]",
		Short: "Connect shares, or pick one interactively",
		Long: `Connect one or more shares by name, a whole profile, or everything.

Without arguments the interactive manager opens: navigate with the arrow
keys, press Enter to connect or disconnect, O for a share's options,
? for all keys.`,
		Example: `  mountkeeper connect
  mountkeeper connect projects --open
  mountkeeper connect --profile office
  mountkeeper connect --all`,
		ValidArgsFunction: completeShares,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if auto {
				return runAutoConnect(cfg)
			}

			targets, err := resolveTargets(cfg, args, all, profile)
			if err != nil {
				return err
			}
			if targets == nil {
				if !interactive() {
					return withHint(errors.New("name a share to connect"), "The interactive manager needs a terminal. Try: mountkeeper connect <share>")
				}
				return runApp(cfg, true)
			}

			failed := 0
			for _, s := range targets {
				before, _ := mount.StatusNow(s)
				path, err := connectInteractive(s)
				if err != nil {
					if len(targets) == 1 {
						return err
					}
					failed++
					printShareError(s, err)
					continue
				}
				switch {
				case before.State == mount.Elsewhere:
					success("%s is already connected at %s", boldStyle.Render(s.ID), share.CollapsePath(path))
				case before.Mounted():
					success("%s is already connected", boldStyle.Render(s.ID))
				default:
					success("Connected %s at %s", boldStyle.Render(s.ID), share.CollapsePath(path))
				}
				if (s.AutoOpen || open) && !noOpen {
					if err := openInFinder(path); err != nil {
						warn("%v", err)
					}
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d shares could not be connected", failed, len(targets))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&open, "open", "o", false, "open the share in Finder after connecting")
	f.BoolVar(&noOpen, "no-open", false, "do not open Finder, even if the share is configured to")
	f.BoolVarP(&all, "all", "a", false, "connect all registered shares")
	f.StringVarP(&profile, "profile", "p", "", "connect all shares of a profile")
	f.BoolVar(&auto, "auto", false, "connect all shares with auto-connect enabled, without prompting (used by the login agent)")
	cmd.MarkFlagsMutuallyExclusive("open", "no-open")
	cmd.MarkFlagsMutuallyExclusive("all", "auto", "profile")
	cmd.RegisterFlagCompletionFunc("profile", completeProfiles)
	return cmd
}
