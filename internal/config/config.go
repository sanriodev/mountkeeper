// Package config loads and stores the Mountkeeper configuration file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sanriodev/mountkeeper/internal/share"
)

// CurrentVersion is the schema version written to new configuration files.
const CurrentVersion = 1

// EnvPath overrides the configuration file location (used by tests).
const EnvPath = "MOUNTKEEPER_CONFIG"

// Profile is a named group of shares that are connected or disconnected
// together, e.g. "office" or "home".
type Profile struct {
	Name   string   `yaml:"name"`
	Shares []string `yaml:"shares"` // share ids
	// Auto lists network rules ("domain:corp.example.com", "router:<mac>",
	// "ssid:<name>"). While one of them matches the current network, the
	// background agent keeps the profile's shares connected; when none
	// matches any more, it disconnects the ones it connected.
	Auto []string `yaml:"auto,omitempty"`
}

// Config is the on-disk configuration. It never contains credentials.
type Config struct {
	Version  int            `yaml:"version"`
	Shares   []*share.Share `yaml:"shares"`
	Profiles []*Profile     `yaml:"profiles,omitempty"`
	// Notifications: false stops the background agent from showing macOS
	// notifications (for example when a share is paused). Default: on.
	Notifications *bool `yaml:"notifications,omitempty"`

	path string
}

// NotificationsEnabled reports whether the agent may show notifications.
func (c *Config) NotificationsEnabled() bool {
	return c.Notifications == nil || *c.Notifications
}

// DefaultPath returns ~/.config/mountkeeper/config.yaml.
func DefaultPath() (string, error) {
	if p := os.Getenv(EnvPath); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "mountkeeper", "config.yaml"), nil
}

// Load reads the configuration; a missing file yields an empty configuration.
func Load() (*Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	c := &Config{Version: CurrentVersion, path: path}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Version > CurrentVersion {
		return nil, fmt.Errorf("%s: config version %d is newer than this Mountkeeper supports (%d)", path, c.Version, CurrentVersion)
	}
	if c.Version == 0 {
		c.Version = CurrentVersion
	}

	seen := map[string]bool{}
	for i, s := range c.Shares {
		if s == nil || s.ID == "" || s.Host == "" || s.Path == "" {
			return nil, fmt.Errorf("%s: share #%d needs id, host and path", path, i+1)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("%s: duplicate share id %q", path, s.ID)
		}
		seen[s.ID] = true
		if s.Protocol == "" {
			s.Protocol = "smb"
		}
		if s.Name == "" {
			s.Name = s.ID
		}
		if s.MountPoint == "" {
			s.MountPoint = share.DefaultMountRoot + "/" + s.ID
		}
	}

	names := map[string]bool{}
	for i, p := range c.Profiles {
		if p == nil || p.Name == "" {
			return nil, fmt.Errorf("%s: profile #%d has no name", path, i+1)
		}
		if names[strings.ToLower(p.Name)] {
			return nil, fmt.Errorf("%s: duplicate profile %q", path, p.Name)
		}
		names[strings.ToLower(p.Name)] = true
		for _, id := range p.Shares {
			if !seen[id] {
				return nil, fmt.Errorf("%s: profile %q refers to unknown share %q", path, p.Name, id)
			}
		}
	}
	return c, nil
}

// Path returns the file the configuration was loaded from.
func (c *Config) Path() string { return c.path }

// Save writes the configuration atomically with user-only permissions.
func (c *Config) Save() error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.path)
}

// Find looks a share up by id or (case-insensitively) by name.
func (c *Config) Find(key string) *share.Share {
	key = strings.TrimSpace(key)
	for _, s := range c.Shares {
		if s.ID == key {
			return s
		}
	}
	for _, s := range c.Shares {
		if strings.EqualFold(s.ID, key) || strings.EqualFold(s.Name, key) {
			return s
		}
	}
	return nil
}

// Get is Find with a helpful error for unknown shares.
func (c *Config) Get(key string) (*share.Share, error) {
	if s := c.Find(key); s != nil {
		return s, nil
	}
	if len(c.Shares) == 0 {
		return nil, fmt.Errorf("no share named %q — nothing is registered yet (see: mountkeeper add)", key)
	}
	if guess := closest(key, c.IDs()); guess != "" {
		return nil, fmt.Errorf("no share named %q — did you mean %q?", key, guess)
	}
	return nil, fmt.Errorf("no share named %q (known: %s)", key, strings.Join(c.IDs(), ", "))
}

// closest returns the candidate most similar to key, or "" if none is close
// enough to be a plausible typo.
func closest(key string, candidates []string) string {
	key = strings.ToLower(key)
	best, bestDist := "", 0
	for _, c := range candidates {
		lc := strings.ToLower(c)
		if key != "" && (strings.HasPrefix(lc, key) || strings.HasPrefix(key, lc)) {
			return c
		}
		if d := editDistance(key, lc); best == "" || d < bestDist {
			best, bestDist = c, d
		}
	}
	// Allow roughly one typo per three characters (a swapped pair counts as two).
	if best != "" && bestDist <= max(1, (len(key)+1)/3) {
		return best
	}
	return ""
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// IDs lists all share ids in configuration order.
func (c *Config) IDs() []string {
	ids := make([]string, len(c.Shares))
	for i, s := range c.Shares {
		ids[i] = s.ID
	}
	return ids
}

// Add appends a share, rejecting duplicate ids and mount points.
func (c *Config) Add(s *share.Share) error {
	mp, err := s.MountPath()
	if err != nil {
		return err
	}
	if c.Find(s.ID) != nil {
		return fmt.Errorf("a share with id %q already exists", s.ID)
	}
	if o := c.MountPointOwner(mp, s); o != nil {
		return fmt.Errorf("mount point %s is already used by share %q", s.MountPoint, o.ID)
	}
	c.Shares = append(c.Shares, s)
	return nil
}

// Remove deletes the share with the given id, also from all profiles.
func (c *Config) Remove(id string) bool {
	for i, s := range c.Shares {
		if s.ID == id {
			c.Shares = append(c.Shares[:i], c.Shares[i+1:]...)
			for _, p := range c.Profiles {
				p.remove(id)
			}
			return true
		}
	}
	return false
}

// MountPointOwner returns the share other than except that uses the given
// absolute mount point, or nil.
func (c *Config) MountPointOwner(abs string, except *share.Share) *share.Share {
	for _, o := range c.Shares {
		if o == except {
			continue
		}
		if omp, err := o.MountPath(); err == nil && omp == abs {
			return o
		}
	}
	return nil
}

// FindProfile looks a profile up by name, ignoring case.
func (c *Config) FindProfile(name string) *Profile {
	for _, p := range c.Profiles {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			return p
		}
	}
	return nil
}

// GetProfile is FindProfile with a helpful error for unknown profiles.
func (c *Config) GetProfile(name string) (*Profile, error) {
	if p := c.FindProfile(name); p != nil {
		return p, nil
	}
	if len(c.Profiles) == 0 {
		return nil, fmt.Errorf("no profile named %q — none exist yet (see: mountkeeper profile add)", name)
	}
	names := c.ProfileNames()
	if guess := closest(name, names); guess != "" {
		return nil, fmt.Errorf("no profile named %q — did you mean %q?", name, guess)
	}
	return nil, fmt.Errorf("no profile named %q (known: %s)", name, strings.Join(names, ", "))
}

// ProfileNames lists all profile names in configuration order.
func (c *Config) ProfileNames() []string {
	names := make([]string, len(c.Profiles))
	for i, p := range c.Profiles {
		names[i] = p.Name
	}
	return names
}

// ProfileShares resolves the shares of a profile, in profile order.
func (c *Config) ProfileShares(p *Profile) []*share.Share {
	var out []*share.Share
	for _, id := range p.Shares {
		if s := c.Find(id); s != nil {
			out = append(out, s)
		}
	}
	return out
}

// AddToProfile adds shares to a profile, creating the profile if necessary.
// It returns the profile and whether it was newly created.
func (c *Config) AddToProfile(name string, ids ...string) (*Profile, bool) {
	p := c.FindProfile(name)
	created := p == nil
	if created {
		p = &Profile{Name: strings.TrimSpace(name)}
		c.Profiles = append(c.Profiles, p)
	}
	for _, id := range ids {
		if !p.Has(id) {
			p.Shares = append(p.Shares, id)
		}
	}
	return p, created
}

// RemoveProfile deletes a profile; the shares themselves are untouched.
func (c *Config) RemoveProfile(name string) bool {
	for i, p := range c.Profiles {
		if strings.EqualFold(p.Name, name) {
			c.Profiles = append(c.Profiles[:i], c.Profiles[i+1:]...)
			return true
		}
	}
	return false
}

// Has reports whether the profile contains the share id.
func (p *Profile) Has(id string) bool {
	for _, s := range p.Shares {
		if s == id {
			return true
		}
	}
	return false
}

// Remove takes share ids out of the profile.
func (p *Profile) Remove(ids ...string) {
	for _, id := range ids {
		p.remove(id)
	}
}

func (p *Profile) remove(id string) {
	kept := p.Shares[:0]
	for _, s := range p.Shares {
		if s != id {
			kept = append(kept, s)
		}
	}
	p.Shares = kept
}

// NeedsAgent reports whether the background agent has anything to do: a
// share with auto-connect, or a profile bound to a network.
func (c *Config) NeedsAgent() bool {
	if c.AnyAutoConnect() {
		return true
	}
	for _, p := range c.Profiles {
		if len(p.Auto) > 0 && len(p.Shares) > 0 {
			return true
		}
	}
	return false
}

// AnyAutoConnect reports whether at least one share wants automatic mounting.
func (c *Config) AnyAutoConnect() bool {
	for _, s := range c.Shares {
		if s.AutoConnect {
			return true
		}
	}
	return false
}
