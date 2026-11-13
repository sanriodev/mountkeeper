package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

func newConnectCmd() *cobra.Command {
	var open, noOpen, all, auto bool
	cmd := &cobra.Command{
		Use:   "connect [share...]",
		Short: "Connect shares, or pick one interactively",
		Long: `Connect one or more shares by name.

Without arguments an interactive manager opens: navigate with the arrow
keys, press Enter to connect or disconnect, O to open a share in Finder.`,
		Example: `  mountkeeper connect
  mountkeeper connect projects --open
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

			var targets []*share.Share
			switch {
			case all:
				targets = cfg.Shares
			case len(args) > 0:
				for _, a := range args {
					s, err := cfg.Get(a)
					if err != nil {
						return err
					}
					targets = append(targets, s)
				}
			case !interactive():
				return errors.New("name a share to connect (the interactive manager needs a terminal)")
			default:
				return runTUI(cfg)
			}
			if len(targets) == 0 {
				noShares()
				return nil
			}

			failed := 0
			for _, s := range targets {
				before, _ := mount.StatusNow(s)
				path, err := connectInteractive(s)
				if err != nil {
					failed++
					fmt.Println(errStyle.Render("✗") + " " + boldStyle.Render(s.ID) + ": " + err.Error())
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
	f.BoolVar(&auto, "auto", false, "connect all shares with auto-connect enabled, without prompting (used by the login agent)")
	cmd.MarkFlagsMutuallyExclusive("open", "no-open")
	cmd.MarkFlagsMutuallyExclusive("all", "auto")
	return cmd
}

func runTUI(cfg *config.Config) error {
	return tui.Run(cfg.Shares, tui.Backend{
		Statuses: func() (map[string]mount.Status, error) { return statuses(cfg) },
		Connect: func(s *share.Share, password *string) (string, error) {
			path, err := mountShare(s, password)
			if err == nil && s.AutoOpen {
				openInFinder(path)
			}
			return path, err
		},
		Disconnect: func(s *share.Share, path string) error { return mount.Unmount(path, false) },
		Open:       openInFinder,
	})
}

// runAutoConnect is what the LaunchAgent runs: it mounts every auto-connect
// share that is not mounted yet, never prompts, and stays quiet unless
// something happened. Unreachable servers are skipped silently because being
// off the network is the normal case for a laptop.
func runAutoConnect(cfg *config.Config) error {
	logf := func(format string, a ...any) {
		fmt.Printf("%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
	}
	for _, s := range cfg.Shares {
		if !s.AutoConnect {
			continue
		}
		if st, err := mount.StatusNow(s); err != nil || st.Mounted() {
			continue
		}
		if authFailed(s) {
			continue
		}
		if mount.Reachable(s, 3*time.Second) != nil {
			continue
		}
		path, err := mountShare(s, nil)
		switch {
		case err == nil:
			logf("connected %s at %s", s.ID, path)
		case errors.Is(err, tui.ErrPasswordRequired):
			markAuthFailed(s)
			logf("%s: no password stored; auto-connect paused until one is saved (mountkeeper credentials set %s)", s.ID, s.ID)
		case errors.Is(err, mount.ErrAuth):
			// Do not retry a rejected password every few minutes: that
			// could lock the account on the server.
			markAuthFailed(s)
			logf("%s: password rejected; auto-connect paused until it is updated (mountkeeper credentials set %s)", s.ID, s.ID)
		default:
			logf("%s: %v", s.ID, err)
		}
	}
	return nil
}
