// Package logging writes the desktop app log to a size-rotated file
// (%LOCALAPPDATA%\VideoCompress\logs\app.log, 5MB per file, 3 files kept).
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// DefaultMaxBytes is the size at which app.log rotates (spec §9).
	DefaultMaxBytes = 5 << 20
	// DefaultKeep is how many log files are kept in total, newest first.
	DefaultKeep = 3
	// FileName is the current log file's name inside the log directory.
	FileName = "app.log"
)

// Logger appends timestamped lines to <dir>/app.log and rotates the file once it
// grows past maxBytes. A nil *Logger is a no-op, so callers need no nil checks.
type Logger struct {
	mu       sync.Mutex
	dir      string
	maxBytes int64
	keep     int
	file     *os.File
	size     int64
	now      func() time.Time
}

// Open creates dir if needed and opens app.log for appending.
func Open(dir string) (*Logger, error) {
	return OpenWith(dir, DefaultMaxBytes, DefaultKeep)
}

// OpenWith opens app.log with explicit rotation limits (used by tests).
func OpenWith(dir string, maxBytes int64, keep int) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Logger{dir: dir, maxBytes: maxBytes, keep: keep, now: time.Now}
	if err := l.openLocked(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Logger) path() string { return filepath.Join(l.dir, FileName) }

func (l *Logger) openLocked() error {
	f, err := os.OpenFile(l.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	l.file = f
	if st, statErr := f.Stat(); statErr == nil {
		l.size = st.Size()
	} else {
		l.size = 0
	}
	return nil
}

// Printf writes one timestamped line. Write failures are swallowed: logging must
// never take the app down.
func (l *Logger) Printf(format string, args ...any) {
	if l == nil {
		return
	}
	line := l.now().Format("2006-01-02T15:04:05.000") + " " + fmt.Sprintf(format, args...) + "\n"
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	if l.maxBytes > 0 && l.size > 0 && l.size+int64(len(line)) > l.maxBytes {
		l.rotateLocked()
	}
	n, err := l.file.WriteString(line)
	if err != nil {
		return
	}
	l.size += int64(n)
}

// rotateLocked shifts app.log to app.log.1 (and .1 to .2, …), dropping the
// oldest file so at most keep files remain.
func (l *Logger) rotateLocked() {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	if l.keep <= 1 {
		_ = os.Remove(l.path())
		_ = l.openLocked()
		return
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", l.path(), l.keep-1))
	for i := l.keep - 2; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", l.path(), i), fmt.Sprintf("%s.%d", l.path(), i+1))
	}
	_ = os.Rename(l.path(), l.path()+".1")
	_ = l.openLocked()
}

// Close releases the file handle.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
