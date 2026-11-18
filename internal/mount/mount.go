// Package mount inspects the macOS mount table and mounts and unmounts SMB
// shares. Mounting goes through the NetFS framework (the same machinery
// Finder uses) so that passwords are passed in memory instead of on a
// command line, as mount_smbfs would require.
package mount

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sanriodev/mountkeeper/internal/share"
)

// Entry is one row of the kernel mount table.
type Entry struct {
	FSType string // e.g. "smbfs", "apfs"
	From   string // e.g. "//user@host/share"
	On     string // mount point
	Total  uint64 // capacity in bytes, as last reported by the server
	Free   uint64 // bytes available to the user
}

// Table returns the current mount table straight from the kernel
// (getfsstat(2)), i.e. the same data `mount` prints.
func Table() ([]Entry, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, fmt.Errorf("reading mount table: %w", err)
	}
	buf := make([]unix.Statfs_t, n+8)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil, fmt.Errorf("reading mount table: %w", err)
	}
	entries := make([]Entry, 0, n)
	for i := 0; i < n && i < len(buf); i++ {
		entries = append(entries, Entry{
			FSType: unix.ByteSliceToString(buf[i].Fstypename[:]),
			From:   unix.ByteSliceToString(buf[i].Mntfromname[:]),
			On:     unix.ByteSliceToString(buf[i].Mntonname[:]),
			Total:  buf[i].Blocks * uint64(buf[i].Bsize),
			Free:   buf[i].Bavail * uint64(buf[i].Bsize),
		})
	}
	return entries, nil
}

// State describes how a share currently relates to the mount table.
type State int

const (
	// Disconnected: the share is not mounted anywhere.
	Disconnected State = iota
	// Connected: the share is mounted on its configured mount point.
	Connected
	// Elsewhere: the share is mounted, but on another path (e.g. by Finder
	// under /Volumes).
	Elsewhere
)

// Status is the live state of one share.
type Status struct {
	State State
	Path  string // where it is mounted; empty when disconnected
	Total uint64 // capacity in bytes; 0 when disconnected or unknown
	Free  uint64 // free bytes; 0 when disconnected or unknown
}

// FormatBytes formats a size the way Finder does (decimal units).
func FormatBytes(n uint64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	value := float64(n) / float64(div)
	format := "%.1f %cB"
	if value >= 100 {
		format = "%.0f %cB"
	}
	return fmt.Sprintf(format, value, "kMGTP"[exp])
}

// Mounted reports whether the share is reachable through some mount point.
func (s Status) Mounted() bool { return s.State != Disconnected }

func (s Status) String() string {
	switch s.State {
	case Connected:
		return "Connected"
	case Elsewhere:
		return "Connected at " + share.CollapsePath(s.Path)
	}
	return "Disconnected"
}

// StatusOf determines the state of a share from a mount table.
func StatusOf(s *share.Share, table []Entry) Status {
	mp, err := s.MountPath()
	if err == nil {
		mp = resolveParent(mp)
		for _, e := range table {
			if e.On == mp && e.FSType == "smbfs" {
				return Status{State: Connected, Path: e.On, Total: e.Total, Free: e.Free}
			}
		}
	}
	for _, e := range table {
		if e.FSType == "smbfs" && sameRemote(s, e.From) {
			return Status{State: Elsewhere, Path: e.On, Total: e.Total, Free: e.Free}
		}
	}
	return Status{}
}

// StatusNow is StatusOf against a freshly read mount table.
func StatusNow(s *share.Share) (Status, error) {
	table, err := Table()
	if err != nil {
		return Status{}, err
	}
	return StatusOf(s, table), nil
}

// sameRemote compares a share with an smbfs "from" name such as
// "//DOMAIN;user@host:445/Share%20Name/sub".
func sameRemote(s *share.Share, from string) bool {
	rest, ok := strings.CutPrefix(from, "//")
	if !ok {
		return false
	}
	host, path, _ := strings.Cut(rest, "/")
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		port, _ := strconv.Atoi(p)
		if port != 445 && port != s.Port {
			return false
		}
		host = h
	}
	if un, err := url.PathUnescape(path); err == nil {
		path = un
	}
	return strings.EqualFold(strings.TrimSuffix(host, "."), s.Host) &&
		strings.EqualFold(strings.Trim(path, "/"), strings.Trim(s.Path, "/"))
}

// resolve follows symlinks (e.g. /tmp → /private/tmp) so paths compare equal
// to what the kernel reports. Unresolvable paths are returned unchanged.
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	// The leaf may not exist yet; resolve the parent instead.
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}

// resolveParent is resolve for a path that may be a mount point itself: only
// the parent directory is resolved and the last element is appended
// unchanged. Looking at the mount point would ask the file system of the
// mounted volume, which blocks for as long as an unresponsive SMB server
// takes to time out. Mount points are never symlinks (PrepareMountPoint
// insists on a directory), so nothing is lost.
func resolveParent(p string) string {
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}

// ErrMountPoint marks errors caused by an unusable mount point.
var ErrMountPoint = errors.New("unusable mount point")

// ErrUnreachable marks errors caused by a server that cannot be contacted.
var ErrUnreachable = errors.New("server unreachable")

// ErrNotResponding marks an unmount that did not finish because the volume no
// longer answers; only forcing it gets rid of it.
var ErrNotResponding = errors.New("volume not responding")

type wrapped struct {
	msg  string
	kind error
}

func (e *wrapped) Error() string { return e.msg }
func (e *wrapped) Unwrap() error { return e.kind }

func mountPointErr(format string, a ...any) error {
	return &wrapped{msg: fmt.Sprintf(format, a...), kind: ErrMountPoint}
}

func unreachableErr(format string, a ...any) error {
	return &wrapped{msg: fmt.Sprintf(format, a...), kind: ErrUnreachable}
}

// PrepareMountPoint validates a mount point and creates it when missing. It
// refuses anything that could hide existing data: files, non-empty
// directories, and directories something else is already mounted on.
func PrepareMountPoint(path string, table []Entry) (string, error) {
	if !filepath.IsAbs(path) {
		return "", mountPointErr("mount point %q must be an absolute path", path)
	}
	path = resolve(filepath.Clean(path))
	if path == "/" {
		return "", mountPointErr("refusing to mount on /")
	}
	if home, err := os.UserHomeDir(); err == nil && path == resolve(home) {
		return "", mountPointErr("refusing to mount on the home directory")
	}

	for _, e := range table {
		if e.On == path {
			return "", mountPointErr("%s is already a mount point of %s (%s)", share.CollapsePath(path), e.From, e.FSType)
		}
	}

	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", mountPointErr("cannot create the mount point %s: %v", share.CollapsePath(path), errors.Unwrap(err))
		}
		return path, nil
	case err != nil:
		return "", err
	case !info.IsDir():
		return "", mountPointErr("%s exists and is not a directory", share.CollapsePath(path))
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Name() == ".DS_Store" {
			continue
		}
		return "", mountPointErr("%s is not empty — refusing to mount over existing files", share.CollapsePath(path))
	}
	return path, nil
}

// Reachable checks that the server answers on its SMB port, so that an
// unreachable host fails within the timeout instead of hanging in the kernel.
func Reachable(s *share.Share, timeout time.Duration) error {
	port := s.Port
	if port == 0 {
		port = 445
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(s.Host, strconv.Itoa(port)), timeout)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return unreachableErr("the host name %s cannot be resolved", s.Host)
		}
		return unreachableErr("the server %s does not answer on port %d", s.Host, port)
	}
	conn.Close()
	return nil
}

// Mount mounts the share on its configured mount point and returns that path.
// password may be empty (guest access, or Kerberos/saved system credentials).
func Mount(s *share.Share, password string) (string, error) {
	mp, err := s.MountPath()
	if err != nil {
		return "", err
	}
	table, err := Table()
	if err != nil {
		return "", err
	}
	if st := StatusOf(s, table); st.State == Connected {
		return st.Path, nil
	}
	dir, err := PrepareMountPoint(mp, table)
	if err != nil {
		return "", err
	}
	if err := Reachable(s, 4*time.Second); err != nil {
		cleanup(dir)
		return "", err
	}
	path, err := netfsMount(s.MountURL(), dir, password, s.Guest())
	if err != nil {
		cleanup(dir)
		return "", err
	}
	return path, nil
}

// cleanup removes a mount point directory if (and only if) it is empty.
func cleanup(dir string) {
	os.Remove(filepath.Join(dir, ".DS_Store"))
	os.Remove(dir)
}

// BusyError is returned when a volume cannot be unmounted because it is in use.
type BusyError struct {
	Path      string
	Processes []string // e.g. "Preview (PID 812)"; may be empty
}

func (e *BusyError) Error() string {
	msg := share.CollapsePath(e.Path) + " is still in use"
	if len(e.Processes) > 0 {
		msg += " by " + strings.Join(e.Processes, ", ")
	}
	return msg
}

var dissenter = regexp.MustCompile(`dissented by PID (\d+) \(([^)]*)\)`)

// parseBusy extracts the blocking processes from diskutil's output; ok is
// false when the output does not describe a busy volume.
func parseBusy(out string) (procs []string, ok bool) {
	for _, m := range dissenter.FindAllStringSubmatch(out, -1) {
		name := strings.TrimSuffix(filepath.Base(m[2]), ".app")
		if name == "" || name == "." {
			name = "process"
		}
		procs = append(procs, fmt.Sprintf("%s (PID %s)", name, m[1]))
	}
	low := strings.ToLower(out)
	ok = len(procs) > 0 || strings.Contains(low, "dissent") || strings.Contains(low, "busy") || strings.Contains(low, "in use")
	return procs, ok
}

// Unmount unmounts the volume at path. diskutil is tried first because it
// asks running apps to let go and names the process that refuses; umount is
// the fallback for volumes diskutil does not manage.
//
// With force, open files do not prevent the unmount, and each tool gets a
// shorter time: forcing is what is done to volumes whose server no longer
// answers, where waiting longer achieves nothing.
func Unmount(path string, force bool) error {
	args := []string{"unmount"}
	timeout := 60 * time.Second
	if force {
		args = append(args, "force")
		timeout = 15 * time.Second
	}
	out, err := runTool(timeout, "/usr/sbin/diskutil", append(args, path)...)
	if err == nil {
		cleanup(path)
		return nil
	}
	timedOut := errors.Is(err, context.DeadlineExceeded)
	if timedOut && !force {
		return &wrapped{msg: fmt.Sprintf("unmounting %s timed out — the volume does not respond", share.CollapsePath(path)), kind: ErrNotResponding}
	}
	if procs, busy := parseBusy(out); busy && !timedOut {
		return &BusyError{Path: path, Processes: procs}
	}

	uargs := []string{path}
	if force {
		uargs = []string{"-f", path}
	}
	out2, err2 := runTool(timeout, "/sbin/umount", uargs...)
	if err2 == nil {
		cleanup(path)
		return nil
	}
	if errors.Is(err2, context.DeadlineExceeded) {
		return &wrapped{msg: fmt.Sprintf("unmounting %s timed out — the volume does not respond", share.CollapsePath(path)), kind: ErrNotResponding}
	}
	if strings.Contains(strings.ToLower(out2), "busy") {
		return &BusyError{Path: path}
	}
	detail := out
	if detail == "" {
		detail = out2
	}
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("unmounting %s failed: %s", share.CollapsePath(path), detail)
}

// runTool runs a system utility without a shell and returns its combined
// output collapsed to one line.
func runTool(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	text := strings.Join(strings.Fields(string(out)), " ")
	if ctx.Err() != nil {
		return text, context.DeadlineExceeded
	}
	return text, err
}
