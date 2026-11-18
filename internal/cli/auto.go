package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/history"
	"github.com/sanriodev/mountkeeper/internal/kerberos"
	"github.com/sanriodev/mountkeeper/internal/logtrim"
	"github.com/sanriodev/mountkeeper/internal/macos"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

// autoTargets decides which shares the background agent should keep
// connected on the given network: every share with auto-connect, plus the
// shares of every profile whose network rule matches. viaProfile marks the
// ones that are wanted only because of a profile — those are the agent's to
// disconnect again when the network is left.
func autoTargets(cfg *config.Config, net macos.Network) (wanted, viaProfile map[string]bool) {
	wanted, viaProfile = map[string]bool{}, map[string]bool{}
	for _, p := range cfg.Profiles {
		if len(p.Auto) == 0 || !net.MatchesAny(p.Auto) {
			continue
		}
		for _, id := range p.Shares {
			wanted[id], viaProfile[id] = true, true
		}
	}
	for _, s := range cfg.Shares {
		if s.AutoConnect {
			wanted[s.ID] = true
			delete(viaProfile, s.ID)
		}
	}
	return wanted, viaProfile
}

func anyNetworkProfile(cfg *config.Config) bool {
	for _, p := range cfg.Profiles {
		if len(p.Auto) > 0 {
			return true
		}
	}
	return false
}

// runAutoConnect is what the LaunchAgent runs — at login, when macOS changes
// its network configuration, and every few minutes as a fallback. It never
// prompts and stays quiet unless something happened.
//
// Being off the network is the normal case for a laptop, so unreachable
// servers are skipped without a word. A rejected password pauses the share
// instead of being retried, which could lock the account on the server.
func runAutoConnect(cfg *config.Config) error {
	trigger = history.Auto
	// launchd only ever appends to the log; keep it bounded.
	if path, err := macos.AgentLogPath(); err == nil {
		logtrim.Trim(path, macos.AgentLogMax)
	}
	logf := func(format string, a ...any) {
		fmt.Printf("%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
	}

	cleanupStale(cfg, logf)

	var net macos.Network
	if anyNetworkProfile(cfg) {
		net = macos.CurrentNetwork()
	}
	wanted, viaProfile := autoTargets(cfg, net)

	// Left a network: disconnect what was connected because of a profile.
	for _, s := range cfg.Shares {
		if !hasMarker(s, markAutoMounted) || wanted[s.ID] {
			continue
		}
		if st, err := mount.StatusNow(s); err == nil && st.State == mount.Connected {
			if err := unmountAt(s, st.Path, false); err != nil {
				logf("%s: its network is no longer active, but it could not be disconnected: %v", s.ID, err)
				continue // keep the marker and try again next time
			}
			logf("disconnected %s (its network is no longer active)", s.ID)
		}
		clearMarker(s, markAutoMounted)
	}

	var pending []*share.Share
	for _, s := range cfg.Shares {
		if wanted[s.ID] && agentMayTry(s) {
			pending = append(pending, s)
		}
	}

	// Right after a network change the server may need a moment, so try a
	// few times — but only while the Mac is online at all.
	for attempt := 0; attempt < 3 && len(pending) > 0; attempt++ {
		if attempt > 0 {
			if !macos.CurrentNetwork().Online() {
				break
			}
			time.Sleep(4 * time.Second)
		}
		var again []*share.Share
		for _, s := range pending {
			if st, err := mount.StatusNow(s); err != nil || st.Mounted() {
				continue
			}
			if mount.Reachable(s, 3*time.Second) != nil {
				// Out of reach: a hold set by disconnecting by hand ends here.
				clearMarker(s, markHeld)
				again = append(again, s)
				continue
			}
			if hasMarker(s, markHeld) {
				continue // the user disconnected it by hand and is still on this network
			}
			path, err := mountShare(s, nil)
			switch {
			case err == nil:
				setMarker(s, markAgentMounted)
				if viaProfile[s.ID] {
					setMarker(s, markAutoMounted)
				}
				logf("connected %s at %s", s.ID, path)
			case errors.Is(err, tui.ErrPasswordRequired):
				if pauseShare(s, pauseNoPassword) {
					notifyPaused(cfg, s, pauseNoPassword)
				}
				logf("%s: no password stored; automatic connecting paused until one is saved (mountkeeper credentials set %s)", s.ID, s.ID)
			case errors.Is(err, kerberos.ErrNoTicket):
				if pauseShare(s, pauseNoTicket) {
					notifyPaused(cfg, s, pauseNoTicket)
					logf("%s: no valid Kerberos ticket for %s; waiting for one (kinit, or your company sign-in)", s.ID, kerberosPrincipalHint(s))
				}
			case errors.Is(err, mount.ErrAuth) && usesKerberos(s):
				if pauseShare(s, pauseTicketRejected) {
					notifyPaused(cfg, s, pauseTicketRejected)
				}
				logf("%s: the server did not accept the Kerberos ticket of %s; automatic connecting paused (mountkeeper credentials scan %s)", s.ID, kerberosPrincipalHint(s), s.ID)
			case errors.Is(err, mount.ErrAuth):
				if pauseShare(s, pauseRejected) {
					notifyPaused(cfg, s, pauseRejected)
				}
				logf("%s: password rejected; automatic connecting paused until it is updated (mountkeeper credentials set %s)", s.ID, s.ID)
			case errors.Is(err, mount.ErrUnreachable):
				again = append(again, s) // went away between the check and the mount
			default:
				logf("%s: %v", s.ID, err)
			}
		}
		pending = again
	}
	return nil
}

// agentMayTry reports whether the agent may try to connect s. A paused share
// is left alone, except one waiting for a Kerberos ticket: checking for the
// ticket is local, and the SSO extension or the user may have renewed it.
func agentMayTry(s *share.Share) bool {
	reason, _, paused := pauseOf(s)
	return !paused || reason == pauseNoTicket
}

// Tests replace these.
var (
	agentSleep = time.Sleep
	statusNow  = mount.StatusNow
	probe      = func(shares []*share.Share) map[string]bool { return reachability(shares, 3*time.Second) }
)

// staleGrace is how long a server must stay silent before the agent gives up
// on a connection it made. Shorter outages (roaming between access points, a
// VPN reconnecting) are sat out.
const staleGrace = 60 * time.Second

// cleanupStale finds shares that are still mounted although their server no
// longer answers — what sleep, a dropped VPN or a change of network leave
// behind. While such a mount exists the share cannot be connected again.
//
// The agent force-disconnects only connections it made itself, and only once
// the server has been silent for staleGrace; the normal run then reconnects
// as soon as the server is back. Mounts made by hand are left to the user,
// with one line in the log.
func cleanupStale(cfg *config.Config, logf func(string, ...any)) {
	var mounted []*share.Share
	paths := map[string]string{}
	for _, s := range cfg.Shares {
		st, err := statusNow(s)
		if err != nil {
			continue
		}
		if !st.Mounted() {
			clearMarker(s, markAgentMounted)
			clearMarker(s, markStale)
			continue
		}
		mounted = append(mounted, s)
		paths[s.ID] = st.Path
	}
	if len(mounted) == 0 {
		return
	}

	var dead []*share.Share
	reach := probe(mounted)
	for _, s := range mounted {
		if reach[s.ID] {
			clearMarker(s, markStale)
			continue
		}
		if _, ok := markerSince(s, markStale); !ok {
			setMarker(s, markStale)
			if !hasMarker(s, markAgentMounted) {
				logf("%s: its server no longer answers; it was connected by hand, so it is left alone (mountkeeper disconnect %s --force)", s.ID, s.ID)
			}
		}
		if hasMarker(s, markAgentMounted) {
			dead = append(dead, s)
		}
	}

	// Give the server the grace period within this run, so that a mount is
	// cleaned up promptly instead of on the next five-minute run.
	for round := 0; round < 2 && len(dead) > 0; round++ {
		agentSleep(staleGrace / 2)
		reach := probe(dead)
		var still []*share.Share
		for _, s := range dead {
			if reach[s.ID] {
				clearMarker(s, markStale)
				continue
			}
			still = append(still, s)
		}
		dead = still
	}

	for _, s := range dead {
		since, ok := markerSince(s, markStale)
		if !ok || time.Since(since) < staleGrace {
			continue
		}
		if err := unmountWhy(s, paths[s.ID], true, "server not responding"); err != nil {
			logf("%s: its server no longer answers, and it could not be disconnected: %v", s.ID, err)
			continue
		}
		logf("disconnected %s: its server no longer answers", s.ID)
	}
}
