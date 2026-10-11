package scheduler

import (
	"errors"
	"testing"
)

func TestNewFor(t *testing.T) {
	run := func(string, ...string) error { return nil }

	s, err := newFor("darwin", t.TempDir(), 501, run)
	if l, ok := s.(*launchd); err != nil || !ok || l.uid != 501 {
		t.Errorf("darwin: got %T, %v", s, err)
	}
	s, err = newFor("linux", t.TempDir(), 501, run)
	if _, ok := s.(*systemd); err != nil || !ok {
		t.Errorf("linux: got %T, %v", s, err)
	}
	for _, goos := range []string{"windows", "freebsd"} {
		if _, err := newFor(goos, t.TempDir(), 0, run); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", goos, err)
		}
	}
}
