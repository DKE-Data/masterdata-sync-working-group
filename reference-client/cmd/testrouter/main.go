// Command testrouter serves the AgmaSync test router over HTTP.
//
// It is the same implementation the in-process tests mount; this wraps it in a
// listener so the containerised integration tests, and anyone poking at the
// protocol with curl, can reach it.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter"
)

func main() {
	addr := os.Getenv("TESTROUTER_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	server := &http.Server{
		Addr:    addr,
		Handler: testrouter.New().Handler(),
		// The event streams are long-lived by design, so there is no write
		// timeout to cut them off. Read timeouts are safe.
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("test router listening", "addr", addr)
		if err := server.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			slog.Error("test router failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		slog.Error("shutdown failed", "error", err)
	}
}
