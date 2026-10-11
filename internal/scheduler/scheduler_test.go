package scheduler_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/scheduler"
)

// fake records every command and fails those whose joined form starts with
// one of the fail prefixes.
type fake struct {
	calls []string
	fail  []string
}

func (f *fake) run(name string, args ...string) error {
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, cmd)
	for _, p := range f.fail {
		if strings.HasPrefix(cmd, p) {
			return errors.New("boom")
		}
	}
	return nil
}

const testPath = "/opt/homebrew/bin:/usr/bin:/bin"

func newLaunchd(home string, run scheduler.Runner) scheduler.Scheduler {
	s := scheduler.NewLaunchd(home, 501, run)
	scheduler.SetPath(s, testPath)
	return s
}

func newSystemd(home string, run scheduler.Runner) scheduler.Scheduler {
	s := scheduler.NewSystemd(home, run)
	scheduler.SetPath(s, testPath)
	return s
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

const wantPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>co.yoandev.skillcrew.sync</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/local/bin/skillcrew</string>
		<string>sync</string>
		<string>--quiet</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/bin:/bin</string>
	</dict>
	<key>StartInterval</key>
	<integer>3600</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`

func TestLaunchdInstall(t *testing.T) {
	home := t.TempDir()
	f := &fake{fail: []string{"launchctl bootout"}} // not loaded yet: must be ignored
	s := newLaunchd(home, f.run)

	if s.Installed() {
		t.Fatal("Installed before Install")
	}
	if err := s.Install("/usr/local/bin/skillcrew", time.Hour); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", "co.yoandev.skillcrew.sync.plist")
	if got := read(t, plist); got != wantPlist {
		t.Errorf("plist:\n%s\nwant:\n%s", got, wantPlist)
	}
	info, err := os.Stat(plist)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	want := []string{
		"launchctl bootout gui/501/co.yoandev.skillcrew.sync",
		"launchctl bootstrap gui/501 " + plist,
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
	if !s.Installed() {
		t.Error("Installed after Install = false")
	}
}

func TestLaunchdReinstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	f := &fake{}
	s := newLaunchd(home, f.run)
	if err := s.Install("/old/skillcrew", time.Minute); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := s.Install("/usr/local/bin/skillcrew", time.Hour); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", "co.yoandev.skillcrew.sync.plist")
	if got := read(t, plist); got != wantPlist {
		t.Errorf("plist not rewritten:\n%s", got)
	}
	if len(f.calls) != 2 || !strings.HasPrefix(f.calls[0], "launchctl bootout") || !strings.HasPrefix(f.calls[1], "launchctl bootstrap") {
		t.Errorf("calls = %q", f.calls)
	}
}

func TestLaunchdBootstrapFailure(t *testing.T) {
	home := t.TempDir()
	f := &fake{fail: []string{"launchctl bootstrap"}}
	s := scheduler.NewLaunchd(home, 501, f.run)
	err := s.Install("/bin/skillcrew", time.Hour)
	if !errors.Is(err, scheduler.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if exists(filepath.Join(home, "Library", "LaunchAgents", "co.yoandev.skillcrew.sync.plist")) {
		t.Error("plist left behind")
	}
	if s.Installed() {
		t.Error("Installed after failure")
	}
}

func TestLaunchdEscapesPath(t *testing.T) {
	home := t.TempDir()
	s := scheduler.NewLaunchd(home, 501, (&fake{}).run)
	scheduler.SetPath(s, `/a b/<x>&"y'/bin:/usr/bin`)
	if err := s.Install("/bin/skillcrew", time.Hour); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(home, "Library", "LaunchAgents", "co.yoandev.skillcrew.sync.plist"))
	want := "<key>PATH</key>\n\t\t<string>/a b/&lt;x&gt;&amp;&#34;y&#39;/bin:/usr/bin</string>"
	if !strings.Contains(got, want) {
		t.Errorf("plist does not contain the escaped PATH:\n%s", got)
	}
}

func TestLaunchdEscapesBin(t *testing.T) {
	home := t.TempDir()
	s := scheduler.NewLaunchd(home, 501, (&fake{}).run)
	if err := s.Install(`/Users/a b/<x>&"y'/skillcrew`, time.Hour); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(home, "Library", "LaunchAgents", "co.yoandev.skillcrew.sync.plist"))
	want := "<string>/Users/a b/&lt;x&gt;&amp;&#34;y&#39;/skillcrew</string>"
	if !strings.Contains(got, want) {
		t.Errorf("plist does not contain %s:\n%s", want, got)
	}
}

func TestLaunchdUninstall(t *testing.T) {
	home := t.TempDir()
	f := &fake{}
	s := newLaunchd(home, f.run)
	if err := s.Install("/bin/skillcrew", time.Hour); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	f.fail = []string{"launchctl bootout"}
	if err := s.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if s.Installed() {
		t.Error("still installed")
	}
	want := []string{"launchctl bootout gui/501/co.yoandev.skillcrew.sync"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
}

func TestLaunchdUninstallWhenNotInstalled(t *testing.T) {
	f := &fake{fail: []string{"launchctl"}}
	s := scheduler.NewLaunchd(t.TempDir(), 501, f.run)
	if err := s.Uninstall(); err != nil {
		t.Fatal(err)
	}
}

const wantService = `[Unit]
Description=Skillcrew periodic sync

[Service]
Type=oneshot
Environment="PATH=/opt/homebrew/bin:/usr/bin:/bin"
ExecStart=/usr/local/bin/skillcrew sync --quiet
`

const wantTimer = `[Unit]
Description=Skillcrew periodic sync timer

[Timer]
OnStartupSec=2min
OnUnitActiveSec=3600s
Unit=skillcrew-sync.service

[Install]
WantedBy=timers.target
`

func unitDir(home string) string { return filepath.Join(home, ".config", "systemd", "user") }

func TestSystemdInstall(t *testing.T) {
	home := t.TempDir()
	f := &fake{}
	s := newSystemd(home, f.run)
	if s.Installed() {
		t.Fatal("Installed before Install")
	}
	if err := s.Install("/usr/local/bin/skillcrew", time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(unitDir(home), "skillcrew-sync.service")); got != wantService {
		t.Errorf("service:\n%s\nwant:\n%s", got, wantService)
	}
	if got := read(t, filepath.Join(unitDir(home), "skillcrew-sync.timer")); got != wantTimer {
		t.Errorf("timer:\n%s\nwant:\n%s", got, wantTimer)
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable --now skillcrew-sync.timer",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
	if !s.Installed() {
		t.Error("Installed after Install = false")
	}
}

func TestSystemdReinstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	f := &fake{}
	s := newSystemd(home, f.run)
	if err := s.Install("/old/skillcrew", time.Minute); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := s.Install("/usr/local/bin/skillcrew", time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(unitDir(home), "skillcrew-sync.service")); got != wantService {
		t.Errorf("service not rewritten:\n%s", got)
	}
	if got := read(t, filepath.Join(unitDir(home), "skillcrew-sync.timer")); got != wantTimer {
		t.Errorf("timer not rewritten:\n%s", got)
	}
	if len(f.calls) != 2 {
		t.Errorf("calls = %q", f.calls)
	}
}

func TestSystemdUnavailable(t *testing.T) {
	for _, failing := range []string{"systemctl --user daemon-reload", "systemctl --user enable"} {
		t.Run(failing, func(t *testing.T) {
			home := t.TempDir()
			s := scheduler.NewSystemd(home, (&fake{fail: []string{failing}}).run)
			err := s.Install("/bin/skillcrew", time.Hour)
			if !errors.Is(err, scheduler.ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
			for _, name := range []string{"skillcrew-sync.service", "skillcrew-sync.timer"} {
				if exists(filepath.Join(unitDir(home), name)) {
					t.Errorf("%s left behind", name)
				}
			}
			if s.Installed() {
				t.Error("Installed after failure")
			}
		})
	}
}

func TestSystemdUninstall(t *testing.T) {
	home := t.TempDir()
	f := &fake{}
	s := newSystemd(home, f.run)
	if err := s.Install("/bin/skillcrew", time.Hour); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	f.fail = []string{"systemctl"}
	if err := s.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if s.Installed() {
		t.Error("still installed")
	}
	want := []string{
		"systemctl --user disable --now skillcrew-sync.timer",
		"systemctl --user daemon-reload",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %q, want %q", f.calls, want)
	}
}

func TestSystemdUninstallWhenNotInstalled(t *testing.T) {
	s := scheduler.NewSystemd(t.TempDir(), (&fake{fail: []string{"systemctl"}}).run)
	if err := s.Uninstall(); err != nil {
		t.Fatal(err)
	}
}

func TestSystemdEscapesBin(t *testing.T) {
	cases := []struct{ bin, want string }{
		{"/usr/bin/skillcrew", "ExecStart=/usr/bin/skillcrew sync --quiet\n"},
		{"/home/a b/skillcrew", `ExecStart="/home/a b/skillcrew" sync --quiet` + "\n"},
		{`/home/a"b\c/skillcrew`, `ExecStart="/home/a\"b\\c/skillcrew" sync --quiet` + "\n"},
		{"/home/100%/$HOME/skillcrew", "ExecStart=/home/100%%/$$HOME/skillcrew sync --quiet\n"},
		{"/home/it's/skillcrew", `ExecStart="/home/it's/skillcrew" sync --quiet` + "\n"},
	}
	for _, c := range cases {
		t.Run(c.bin, func(t *testing.T) {
			home := t.TempDir()
			s := scheduler.NewSystemd(home, (&fake{}).run)
			if err := s.Install(c.bin, time.Hour); err != nil {
				t.Fatal(err)
			}
			got := read(t, filepath.Join(unitDir(home), "skillcrew-sync.service"))
			if !strings.Contains(got, c.want) {
				t.Errorf("service does not contain %q:\n%s", c.want, got)
			}
		})
	}
}

func TestSystemdEscapesPath(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/usr/bin:/bin", `Environment="PATH=/usr/bin:/bin"` + "\n"},
		{"/home/a b/bin:/bin", `Environment="PATH=/home/a b/bin:/bin"` + "\n"},
		{`/home/a"b\c/bin`, `Environment="PATH=/home/a\"b\\c/bin"` + "\n"},
		{"/home/100%/bin", `Environment="PATH=/home/100%%/bin"` + "\n"},
		// "$" has no special meaning in Environment= (no variable expansion).
		{"/home/$x/bin", `Environment="PATH=/home/$x/bin"` + "\n"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			home := t.TempDir()
			s := scheduler.NewSystemd(home, (&fake{}).run)
			scheduler.SetPath(s, c.path)
			if err := s.Install("/bin/skillcrew", time.Hour); err != nil {
				t.Fatal(err)
			}
			got := read(t, filepath.Join(unitDir(home), "skillcrew-sync.service"))
			if !strings.Contains(got, c.want) {
				t.Errorf("service does not contain %q:\n%s", c.want, got)
			}
		})
	}
}

func TestPathCapturedAtConstruction(t *testing.T) {
	t.Setenv("PATH", "/captured/bin:/usr/bin")
	home := t.TempDir()
	launchd := scheduler.NewLaunchd(home, 501, (&fake{}).run)
	systemd := scheduler.NewSystemd(home, (&fake{}).run)
	t.Setenv("PATH", "/later/bin")

	for _, s := range []scheduler.Scheduler{launchd, systemd} {
		if err := s.Install("/bin/skillcrew", time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(t, filepath.Join(home, "Library", "LaunchAgents", "co.yoandev.skillcrew.sync.plist")); !strings.Contains(got, "<string>/captured/bin:/usr/bin</string>") {
		t.Errorf("plist lacks the captured PATH:\n%s", got)
	}
	if got := read(t, filepath.Join(unitDir(home), "skillcrew-sync.service")); !strings.Contains(got, `Environment="PATH=/captured/bin:/usr/bin"`) {
		t.Errorf("service lacks the captured PATH:\n%s", got)
	}
}

func TestEmptyPathOmitted(t *testing.T) {
	home := t.TempDir()
	l := scheduler.NewLaunchd(home, 501, (&fake{}).run)
	scheduler.SetPath(l, "")
	if err := l.Install("/bin/skillcrew", time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(home, "Library", "LaunchAgents", "co.yoandev.skillcrew.sync.plist")); strings.Contains(got, "EnvironmentVariables") {
		t.Errorf("plist has EnvironmentVariables with an empty PATH:\n%s", got)
	}
	s := scheduler.NewSystemd(home, (&fake{}).run)
	scheduler.SetPath(s, "")
	if err := s.Install("/bin/skillcrew", time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(unitDir(home), "skillcrew-sync.service")); strings.Contains(got, "Environment=") {
		t.Errorf("service has Environment= with an empty PATH:\n%s", got)
	}
}

func TestInvalidInterval(t *testing.T) {
	for name, s := range map[string]scheduler.Scheduler{
		"launchd": scheduler.NewLaunchd(t.TempDir(), 501, (&fake{}).run),
		"systemd": scheduler.NewSystemd(t.TempDir(), (&fake{}).run),
	} {
		if err := s.Install("/bin/skillcrew", 0); err == nil {
			t.Errorf("%s: want error for zero interval", name)
		}
		if err := s.Install("/bin/skillcrew", 500*time.Millisecond); err == nil {
			t.Errorf("%s: want error for sub-second interval", name)
		}
	}
}

func TestExecRunner(t *testing.T) {
	if err := scheduler.ExecRunner("sh", "-c", "exit 0"); err != nil {
		t.Fatal(err)
	}
	err := scheduler.ExecRunner("sh", "-c", "echo oops >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "oops") {
		t.Errorf("err = %v, want combined output", err)
	}
}

// launchctl bootstrap right after bootout can fail transiently (error 5).
func TestLaunchdRetriesBootstrapOnce(t *testing.T) {
	home := t.TempDir()
	attempts := 0
	run := func(name string, args ...string) error {
		if len(args) > 0 && args[0] == "bootstrap" {
			if attempts++; attempts == 1 {
				return errors.New("Bootstrap failed: 5: Input/output error")
			}
		}
		return nil
	}
	s := scheduler.NewLaunchd(home, 501, run)
	if err := s.Install("/bin/skillcrew", time.Hour); err != nil || attempts != 2 || !s.Installed() {
		t.Fatalf("err=%v attempts=%d installed=%v", err, attempts, s.Installed())
	}
}
