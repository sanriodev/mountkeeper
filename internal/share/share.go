// Package share holds the Share model and SMB URL handling. It contains no
// macOS-specific code and no I/O besides resolving the home directory.
package share

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultMountRoot is where shares are mounted unless configured otherwise.
const DefaultMountRoot = "~/Network"

// Share is one registered network share. Credentials are never part of it;
// KeychainAccount only names the Keychain item that holds the password.
type Share struct {
	ID              string `yaml:"id"`
	Name            string `yaml:"name"`
	Protocol        string `yaml:"protocol"`
	Host            string `yaml:"host"`
	Port            int    `yaml:"port,omitempty"`
	Path            string `yaml:"path"`
	Domain          string `yaml:"domain,omitempty"`
	Username        string `yaml:"username,omitempty"`
	MountPoint      string `yaml:"mount_point"`
	KeychainAccount string `yaml:"keychain_account,omitempty"`
	AutoOpen        bool   `yaml:"auto_open"`
	AutoConnect     bool   `yaml:"auto_connect"`
}

// ErrPasswordInURL is returned when a URL carries an inline password.
var ErrPasswordInURL = errors.New("the URL contains a password; remove it — Mountkeeper asks for it and stores it in the Keychain")

// ParseURL turns smb://[domain;]user@host[:port]/share[/sub/dir] into a Share.
// "//host/share" is accepted as shorthand. Name, ID and MountPoint are set to
// defaults derived from the share name.
func ParseURL(raw string) (*Share, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "//"):
		raw = "smb:" + raw
	case !strings.Contains(raw, "://"):
		return nil, fmt.Errorf("%q is not an SMB URL (expected smb://host/share)", raw)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "smb") {
		return nil, fmt.Errorf("unsupported protocol %q (only smb:// is supported)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("the URL has no host")
	}

	s := &Share{Protocol: "smb", Host: strings.ToLower(u.Hostname())}

	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid port %q", p)
		}
		if n != 445 {
			s.Port = n
		}
	}

	if u.User != nil {
		if _, has := u.User.Password(); has {
			return nil, ErrPasswordInURL
		}
		s.Domain, s.Username = SplitUser(u.User.Username())
	}

	path := "/" + strings.Trim(u.Path, "/")
	if path == "/" {
		return nil, errors.New("the URL has no share name (expected smb://host/share)")
	}
	for _, seg := range strings.Split(path[1:], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return nil, fmt.Errorf("invalid share path %q", u.Path)
		}
	}
	s.Path = path

	s.Name = filepath.Base(path)
	s.ID = Slug(s.Name)
	if s.ID == "" {
		return nil, fmt.Errorf("cannot derive a name from %q; pass --name", path)
	}
	s.MountPoint = DefaultMountRoot + "/" + s.ID
	return s, nil
}

// SplitUser separates "DOMAIN;user" or "DOMAIN\user" into its parts.
func SplitUser(v string) (domain, user string) {
	if i := strings.IndexAny(v, `;\`); i >= 0 {
		return v[:i], v[i+1:]
	}
	return "", v
}

// Slug reduces a name to a lowercase identifier of [a-z0-9-_].
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// URL returns the share address without any credentials.
func (s *Share) URL() string {
	return "smb://" + s.hostPort() + escapePath(s.Path)
}

// MountURL returns the address including domain and user, as handed to the
// system for mounting. It never contains a password.
func (s *Share) MountURL() string {
	auth := ""
	if s.Username != "" {
		auth = url.PathEscape(s.Username) + "@"
		if s.Domain != "" {
			auth = url.PathEscape(s.Domain) + ";" + auth
		}
	}
	return "smb://" + auth + s.hostPort() + escapePath(s.Path)
}

// ShareName is the first path segment, i.e. the SMB share itself.
func (s *Share) ShareName() string {
	return strings.SplitN(strings.TrimPrefix(s.Path, "/"), "/", 2)[0]
}

// MountPath returns the absolute mount point with ~ expanded.
func (s *Share) MountPath() (string, error) {
	return ExpandPath(s.MountPoint)
}

// Guest reports whether the share is mounted without a user account.
func (s *Share) Guest() bool { return s.Username == "" }

func (s *Share) hostPort() string {
	h := s.Host
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	if s.Port != 0 {
		h += ":" + strconv.Itoa(s.Port)
	}
	return h
}

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/")
}

// ExpandPath resolves a leading ~ and returns a clean absolute path.
func ExpandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	return filepath.Abs(p)
}

// CollapsePath replaces the home directory prefix with ~ for display and storage.
func CollapsePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
