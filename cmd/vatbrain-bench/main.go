// Command vatbrain-bench is the HTTP evaluation entrypoint for the OmniMemEval
// benchmark integration (docs/v0.3/tech-specs/03-omnimemeval-benchmark.md).
//
// It exposes add/search/delete over HTTP on a dedicated port so the OmniMemEval
// harness can drive VatBrain as a memory backend. It uses an independent SQLite
// DB and the same app.New assembly as production, with a gate-mode ablation
// knob (off = benchmark the storage + retrieval kernel, on = run the real
// significance gate).
//
// The server has no per-user authentication. It binds 127.0.0.1 by default;
// binding beyond loopback requires VATBRAIN_BENCH_API_TOKEN, which callers must
// send as Authorization: Bearer <token>.
//
// Run:
//
//	go run ./cmd/vatbrain-bench --gate off --port 18080
//
// The semantic embedder is chosen by the standard VATBRAIN_EMBEDDER_* env vars;
// with no key it falls back to the local keyword embedder (smoke only).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/bench"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
)

func main() {
	if err := run(); err != nil {
		slog.Error("vatbrain-bench: fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	host := flag.String("host", "127.0.0.1", "HTTP listen host (bench server has no auth — bind loopback unless you set VATBRAIN_BENCH_API_TOKEN)")
	port := flag.Int("port", 18080, "HTTP listen port")
	gateMode := flag.String("gate", "", "significance gate mode: off|on (default off; env VATBRAIN_BENCH_GATE_MODE)")
	language := flag.String("language", "", "memory language (default en; env VATBRAIN_BENCH_LANGUAGE)")
	dataDir := flag.String("data", "", "data dir for the benchmark SQLite DB (default $VATBRAIN_BENCH_DATA_DIR or ~/.vatbrain/bench)")
	flag.Parse()

	mode := *gateMode
	if mode == "" {
		mode = os.Getenv("VATBRAIN_BENCH_GATE_MODE")
	}
	if mode == "" {
		mode = string(bench.GateModeOff)
	}
	gm := bench.GateMode(mode)
	if gm != bench.GateModeOff && gm != bench.GateModeOn {
		return fmt.Errorf("invalid gate mode %q (want off|on)", mode)
	}

	lang := *language
	if lang == "" {
		lang = os.Getenv("VATBRAIN_BENCH_LANGUAGE")
	}
	if lang == "" {
		lang = "en"
	}

	token := os.Getenv("VATBRAIN_BENCH_API_TOKEN")
	if *host != "127.0.0.1" && *host != "localhost" && token == "" {
		return fmt.Errorf("refusing to bind non-loopback host %q without VATBRAIN_BENCH_API_TOKEN", *host)
	}

	if *dataDir == "" {
		*dataDir = os.Getenv("VATBRAIN_BENCH_DATA_DIR")
	}
	if *dataDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			*dataDir = filepath.Join(home, ".vatbrain", "bench")
		} else {
			*dataDir = "./vatbrain-bench-data"
		}
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("create bench data dir: %w", err)
	}

	// The bench server owns its own SQLite DB and never runs the passive
	// watcher — it is a pure add/search/delete evaluation endpoint.
	os.Setenv("VATBRAIN_STORE_BACKEND", "sqlite")
	os.Setenv("VATBRAIN_SQLITE_PATH", filepath.Join(*dataDir, "vatbrain-bench.db"))
	os.Setenv("VATBRAIN_WATCHER_ENABLED", "false")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	defer a.Close()

	// Log the active semantic embedder so a run that forgot to source the
	// embedding credentials (VATBRAIN_EMBEDDER_*) does not silently fall back
	// to the keyword embedder — mixing vector spaces ruins retrieval.
	slog.Info("vatbrain-bench embedder",
		"semantic_provider", os.Getenv("VATBRAIN_EMBEDDER_SEMANTIC_PROVIDER"),
		"semantic_model", os.Getenv("VATBRAIN_EMBEDDER_SEMANTIC_MODEL"),
	)

	deps := core.WriteDeps{
		Store:       a.Store,
		Gate:        a.SignificanceGate,
		PatternSep:  a.PatternSeparation,
		WeightDecay: a.WeightDecay,
		Embedder:    a.Embedder,
		WorkingMem:  a.WorkingMemory,
		// The benchmark does not consume RELATES_TO edges; skipping LinkOnWrite
		// avoids up to ~40 embedding calls per message (docs/v0.3/tech-specs/
		// 03-omnimemeval-benchmark.md).
		SkipLinkOnWrite: true,
	}

	srv, err := bench.NewServer(deps, bench.Options{
		GateMode: gm,
		Language: lang,
		TaskType: models.TaskTypeFeature,
		Token:    token,
	})
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(*host, strconv.Itoa(*port))
	if err := srv.ListenAndServe(ctx, addr); err != nil && err != http.ErrServerClosed {
		return err
	}
	slog.Info("vatbrain-bench: exiting")
	return nil
}
