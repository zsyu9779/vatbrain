// Command vatbrain-provider is the stdio JSON-RPC daemon that hermes spawns
// as its memory provider backend (D1: no HTTP, no MCP; line-delimited
// JSON-RPC 2.0 on stdin/stdout). hermes drives it via the
// $HERMES_HOME/plugins/vatbrain/ MemoryProvider plugin.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/provider"
)

func main() {
	storeBackend := flag.String("store", "sqlite",
		"store backend (sqlite is the only maintained backend)")
	dataDir := flag.String("data", "",
		"data directory (default $HERMES_HOME/vatbrain or ~/.hermes/vatbrain)")
	maxReqBytes := flag.Int("max-request-bytes", 4<<20,
		"maximum JSON-RPC request line size in bytes")
	flag.Parse()

	if *storeBackend != "sqlite" {
		slog.Error("provider: unsupported store backend", "backend", *storeBackend)
		os.Exit(1)
	}

	// Resolve the data directory; the SQLite DB lives under it.
	if *dataDir == "" {
		home := os.Getenv("HERMES_HOME")
		if home == "" {
			if h, err := os.UserHomeDir(); err == nil {
				home = filepath.Join(h, ".hermes")
			}
		}
		*dataDir = filepath.Join(home, "vatbrain")
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		slog.Error("provider: create data dir", "err", err)
		os.Exit(1)
	}

	// Configure the store through the env config loader that app.New reads.
	os.Setenv("VATBRAIN_STORE_BACKEND", *storeBackend)
	os.Setenv("VATBRAIN_SQLITE_PATH", filepath.Join(*dataDir, "vatbrain.db"))
	// The daemon does its own per-turn intake; the passive watcher stays off.
	os.Setenv("VATBRAIN_WATCHER_ENABLED", "false")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		slog.Error("provider: bootstrap failed", "err", err)
		os.Exit(1)
	}
	defer a.Close()

	deps := core.WriteDeps{
		Store:       a.Store,
		Gate:        a.SignificanceGate,
		PatternSep:  a.PatternSeparation,
		WeightDecay: a.WeightDecay,
		Embedder:    a.Embedder,
		WorkingMem:  a.WorkingMemory,
	}

	srv := provider.NewServer(deps)
	srv.Consolidation = a.Consolidation
	// VATBRAIN_GATE_MODE=off force-confirms every synced turn, bypassing the
	// significance gate (benchmark kernel measurement; mirrors internal/bench).
	if os.Getenv("VATBRAIN_GATE_MODE") == "off" {
		srv.ForceConfirm = true
		slog.Info("vatbrain-provider: significance gate disabled (VATBRAIN_GATE_MODE=off)")
	}
	go func() {
		<-srv.ShutdownSignal()
		stop()
	}()

	slog.Info("vatbrain-provider: ready",
		"store", *storeBackend, "data", *dataDir)

	if err := srv.Serve(ctx, os.Stdin, os.Stdout, *maxReqBytes); err != nil {
		slog.Warn("provider: serve ended", "err", err)
	}
	slog.Info("vatbrain-provider: exiting")
}
