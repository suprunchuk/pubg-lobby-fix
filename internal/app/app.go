package app

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	"pubg-lobby-fix/internal/hotkey"
	"pubg-lobby-fix/internal/process"
	"pubg-lobby-fix/internal/tcp"
)

// Config holds runtime options for the monitor.
type Config struct {
	ProcessNames []string
	Hotkey       string
	Pause        time.Duration
	Once         bool
	ListOnly     bool
}

// Run starts the monitor according to cfg until ctx is cancelled.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if len(cfg.ProcessNames) == 0 {
		cfg.ProcessNames = []string{"TslGame"}
	}
	if cfg.Pause <= 0 {
		cfg.Pause = 100 * time.Millisecond
	}
	if log == nil {
		log = slog.Default()
	}

	if cfg.ListOnly {
		return listConnections(cfg, log)
	}
	if cfg.Once {
		return closeLobby(cfg, log)
	}

	binding, err := hotkey.Parse(cfg.Hotkey)
	if err != nil {
		return fmt.Errorf("parse hotkey: %w", err)
	}

	log.Info("waiting for hotkey",
		"hotkey", binding.Display,
		"processes", strings.Join(cfg.ProcessNames, ","),
	)
	log.Info("run as administrator — SetTcpEntry needs elevation")

	presses := make(chan struct{}, 1)
	errCh := make(chan error, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		errCh <- hotkey.Listen(ctx, binding, presses)
	}()

	var mu sync.Mutex
	busy := false

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
		case <-presses:
			mu.Lock()
			if busy {
				mu.Unlock()
				log.Warn("already closing connections, ignoring hotkey")
				continue
			}
			busy = true
			mu.Unlock()

			log.Info("hotkey pressed, closing lobby connections")
			if err := closeLobby(cfg, log); err != nil {
				log.Error("close failed", "err", err)
			}

			mu.Lock()
			busy = false
			mu.Unlock()
		}
	}
}

func listConnections(cfg Config, log *slog.Logger) error {
	conns, err := collect(cfg.ProcessNames)
	if err != nil {
		return err
	}
	if len(conns) == 0 {
		log.Info("no connections found", "processes", cfg.ProcessNames)
		return nil
	}
	log.Info("connections", "count", len(conns))
	for _, c := range conns {
		log.Info("tcp", "conn", c.String())
	}
	return nil
}

func closeLobby(cfg Config, log *slog.Logger) error {
	conns, err := collect(cfg.ProcessNames)
	if err != nil {
		return err
	}

	targets := make([]tcp.Connection, 0, len(conns))
	for _, c := range conns {
		if c.IsRemoteZero() {
			continue
		}
		targets = append(targets, c)
	}

	if len(targets) == 0 {
		log.Info("nothing to close",
			"scanned", len(conns),
			"processes", cfg.ProcessNames,
		)
		return nil
	}

	log.Info("closing connections", "count", len(targets))
	results := tcp.CloseAll(targets, cfg.Pause)

	var closed, failed int
	for _, r := range results {
		if r.Err != nil {
			failed++
			log.Warn("close failed", "conn", r.Connection.String(), "err", r.Err)
			continue
		}
		closed++
		log.Info("closed", "conn", r.Connection.String())
	}
	log.Info("done", "closed", closed, "failed", failed, "total", len(targets))
	if failed > 0 && closed == 0 {
		return fmt.Errorf("closed 0/%d connections (try running as administrator)", len(targets))
	}
	return nil
}

func collect(names []string) ([]tcp.Connection, error) {
	pids, err := process.FindByName(names...)
	if err != nil {
		return nil, fmt.Errorf("find processes: %w", err)
	}
	if len(pids) == 0 {
		return nil, nil
	}
	conns, err := tcp.ListByPIDs(pids)
	if err != nil {
		return nil, fmt.Errorf("list tcp: %w", err)
	}
	return conns, nil
}
