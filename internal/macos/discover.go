package macos

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Server is an SMB server announced on the local network via Bonjour.
type Server struct {
	Name string // Bonjour instance name, e.g. "Office NAS"
	Host string // resolved host name, e.g. "office-nas.local"; may be empty
}

var (
	browseLine  = regexp.MustCompile(`^\S+\s+Add\s+\d+\s+\d+\s+(\S+)\s+_smb\._tcp\.\s+(.+)$`)
	resolveLine = regexp.MustCompile(`can be reached at\s+(\S+?)\.?:\d+`)
)

// Browse lists SMB servers that announce themselves via Bonjour. dns-sd
// never exits on its own, so it is stopped after the given duration.
func Browse(wait time.Duration) ([]Server, error) {
	out, err := runFor(wait, "/usr/bin/dns-sd", "-B", "_smb._tcp", "local.")
	if err != nil {
		return nil, err
	}
	names := parseBrowse(out)

	servers := make([]Server, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		servers[i].Name = name
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out, err := runFor(1500*time.Millisecond, "/usr/bin/dns-sd", "-L", name, "_smb._tcp", "local."); err == nil {
				if m := resolveLine.FindSubmatch(out); m != nil {
					servers[i].Host = string(m[1])
				}
			}
		}()
	}
	wg.Wait()
	return servers, nil
}

func parseBrowse(out []byte) []string {
	seen := map[string]bool{}
	var names []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if m := browseLine.FindStringSubmatch(strings.TrimRight(sc.Text(), " \r")); m != nil && !seen[m[2]] {
			seen[m[2]] = true
			names = append(names, m[2])
		}
	}
	sort.Strings(names)
	return names
}

// runFor runs a long-lived command for a fixed time and returns what it
// printed until then.
func runFor(d time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	cmd.Wait() // ends through the timeout; the exit status carries no meaning
	return buf.Bytes(), nil
}

// ViewShares lists the shares a server offers using smbutil, attached to the
// terminal so that smbutil itself can ask for a password if it needs one —
// Mountkeeper never puts one on its command line. target is
// "[domain;][user@]host".
func ViewShares(target string, guest bool) error {
	args := []string{"view"}
	if guest {
		args = append(args, "-G")
	}
	args = append(args, "//"+target)
	cmd := exec.Command("/usr/bin/smbutil", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
