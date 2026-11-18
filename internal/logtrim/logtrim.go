// Package logtrim keeps append-only log files from growing without bound.
package logtrim

import (
	"bytes"
	"os"
)

// Trim cuts the file at path down to its newer half, starting at a line
// boundary, once it is larger than max bytes. Smaller or missing files are
// left alone.
//
// The file is rewritten in place (truncated and written again) rather than
// replaced by a renamed copy: launchd hands the background agent its stdout
// and stderr as descriptors opened with O_APPEND on this very file, and a
// rename would leave the running agent writing into a deleted inode.
func Trim(path string, max int64) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= max {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	half := data[len(data)/2:]
	if i := bytes.IndexByte(half, '\n'); i >= 0 {
		half = half[i+1:]
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(half)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
