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

// Config is the on-disk configuration. It never contains credentials.
type Config struct {
	Version int            `yaml:"version"`
	Shares  []*share.Share `yaml:"shares"`

	path string
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
	return nil, fmt.Errorf("no share named %q (known: %s)", key, strings.Join(c.IDs(), ", "))
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
	for _, o := range c.Shares {
		if o.ID == s.ID {
			return fmt.Errorf("a share with id %q already exists", s.ID)
		}
		if omp, err := o.MountPath(); err == nil && omp == mp {
			return fmt.Errorf("mount point %s is already used by share %q", s.MountPoint, o.ID)
		}
	}
	c.Shares = append(c.Shares, s)
	return nil
}

// Remove deletes the share with the given id.
func (c *Config) Remove(id string) bool {
	for i, s := range c.Shares {
		if s.ID == id {
			c.Shares = append(c.Shares[:i], c.Shares[i+1:]...)
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
