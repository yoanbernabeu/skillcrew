package scheduler

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// LaunchdLabel identifies the LaunchAgent.
const LaunchdLabel = "co.yoandev.skillcrew.sync"

type launchd struct {
	home    string
	uid     int
	run     Runner
	envPath string // PATH given to the job, captured at construction
}

// NewLaunchd returns the macOS backend: a LaunchAgent in
// ~/Library/LaunchAgents loaded into the gui/<uid> domain.
func NewLaunchd(home string, uid int, run Runner) Scheduler {
	return &launchd{home: home, uid: uid, run: run, envPath: os.Getenv("PATH")}
}

func (l *launchd) plist() string {
	return filepath.Join(l.home, "Library", "LaunchAgents", LaunchdLabel+".plist")
}

func (l *launchd) domain() string { return "gui/" + strconv.Itoa(l.uid) }

func (l *launchd) Install(bin string, interval time.Duration) error {
	secs, err := seconds(interval)
	if err != nil {
		return err
	}
	if err := writeFile(l.plist(), plist(bin, secs, l.envPath)); err != nil {
		return fmt.Errorf("writing LaunchAgent: %w", err)
	}
	_ = l.run("launchctl", "bootout", l.domain()+"/"+LaunchdLabel) // may not be loaded
	err = l.run("launchctl", "bootstrap", l.domain(), l.plist())
	if err != nil { // bootstrap right after bootout fails transiently: retry once
		time.Sleep(retryDelay)
		err = l.run("launchctl", "bootstrap", l.domain(), l.plist())
	}
	if err != nil {
		return errors.Join(fmt.Errorf("%w: loading LaunchAgent: %w", ErrUnavailable, err), removeFile(l.plist()))
	}
	return nil
}

// retryDelay lets launchd finish unloading the previous job.
var retryDelay = 500 * time.Millisecond

func (l *launchd) Uninstall() error {
	_ = l.run("launchctl", "bootout", l.domain()+"/"+LaunchdLabel) // may not be loaded
	return removeFile(l.plist())
}

func (l *launchd) Installed() bool { return exists(l.plist()) }

// plist renders the LaunchAgent; a non-empty path is set as the job's PATH,
// since launchd only provides a minimal one.
func plist(bin string, secs int64, path string) string {
	env := ""
	if path != "" {
		env = fmt.Sprintf(`
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>`, xmlEscape(path))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>sync</string>
		<string>--quiet</string>
	</array>%s
	<key>StartInterval</key>
	<integer>%d</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`, LaunchdLabel, xmlEscape(bin), env, secs)
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s)) // never fails on a bytes.Buffer
	return b.String()
}
