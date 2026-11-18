package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

// report is everything status knows about one share.
type report struct {
	Share  *share.Share
	Status mount.Status
	// Reachable says whether the server answers; nil when it was not checked
	// (checking was switched off).
	Reachable *bool
	// Stale: the share is mounted, but its server no longer answers — what is
	// left after sleep, a dropped VPN or a change of network.
	Stale bool
	// Paused: the background agent stopped connecting the share by itself;
	// PauseReason says why (see pause.go).
	Paused      bool
	PauseReason string
}

// state names the share's condition; these words are the JSON contract.
func (r report) state() string {
	switch {
	case r.Stale:
		return "not_responding"
	case r.Status.State == mount.Connected:
		return "connected"
	case r.Status.State == mount.Elsewhere:
		return "connected_elsewhere"
	case r.Reachable == nil:
		return "disconnected"
	case *r.Reachable:
		return "available"
	}
	return "unreachable"
}

// reachability probes the SMB port of every given share's server at once and
// returns the result per share id. Shares on the same server share a probe.
func reachability(shares []*share.Share, timeout time.Duration) map[string]bool {
	type probe struct {
		once sync.Once
		ok   bool
	}
	var (
		mu     sync.Mutex
		probes = map[string]*probe{}
		wg     sync.WaitGroup
		result = make(map[string]bool, len(shares))
	)
	for _, s := range shares {
		port := s.Port
		if port == 0 {
			port = 445
		}
		key := net.JoinHostPort(s.Host, strconv.Itoa(port))
		p := probes[key]
		if p == nil {
			p = &probe{}
			probes[key] = p
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.once.Do(func() { p.ok = mount.Reachable(s, timeout) == nil })
			mu.Lock()
			result[s.ID] = p.ok
			mu.Unlock()
		}()
	}
	wg.Wait()
	return result
}

// inspect gathers the live state of the given shares. With check, every
// share's server is probed (in parallel, bounded by a short timeout): for
// shares that are not mounted to tell "available" from "unreachable", for
// mounted ones to find those whose server has gone away.
func inspect(cfg *config.Config, shares []*share.Share, check bool) ([]report, error) {
	table, err := mount.Table()
	if err != nil {
		return nil, err
	}
	reports := make([]report, len(shares))
	for i, s := range shares {
		reports[i] = report{Share: s, Status: mount.StatusOf(s, table)}
		reports[i].PauseReason, _, reports[i].Paused = pausedNow(cfg, s)
	}
	if check && len(shares) > 0 {
		reach := reachability(shares, 1500*time.Millisecond)
		for i := range reports {
			if ok, probed := reach[reports[i].Share.ID]; probed {
				reports[i].Reachable = &ok
				reports[i].Stale = !ok && reports[i].Status.Mounted()
			}
		}
	}
	return reports, nil
}

// describe renders the state and a detail column for humans.
func (r report) describe() (state, detail string) {
	switch r.state() {
	case "not_responding":
		state = warnStyle.Render("Not responding")
		detail = "server does not answer · mountkeeper disconnect " + r.Share.ID + " --force"
	case "connected":
		state = okStyle.Render("Connected")
		detail = spaceText(r.Status)
	case "connected_elsewhere":
		state = okStyle.Render("Connected")
		detail = "at " + share.CollapsePath(r.Status.Path)
		if space := spaceText(r.Status); space != "" {
			detail += " · " + space
		}
	case "available":
		state = dimStyle.Render("Disconnected")
		detail = "server available"
	case "unreachable":
		state = warnStyle.Render("Unreachable")
		detail = "server does not answer"
	default:
		state = dimStyle.Render("Disconnected")
	}
	detail = dimStyle.Render(detail)
	if r.Paused {
		if detail != "" {
			detail += dimStyle.Render(" · ")
		}
		detail += warnStyle.Render("auto-connect paused: " + pauseText(r.PauseReason))
	}
	return state, detail
}

func spaceText(st mount.Status) string {
	if st.Total == 0 {
		return ""
	}
	return humanBytes(st.Free) + " free of " + humanBytes(st.Total)
}

// jsonStatus is the machine-readable form of a report.
type jsonStatus struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	URL               string `json:"url"`
	State             string `json:"state"`
	Connected         bool   `json:"connected"`
	MountPoint        string `json:"mount_point"`
	MountedAt         string `json:"mounted_at,omitempty"`
	ServerReachable   *bool  `json:"server_reachable,omitempty"`
	TotalBytes        uint64 `json:"total_bytes,omitempty"`
	FreeBytes         uint64 `json:"free_bytes,omitempty"`
	AutoConnect       bool   `json:"auto_connect"`
	AutoConnectPaused bool   `json:"auto_connect_paused"`
	// PausedReason is no-password, rejected, kerberos-no-ticket,
	// kerberos-rejected, or unknown.
	PausedReason string `json:"auto_connect_paused_reason,omitempty"`
}

func (r report) json() jsonStatus {
	mp, _ := r.Share.MountPath()
	return jsonStatus{
		ID:                r.Share.ID,
		Name:              r.Share.Name,
		URL:               r.Share.URL(),
		State:             r.state(),
		Connected:         r.Status.Mounted(),
		MountPoint:        mp,
		MountedAt:         r.Status.Path,
		ServerReachable:   r.Reachable,
		TotalBytes:        r.Status.Total,
		FreeBytes:         r.Status.Free,
		AutoConnect:       r.Share.AutoConnect,
		AutoConnectPaused: r.Paused,
		PausedReason:      pausedReason(r),
	}
}

func pausedReason(r report) string {
	switch {
	case !r.Paused:
		return ""
	case r.PauseReason == "":
		return "unknown"
	}
	return r.PauseReason
}

// printPauseHints tells, below a table, how to resume each paused share.
func printPauseHints(reports []report) {
	first := true
	for _, r := range reports {
		if !r.Paused {
			continue
		}
		if first {
			fmt.Println()
			first = false
		}
		fmt.Printf("  %s %s %s\n", warnStyle.Render("!"), boldStyle.Render(r.Share.ID)+":",
			"connects automatically again after: "+pauseHint(r.Share, r.PauseReason))
	}
}

func newStatusCmd() *cobra.Command {
	var asJSON, noCheck bool
	cmd := &cobra.Command{
		Use:   "status [share...]",
		Short: "Show the live connection status of shares",
		Long: `Show the state of all shares, or of the ones named.

The state is read from the macOS mount table, so shares connected through
Finder or other tools are detected as well. For shares that are not
connected, the server is probed briefly to tell apart "could be connected
now" from "server does not answer" (skip that with --no-check).

For connected shares the server is probed as well: a share that is still in
the mount table while its server no longer answers (after sleep, a dropped
VPN or a change of network) is shown as "Not responding". Disconnect it with
"mountkeeper disconnect <share> --force".

With --json the states are: connected, connected_elsewhere, not_responding,
available, unreachable, and disconnected (when the server was not probed).`,
		Example: `  mountkeeper status
  mountkeeper status projects --json`,
		ValidArgsFunction: completeShares,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			targets, err := resolveTargets(cfg, args, false, "")
			if err != nil {
				return err
			}
			if targets == nil {
				targets = cfg.Shares
			}
			reports, err := inspect(cfg, targets, !noCheck)
			if err != nil {
				return err
			}

			if asJSON {
				out := make([]jsonStatus, len(reports))
				for i, r := range reports {
					out[i] = r.json()
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			if len(reports) == 0 {
				noShares()
				return nil
			}
			var rows [][]string
			connected := 0
			for _, r := range reports {
				state, detail := r.describe()
				d := dot(r.Status)
				if r.Stale {
					d = warnStyle.Render("●")
				}
				rows = append(rows, []string{d + " " + boldStyle.Render(r.Share.ID), state, detail})
				if r.Status.Mounted() {
					connected++
				}
			}
			table(rows)
			printPauseHints(reports)
			if len(reports) > 1 {
				fmt.Println()
				fmt.Println("  " + dimStyle.Render(fmt.Sprintf("%d of %d connected", connected, len(reports))))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	cmd.Flags().BoolVar(&noCheck, "no-check", false, "do not probe the servers of disconnected shares")
	return cmd
}
