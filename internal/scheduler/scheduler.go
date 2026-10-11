// Package scheduler registers the fallback periodic sync with the OS
// user-level scheduler: launchd on macOS, systemd user units on Linux.
// No admin rights, no resident daemon.
package scheduler

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	// ErrUnsupported is returned by New on an OS without a supported scheduler.
	ErrUnsupported = errors.New("no user-level scheduler supported on this OS")
	// ErrUnavailable wraps an Install failure caused by the scheduler itself
	// not running (e.g. WSL2 without systemd); callers can warn and continue.
	ErrUnavailable = errors.New("user-level scheduler unavailable")
)

// Scheduler registers the periodic sync with the OS user-level scheduler.
type Scheduler interface {
	// Install writes the scheduler files and (re)loads them. It is
	// idempotent: re-installing rewrites the files and reloads.
	Install(bin string, interval time.Duration) error
	// Uninstall unloads and removes the files; it succeeds if not installed.
	Uninstall() error
	// Installed reports whether the unit or plist file is present.
	Installed() bool
}

// Runner executes an external command (injected for tests).
type Runner func(name string, args ...string) error

// ExecRunner runs the command with os/exec, including its combined output in
// the error on failure.
func ExecRunner(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		cmd := strings.Join(append([]string{name}, args...), " ")
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s: %w: %s", cmd, err, msg)
		}
		return fmt.Errorf("%s: %w", cmd, err)
	}
	return nil
}

// New returns the scheduler for the current OS: launchd on darwin, systemd
// user units on linux. Other OSes return ErrUnsupported.
func New(home string, run Runner) (Scheduler, error) {
	return newFor(runtime.GOOS, home, os.Getuid(), run)
}

func newFor(goos, home string, uid int, run Runner) (Scheduler, error) {
	switch goos {
	case "darwin":
		return NewLaunchd(home, uid, run), nil
	case "linux":
		return NewSystemd(home, run), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, goos)
}

// seconds validates the interval and returns it in whole seconds.
func seconds(interval time.Duration) (int64, error) {
	s := int64(interval / time.Second)
	if s < 1 {
		return 0, fmt.Errorf("sync interval %v: must be at least 1s", interval)
	}
	return s, nil
}

// writeFile creates the parent directory and writes data with mode 0644.
func writeFile(path, data string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o644)
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
