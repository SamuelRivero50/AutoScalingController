// Command testapp is the minimal, stateless test application run behind the
// load balancer during the real-AWS integration run (docs/spec/app.md).
//
// Routes:
//
//	GET  /                       static response
//	GET  /health                 health check, independent of the stress path
//	POST /admin/stress?duration  busy the CPU at ~85% for a bounded duration
//
// Usage:
//
//	testapp [-addr :8080]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// stressDuty is the fraction of each time slot the stress keeps a CPU busy.
const stressDuty = 0.85

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr, nil)
	stop()
	os.Exit(code)
}

// run serves until ctx is done. ready, if not nil, receives the listening
// address once the server accepts connections.
func run(ctx context.Context, args []string, stderr io.Writer, ready chan<- string) int {
	fs := flag.NewFlagSet("testapp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":8080", "listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))

	if err := serve(ctx, *addr, logger, ready); err != nil {
		logger.Error("server failed", "err", err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, addr string, logger *slog.Logger, ready chan<- string) error {
	stressCtx, cancelStress := context.WithCancel(ctx)
	defer cancelStress()
	stresser := newBurner(stressCtx, stressDuty)

	srv := &http.Server{
		Handler:           http.MaxBytesHandler(newHandler(stresser, logger), 1<<10),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	if ready != nil {
		ready <- ln.Addr().String()
	}
	logger.Info("listening", "addr", ln.Addr().String())

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	cancelStress()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	stresser.Wait()
	logger.Info("stopped")
	return nil
}
