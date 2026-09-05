package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/suprunchuk/pubg-lobby-fix/internal/hotkey"
	"github.com/suprunchuk/pubg-lobby-fix/internal/process"
	"github.com/suprunchuk/pubg-lobby-fix/internal/tcp"
	"github.com/suprunchuk/pubg-lobby-fix/internal/tray"
	"github.com/suprunchuk/pubg-lobby-fix/internal/wfp"
)

// Config holds runtime options for the monitor.
type Config struct {
	ProcessNames []string
	Hotkey       string
	Pause        time.Duration
	// Rounds is the maximum number of close+verify rounds before the traffic
	// block fallback kicks in.
	Rounds int
	// BlockWindow is how long the WFP fallback blocks all game traffic.
	// Zero disables the fallback.
	BlockWindow time.Duration
	Once        bool
	ListOnly    bool
	// JSON makes -list print connections as JSON to stdout.
	JSON bool
	// Tray shows the notification-area icon and hides the console window.
	// Only used in monitor mode (-once and -list print to the console).
	Tray bool
}

// verifyDelay is how long to wait after a close round before re-reading the
// TCP table to detect survivors.
const verifyDelay = 200 * time.Millisecond

// Run starts the monitor according to cfg until ctx is cancelled.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if len(cfg.ProcessNames) == 0 {
		cfg.ProcessNames = []string{"TslGame"}
	}
	if cfg.Pause <= 0 {
		cfg.Pause = 25 * time.Millisecond
	}
	if cfg.Rounds <= 0 {
		cfg.Rounds = 4
	}
	if log == nil {
		log = slog.Default()
	}

	if cfg.ListOnly {
		return listConnections(cfg, log)
	}
	if cfg.Once {
		_, err := closeLobby(ctx, cfg, log)
		return err
	}

	binding, err := hotkey.Parse(cfg.Hotkey)
	if err != nil {
		return fmt.Errorf("parse hotkey: %w", err)
	}

	log.Info("waiting for hotkey",
		"hotkey", binding.Display,
		"processes", strings.Join(cfg.ProcessNames, ","),
	)

	presses := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	trayDone := make(chan error, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		errCh <- hotkey.Listen(ctx, binding, presses)
	}()

	// The tray icon mirrors the hotkey: a left click or the menu item feeds
	// the same presses channel, so the busy guard covers both triggers.
	if cfg.Tray {
		go func() {
			trayDone <- tray.Run(ctx, tray.Options{
				Tooltip: "pubg-lobby-fix — " + binding.Display,
				OnTrigger: func() {
					select {
					case presses <- struct{}{}:
					default:
					}
				},
			})
		}()
	}

	var busy atomic.Bool

	for {
		select {
		case <-ctx.Done():
			<-errCh
			return ctx.Err()
		case err := <-errCh:
			if err != nil && ctx.Err() == nil {
				return err
			}
			return ctx.Err()
		case err := <-trayDone:
			switch {
			case errors.Is(err, tray.ErrExitRequested):
				log.Info("exit requested from the tray menu")
				return nil
			case err == nil || ctx.Err() != nil:
				// The tray stopped because the app is stopping.
			default:
				log.Warn("tray icon is not available — keep the console window open", "err", err)
			}
		case <-presses:
			if !busy.CompareAndSwap(false, true) {
				log.Warn("already closing connections, ignoring hotkey")
				continue
			}

			log.Info("hotkey pressed, closing lobby connections")
			res, err := closeLobby(ctx, cfg, log)
			if err != nil && ctx.Err() == nil {
				log.Error("close failed", "err", err)
			}
			if title, body, warn := closeBalloon(res, err); title != "" {
				tray.Notify(title, body, warn)
			}
			busy.Store(false)
		}
	}
}

// jsonConn is the JSON shape of one TCP connection for -list -json.
type jsonConn struct {
	PID     uint32 `json:"pid"`
	Process string `json:"process"`
	Family  string `json:"family"`
	Local   string `json:"local"`
	Remote  string `json:"remote"`
	State   string `json:"state"`
}

func listConnections(cfg Config, log *slog.Logger) error {
	procs, conns, errs, err := collect(cfg.ProcessNames)
	if err != nil {
		return err
	}
	logWarnings(log, errs)
	if cfg.JSON {
		out := make([]jsonConn, 0, len(conns))
		for _, c := range conns {
			out = append(out, jsonConn{
				PID:     c.PID,
				Process: c.ProcessName,
				Family:  c.Family.String(),
				Local:   formatEndpoint(c.LocalAddr, c.LocalPort),
				Remote:  formatEndpoint(c.RemoteAddr, c.RemotePort),
				State:   c.State.String(),
			})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(conns) == 0 {
		log.Info("no connections found", "processes", cfg.ProcessNames)
		return nil
	}
	log.Info("processes", "count", len(procs))
	log.Info("connections", "count", len(conns))
	for _, c := range conns {
		log.Info("tcp", "conn", c.String())
	}
	return nil
}

func formatEndpoint(ip net.IP, port uint16) string {
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(port)))
}

// closeStats counts the outcomes of one closeLobby run.
type closeStats struct {
	// Closed are connections deleted by SetTcpEntry, Gone are ones that
	// vanished between listing and closing.
	Closed, Gone, Failed, Survived int
	// Blocked reports that the WFP fallback was applied to clear survivors.
	Blocked bool
}

// closeLobby kills every TCP connection owned by the game processes, with
// verification and a fallback for connections SetTcpEntry cannot delete.
// It stops early (returning ctx.Err()) when ctx is cancelled.
func closeLobby(ctx context.Context, cfg Config, log *slog.Logger) (stats closeStats, err error) {
	procs, err := process.FindByName(cfg.ProcessNames...)
	if err != nil {
		return stats, fmt.Errorf("find processes: %w", err)
	}
	if len(procs) == 0 {
		log.Info("game is not running", "processes", cfg.ProcessNames)
		return stats, nil
	}

	pids := make(map[uint32]string, len(procs))
	var paths []string
	for _, p := range procs {
		pids[p.PID] = p.Name
		if p.ExePath != "" {
			paths = append(paths, p.ExePath)
		}
	}

	// snapshot returns the connections that are worth trying to delete right now.
	snapshot := func() []tcp.Connection {
		conns, errs := tcp.ListByPIDs(pids)
		logWarnings(log, errs)
		killable := make([]tcp.Connection, 0, len(conns))
		for _, c := range conns {
			if c.CanDelete() {
				killable = append(killable, c)
			}
		}
		return killable
	}

	targets := snapshot()
	if len(targets) == 0 {
		log.Info("no closeable lobby connections", "processes", cfg.ProcessNames)
		return stats, nil
	}

	var accessDenied bool
	var survivors []tcp.Connection

	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		log.Info("closing lobby connections", "round", round, "count", len(targets))
		for _, r := range tcp.CloseAll(v4Only(targets), cfg.Pause) {
			switch {
			case r.Err == nil:
				stats.Closed++
			case errors.Is(r.Err, tcp.ErrVanished):
				stats.Gone++
			case errors.Is(r.Err, tcp.ErrAccessDenied):
				stats.Failed++
				accessDenied = true
				log.Warn("close failed (not elevated?)", "conn", r.Connection.String(), "err", r.Err)
			default:
				stats.Failed++
				log.Warn("close failed", "conn", r.Connection.String(), "err", r.Err)
			}
		}

		select {
		case <-ctx.Done():
			return stats, ctx.Err()
		case <-time.After(verifyDelay):
		}
		survivors = intersect(targets, snapshot())
		if len(survivors) == 0 {
			break
		}
		if round >= cfg.Rounds || allIPv6(survivors) {
			break
		}
		log.Info("connections survived the round, retrying", "survived", len(survivors))
		targets = survivors
	}

	stats.Survived = len(survivors)
	log.Info("done",
		"closed", stats.Closed,
		"already_gone", stats.Gone,
		"failed", stats.Failed,
		"survived", stats.Survived,
	)

	if len(survivors) == 0 {
		if accessDenied && stats.Closed == 0 && stats.Gone == 0 {
			return stats, errors.New("every SetTcpEntry call was denied — run as administrator")
		}
		return stats, nil
	}

	// Fallback: SetTcpEntry cannot delete IPv6 TCBs (no public API) and a
	// raced row could legitimately fail. Block ALL traffic of the game
	// executables for a short window instead; the engine tears down the
	// flows and the game rebuilds its lobby connections once unblocked.
	resolvedByBlock := false
	blocked, err := blockGameTraffic(ctx, paths, cfg.BlockWindow, log)
	switch {
	case err != nil:
		log.Error("traffic block failed", "err", err)
	case blocked:
		stats.Blocked = true
		still := intersect(targets, snapshot())
		if len(still) == 0 {
			log.Info("traffic block cleared the remaining connections")
			resolvedByBlock = true
			stats.Survived = 0
		} else {
			log.Warn("connections still present after the traffic block", "count", len(still))
		}
	}

	if stats.Closed+stats.Gone == 0 && !resolvedByBlock {
		hint := ""
		if accessDenied {
			hint = " (run as administrator)"
		}
		return stats, fmt.Errorf("could not close %d connection(s)%s", len(targets), hint)
	}
	return stats, nil
}

// closeBalloon turns a closeLobby outcome into a tray balloon; an empty title
// means there is nothing to report (tray inactive or the run was cancelled).
func closeBalloon(res closeStats, err error) (title, body string, warn bool) {
	if errors.Is(err, context.Canceled) {
		return "", "", false
	}
	if err != nil {
		return "Close failed", err.Error(), true
	}
	if res.Closed == 0 && res.Gone == 0 && !res.Blocked {
		return "Nothing to close", "no open game connections right now", false
	}
	var parts []string
	if res.Closed > 0 {
		parts = append(parts, fmt.Sprintf("closed %d", res.Closed))
	}
	if res.Gone > 0 {
		parts = append(parts, fmt.Sprintf("%d already gone", res.Gone))
	}
	if res.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", res.Failed))
	}
	if res.Blocked {
		parts = append(parts, "traffic blocked briefly")
	}
	return "Sockets closed", strings.Join(parts, ", "), res.Failed > 0
}

// blockGameTraffic applies the WFP fallback to every known game executable.
// Returns false when the fallback is disabled or cannot be scoped.
func blockGameTraffic(ctx context.Context, paths []string, window time.Duration, log *slog.Logger) (bool, error) {
	if window <= 0 {
		log.Warn("connections survived; traffic block fallback is disabled (-block 0)")
		return false, nil
	}
	if len(paths) == 0 {
		log.Warn("connections survived; executable paths are unknown, cannot apply the traffic block")
		return false, nil
	}

	log.Warn("blocking all game traffic via WFP", "window", window, "paths", paths)
	for _, p := range paths {
		if err := wfp.BlockApp(ctx, p, window); err != nil {
			return true, fmt.Errorf("block %s: %w", p, err)
		}
	}
	log.Info("traffic block lifted")
	return true, nil
}

func collect(names []string) ([]process.Process, []tcp.Connection, []error, error) {
	procs, err := process.FindByName(names...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("find processes: %w", err)
	}
	if len(procs) == 0 {
		return nil, nil, nil, nil
	}
	pids := make(map[uint32]string, len(procs))
	for _, p := range procs {
		pids[p.PID] = p.Name
	}
	conns, errs := tcp.ListByPIDs(pids)
	return procs, conns, errs, nil
}

func logWarnings(log *slog.Logger, errs []error) {
	for _, e := range errs {
		log.Warn("tcp table query failed", "err", e)
	}
}

func v4Only(conns []tcp.Connection) []tcp.Connection {
	out := make([]tcp.Connection, 0, len(conns))
	for _, c := range conns {
		if c.Family == tcp.IPv4 {
			out = append(out, c)
		}
	}
	return out
}

func allIPv6(conns []tcp.Connection) bool {
	if len(conns) == 0 {
		return false
	}
	for _, c := range conns {
		if c.Family != tcp.IPv6 {
			return false
		}
	}
	return true
}

// intersect returns the connections from want that are still present in now.
func intersect(want, now []tcp.Connection) []tcp.Connection {
	present := make(map[string]struct{}, len(now))
	for _, c := range now {
		present[c.Key()] = struct{}{}
	}
	out := make([]tcp.Connection, 0, len(want))
	for _, c := range want {
		if _, ok := present[c.Key()]; ok {
			out = append(out, c)
		}
	}
	return out
}
