// Package history keeps a local log of connection events: what was connected
// or disconnected, when, by what (the user or the background agent), and why
// something failed. It never records credentials.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/logtrim"
)

// Actions recorded in the log.
const (
	Connected    = "connected"
	Disconnected = "disconnected"
	Failed       = "failed"
)

// Triggers: who caused the event.
const (
	Manual      = "manual"      // a command typed by the user
	Interactive = "interactive" // the interactive interface
	Auto        = "auto"        // the background agent
)

// Event is one line of the log.
type Event struct {
	Time    time.Time `json:"time"`
	Share   string    `json:"share"`
	Action  string    `json:"action"`
	Trigger string    `json:"trigger"`
	Detail  string    `json:"detail,omitempty"` // mount path, or the error
}

// maxSize is the size at which the log is cut down to its newer half.
const maxSize = 256 << 10

// Path returns the log file, which lives next to the configuration.
func Path() (string, error) {
	cfg, err := config.DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cfg), "history.jsonl"), nil
}

// Append adds an event. Failures are returned but callers may ignore them:
// a missing log entry must never break connecting.
func Append(e Event) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Time = e.Time.Round(time.Second)
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	logtrim.Trim(path, maxSize)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Read returns the events of one share (or of all shares when share is ""),
// oldest first, limited to the newest limit entries (0 = no limit).
func Read(share string, limit int) ([]Event, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Share == "" {
			continue // skip damaged lines instead of failing
		}
		if share == "" || e.Share == share {
			events = append(events, e)
		}
	}
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, sc.Err()
}

// Last returns the newest event of a share with the given action, if any.
func Last(share, action string) (Event, bool) {
	events, _ := Read(share, 0)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Action == action {
			return events[i], true
		}
	}
	return Event{}, false
}

// Clear deletes the log.
func Clear() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
