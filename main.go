package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"pubg-lobby-fix/internal/app"
)

// Set by GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		hotkey      = flag.String("hotkey", "ctrl+shift+l", "global hotkey (e.g. ctrl+shift+l, f9, alt+f10)")
		process     = flag.String("process", "TslGame", "comma-separated process names without .exe")
		pause       = flag.Duration("pause", 100*time.Millisecond, "delay between SetTcpEntry calls")
		once        = flag.Bool("once", false, "close connections once and exit")
		list        = flag.Bool("list", false, "list TslGame TCP connections and exit")
		verbose     = flag.Bool("v", false, "verbose (debug) logging")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("pubg-lobby-fix %s (commit %s, built %s)\n", version, commit, date)
		return 0
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	names := splitCSV(*process)
	cfg := app.Config{
		ProcessNames: names,
		Hotkey:       *hotkey,
		Pause:        *pause,
		Once:         *once,
		ListOnly:     *list,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := app.Run(ctx, cfg, log); err != nil {
		if ctx.Err() != nil {
			log.Info("stopped")
			return 0
		}
		log.Error("fatal", "err", err)
		return 1
	}
	return 0
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func init() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `pubg-lobby-fix — force-close PUBG (TslGame) TCP sockets to skip the black lobby screen.

Usage:
  pubg-lobby-fix [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), `
Examples:
  pubg-lobby-fix                      # wait for Ctrl+Shift+L
  pubg-lobby-fix -hotkey f9           # use F9 instead
  pubg-lobby-fix -once                # close now and exit
  pubg-lobby-fix -list                # show current TslGame connections

Requires administrator privileges for SetTcpEntry to succeed.
`)
	}
}
