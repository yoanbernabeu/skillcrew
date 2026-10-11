// Command skillcrew distributes and governs AI agent skills across a team.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/yoanbernabeu/skillcrew/internal/cli"
)

// version is set at build time by GoReleaser.
var version = "dev"

// resolveVersion returns the build-time version, falling back to the module
// version recorded by the Go toolchain, so that
// `go install github.com/yoanbernabeu/skillcrew/cmd/skillcrew@v0.1.0` reports
// v0.1.0 instead of dev.
func resolveVersion(v string, info *debug.BuildInfo, ok bool) string {
	if v != "dev" || !ok || info == nil {
		return v
	}
	if mv := info.Main.Version; mv != "" && mv != "(devel)" {
		return mv
	}
	return v
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	info, ok := debug.ReadBuildInfo()
	app, err := cli.Default(resolveVersion(version, info, ok))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	code := app.Execute(ctx)
	stop()
	os.Exit(code)
}
