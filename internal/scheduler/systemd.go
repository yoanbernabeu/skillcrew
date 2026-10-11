package scheduler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Systemd unit names.
const (
	SystemdService = "skillcrew-sync.service"
	SystemdTimer   = "skillcrew-sync.timer"
)

type systemd struct {
	home    string
	run     Runner
	envPath string // PATH given to the job, captured at construction
}

// NewSystemd returns the Linux backend: a oneshot service and a timer in
// ~/.config/systemd/user, managed with systemctl --user.
func NewSystemd(home string, run Runner) Scheduler {
	return &systemd{home: home, run: run, envPath: os.Getenv("PATH")}
}

func (s *systemd) path(unit string) string {
	return filepath.Join(s.home, ".config", "systemd", "user", unit)
}

func (s *systemd) Install(bin string, interval time.Duration) error {
	secs, err := seconds(interval)
	if err != nil {
		return err
	}
	env := ""
	if s.envPath != "" { // systemd --user only provides a minimal PATH
		env = "Environment=" + cQuote("PATH="+strings.ReplaceAll(s.envPath, "%", "%%")) + "\n"
	}
	service := fmt.Sprintf(`[Unit]
Description=Skillcrew periodic sync

[Service]
Type=oneshot
%sExecStart=%s sync --quiet
`, env, systemdQuote(bin))
	timer := fmt.Sprintf(`[Unit]
Description=Skillcrew periodic sync timer

[Timer]
OnStartupSec=2min
OnUnitActiveSec=%ds
Unit=%s

[Install]
WantedBy=timers.target
`, secs, SystemdService)

	if err := writeFile(s.path(SystemdService), service); err != nil {
		return fmt.Errorf("writing systemd service: %w", err)
	}
	if err := writeFile(s.path(SystemdTimer), timer); err != nil {
		return errors.Join(fmt.Errorf("writing systemd timer: %w", err), s.remove())
	}
	err = s.run("systemctl", "--user", "daemon-reload")
	if err == nil {
		err = s.run("systemctl", "--user", "enable", "--now", SystemdTimer)
	}
	if err != nil {
		return errors.Join(fmt.Errorf("%w: %w", ErrUnavailable, err), s.remove())
	}
	return nil
}

func (s *systemd) Uninstall() error {
	_ = s.run("systemctl", "--user", "disable", "--now", SystemdTimer) // may not be enabled
	if err := s.remove(); err != nil {
		return err
	}
	_ = s.run("systemctl", "--user", "daemon-reload")
	return nil
}

func (s *systemd) Installed() bool { return exists(s.path(SystemdTimer)) }

func (s *systemd) remove() error {
	return errors.Join(removeFile(s.path(SystemdTimer)), removeFile(s.path(SystemdService)))
}

// systemdQuote escapes a word for an ExecStart= line: specifiers (%) and
// variable expansion ($) are doubled, and the word is double-quoted with C
// escapes when it contains whitespace, quotes, backslashes or control chars.
func systemdQuote(s string) string {
	s = strings.NewReplacer("%", "%%", "$", "$$").Replace(s)
	if !strings.ContainsFunc(s, func(r rune) bool {
		return r <= ' ' || r == '"' || r == '\'' || r == '\\' || r == ';' || r == 0x7f
	}) {
		return s
	}
	return cQuote(s)
}

// cQuote double-quotes s with C escapes for quotes, backslashes, newlines and
// tabs, as systemd unit settings accept. Specifiers are not escaped.
func cQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
