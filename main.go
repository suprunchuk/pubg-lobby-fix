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

	"github.com/suprunchuk/pubg-lobby-fix/internal/app"
	"github.com/suprunchuk/pubg-lobby-fix/internal/elevate"
	"github.com/suprunchuk/pubg-lobby-fix/internal/update"
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
		pause       = flag.Duration("pause", 25*time.Millisecond, "delay between SetTcpEntry calls")
		rounds      = flag.Int("rounds", 4, "close+verify rounds before falling back to a traffic block")
		block       = flag.Duration("block", 10*time.Second, "WFP fallback: block all game traffic for this long when some connections survive (0 disables)")
		noElevate   = flag.Bool("no-elevate", false, "do not relaunch with administrator rights")
		noUpdate    = flag.Bool("no-update", false, "disable automatic self-update")
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
	// Log timestamps like "2026-09-05 12:42:53" — the RFC3339 default is
	// noisy, and a raw "+03:00" offset scares non-developers.
	logTime := func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == slog.TimeKey {
			if t, ok := a.Value.Any().(time.Time); ok {
				a.Value = slog.StringValue(t.Format("2006-01-02 15:04:05"))
			}
		}
		return a
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level, ReplaceAttr: logTime}))
	slog.SetDefault(log)

	// Remove the binary a previous self-update renamed out of the way.
	update.CleanupOld()

	// SetTcpEntry is rejected without elevation, so ask for it up front.
	// The elevated copy takes over and this process exits.
	if !*noElevate && !*list && !elevate.IsElevated() {
		log.Info("not running as administrator — requesting elevation, confirm the UAC prompt")
		if err := elevate.RelaunchAsAdmin(os.Args); err != nil {
			log.Warn("could not elevate; closing sockets will fail", "err", err)
		} else {
			return 0
		}
	}

	names := splitCSV(*process)
	cfg := app.Config{
		ProcessNames: names,
		Hotkey:       *hotkey,
		Pause:        *pause,
		Rounds:       *rounds,
		BlockWindow:  *block,
		Once:         *once,
		ListOnly:     *list,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if !*noUpdate && !*once && !*list && os.Getenv(update.EnvUpdated) != version {
		newVersion, err := update.MaybeAuto(ctx, version, log)
		switch {
		case err != nil:
			log.Warn("auto-update failed — download it manually from https://github.com/suprunchuk/pubg-lobby-fix/releases/latest", "err", err)
		case newVersion != "":
			log.Info("restarting with the new version", "version", newVersion)
			if err := update.Restart(newVersion); err != nil {
				log.Error("update installed but the restart failed — start the program again manually", "err", err)
				return 1
			}
			return 0
		}
	}

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
		out := flag.CommandLine.Output()
		_, _ = fmt.Fprintf(out, `pubg-lobby-fix — force-close PUBG (TslGame) TCP sockets to skip the black lobby screen.

Usage:
  pubg-lobby-fix [flags]

Flags:
`)
		flag.PrintDefaults()
		_, _ = fmt.Fprintf(out, `
Examples:
  pubg-lobby-fix                      # wait for Ctrl+Shift+L
  pubg-lobby-fix -hotkey f9           # use F9 instead
  pubg-lobby-fix -once                # close now and exit
  pubg-lobby-fix -list                # show current TslGame connections

Closing sockets needs administrator rights: the tool relaunches itself
elevated through UAC unless -no-elevate is given. Connections that survive
SetTcpEntry (e.g. IPv6) are handled by a short full-traffic block of the
game executables via the Windows Filtering Platform.

On start the tool downloads and installs newer releases by itself
(disable with -no-update).
`)
	}
}
