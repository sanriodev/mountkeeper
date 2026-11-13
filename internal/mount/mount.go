// Package mount inspects the macOS mount table and mounts and unmounts SMB
// shares. Mounting goes through the NetFS framework (the same machinery
// Finder uses) so that passwords are passed in memory instead of on a
// command line, as mount_smbfs would require.
package mount

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
		mp = resolve(mp)
		for _, e := range table {
			if e.On == mp && e.FSType == "smbfs" {
				return Status{State: Connected, Path: e.On}
			}
		}
	}
	for _, e := range table {
		if e.FSType == "smbfs" && sameRemote(s, e.From) {
			return Status{State: Elsewhere, Path: e.On}
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

// PrepareMountPoint validates a mount point and creates it when missing. It
// refuses anything that could hide existing data: files, non-empty
// directories, and directories something else is already mounted on.
func PrepareMountPoint(path string, table []Entry) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("mount point %q must be an absolute path", path)
	}
	path = resolve(filepath.Clean(path))
	if path == "/" {
		return "", errors.New("refusing to mount on /")
	}
	if home, err := os.UserHomeDir(); err == nil && path == resolve(home) {
		return "", errors.New("refusing to mount on the home directory")
	}

	for _, e := range table {
		if e.On == path {
			return "", fmt.Errorf("%s is already a mount point of %s (%s)", share.CollapsePath(path), e.From, e.FSType)
		}
	}

	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("creating mount point: %w", err)
		}
		return path, nil
	case err != nil:
		return "", err
	case !info.IsDir():
		return "", fmt.Errorf("%s exists and is not a directory", share.CollapsePath(path))
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Name() == ".DS_Store" {
			continue
		}
		return "", fmt.Errorf("%s is not empty — refusing to mount over existing files", share.CollapsePath(path))
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
			return fmt.Errorf("host %s not found — is the network/VPN connected?", s.Host)
		}
		return fmt.Errorf("server %s is not reachable on port %d — is the network/VPN connected?", s.Host, port)
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
	Path   string
	Detail string
}

func (e *BusyError) Error() string {
	msg := fmt.Sprintf("%s is still in use", share.CollapsePath(e.Path))
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg + "\nClose the files or apps using it, or retry with --force."
}

// Unmount unmounts the volume at path using diskutil, which asks running
// apps to let go and names the process that refuses.
func Unmount(path string, force bool) error {
	args := []string{"unmount"}
	if force {
		args = append(args, "force")
	}
	args = append(args, path)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/diskutil", args...).CombinedOutput()
	if err == nil {
		cleanup(path)
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("unmounting %s timed out", share.CollapsePath(path))
	}
	detail := strings.Join(strings.Fields(string(bytes.TrimSpace(out))), " ")
	low := strings.ToLower(detail)
	if strings.Contains(low, "dissent") || strings.Contains(low, "busy") || strings.Contains(low, "in use") {
		return &BusyError{Path: path, Detail: detail}
	}
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("unmounting %s failed: %s", share.CollapsePath(path), detail)
}
