package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/peilungao/reverse-geocoding/internal/adapter"
)

// main starts the internal Photon-compatible reverse-geocoding adapter.
func main() {
	config, err := adapter.LoadConfig(os.Getenv)
	if err != nil {
		slog.Error("configuration is invalid", "error", err)
		os.Exit(1)
	}

	service, err := adapter.NewService(config, nil)
	if err != nil {
		slog.Error("service initialization failed", "error", err)
		os.Exit(1)
	}
	defer service.Close()

	server := &http.Server{
		Addr:              config.ListenAddress,
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errorsCh := make(chan error, 1)
	go func() { errorsCh <- server.ListenAndServe() }()

	signalContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	case <-signalContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}
	}

	fmt.Fprintln(os.Stderr, "reverse-geocoding adapter stopped")
}
