// Package agent installs the session-start hooks that make each AI coding
// agent run `skillcrew sync` when a session starts.
package agent

import (
	"strings"
)

// Agent is a coding agent that can trigger a sync when a session starts.
type Agent interface {
	// Name is the agent's stable identifier, such as "claude-code".
	Name() string
	// Detected reports whether the agent's config directory exists under home.
	Detected(home string) bool
	// HookInstalled reports whether a Skillcrew hook is registered.
	HookInstalled(home string) (bool, error)
	// InstallHook registers `<bin> sync --hook`. It is idempotent and keeps
	// every other setting and hook intact.
	InstallHook(home, bin string) error
	// UninstallHook removes Skillcrew's hook only; it is a no-op when absent.
	UninstallHook(home string) error
}

// All returns every supported agent.
func All() []Agent {
	return []Agent{
		&hookFile{
			name: "claude-code", dir: ".claude", file: "settings.json",
			list: "hooks.SessionStart", grouped: true, key: "command",
			entry: `{"matcher":"startup","hooks":[{"type":"command","command":""}]}`,
		},
		&hookFile{
			name: "codex", dir: ".codex", file: "hooks.json",
			list: "hooks.SessionStart", grouped: true, key: "command",
			entry: `{"hooks":[{"type":"command","command":""}]}`,
		},
		&hookFile{
			name: "copilot-cli", dir: ".copilot", file: "hooks/skillcrew.json",
			list: "hooks.sessionStart", key: "bash", version: true, owned: true,
			entry: `{"type":"command","bash":""}`,
		},
		&hookFile{
			name: "cursor", dir: ".cursor", file: "hooks.json",
			list: "hooks.sessionStart", key: "command", version: true,
			entry: `{"command":"","timeout":10}`,
		},
		&hookFile{
			name: "gemini-cli", dir: ".gemini", file: "settings.json",
			list: "hooks.SessionStart", grouped: true, key: "command", flag: "--hook=json",
			entry: `{"matcher":"startup","hooks":[{"type":"command","command":""}]}`,
		},
	}
}

// command builds the hook command line for the given binary.
func command(bin, flag string) string {
	if flag == "" {
		flag = "--hook"
	}
	return shellQuote(bin) + " sync " + flag
}

// isOurs reports whether a hook command was installed by Skillcrew, whatever
// the binary path it points to.
func isOurs(cmd string) bool {
	return strings.Contains(cmd, " sync --hook") && strings.Contains(strings.ToLower(cmd), "skillcrew")
}

// shellQuote single-quotes s when it holds anything but safe characters.
func shellQuote(s string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("_-./:@%+=,", r)
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
