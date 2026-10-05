package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"middle-monitor/backend/services"
)

// RunServer wires up an http.Server with sane production timeouts and graceful
// shutdown on SIGINT/SIGTERM. Pass it the router built by SetupAPIRouter or
// SetupReceiverRouter. Returns when the server has fully drained or after the
// shutdown timeout elapses.
func RunServer(name, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		// Defensive timeouts: every public endpoint sits behind these. Slow
		// clients can no longer hold a goroutine forever.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "service", name, "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		slog.Info("shutting down", "service", name, "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("shutdown failed", "service", name, "error", err)
			return err
		}
		// A burst accumulating in a group_wait only lives in memory: deliver it
		// now rather than losing the alerts to the restart.
		services.FlushPendingGroups()
		slog.Info("shutdown complete", "service", name)
		return nil
	}
}
