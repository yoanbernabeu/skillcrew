package gitx_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yoanbernabeu/skillcrew/internal/gitx"
)

// A user's core.sshCommand (e.g. selecting a key) must be honored.
func TestUserSSHCommandIsHonored(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "called")
	script := filepath.Join(t.TempDir(), "ssh.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[core]\n\tsshCommand = "+script+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_SSH_COMMAND", "")
	os.Unsetenv("GIT_SSH_COMMAND")
	_, _ = gitx.Clone(context.Background(), "ssh://example.invalid/x.git", filepath.Join(t.TempDir(), "reg.git"))
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the user's core.sshCommand was not used")
	}
}
