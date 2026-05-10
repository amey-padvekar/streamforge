package main

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"streamforge/internal/server/router"
	"streamforge/internal/server/session"
	"streamforge/internal/server/transport"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	addr := resolveServerAddr()

	registry := session.NewRegistry()
	sessionHandler := router.NewSessionHandler(registry)
	wsHandler := transport.NewWSHandler(registry)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/sessions", sessionHandler.HandleSessions)
	mux.HandleFunc("/api/sessions/", sessionHandler.HandleSessionMetrics)
	mux.HandleFunc("/ws/session/", wsHandler.HandleSessionWS)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startSessionTelemetryLogger(ctx, registry, logger)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("streamforge server starting", "addr", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}

		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received", "signal", "interrupt")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}

		if err := <-errCh; err != nil {
			logger.Error("server stopped with error", "error", err)
			os.Exit(1)
		}

		logger.Info("server stopped gracefully")
	case err := <-errCh:
		if err != nil {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}

		logger.Info("server stopped")
	}
}

type sessionFPSState struct {
	framesReceived uint64
	lastAt         time.Time
}

func startSessionTelemetryLogger(ctx context.Context, registry *session.Registry, logger *slog.Logger) {
	if registry == nil || logger == nil {
		return
	}

	const interval = 5 * time.Second
	ticker := time.NewTicker(interval)
	states := make(map[string]sessionFPSState)

	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				sessions := registry.List()
				alive := make(map[string]struct{}, len(sessions))

				for _, s := range sessions {
					metrics := s.MetricsSnapshot()
					alive[metrics.SessionID] = struct{}{}

					prev, ok := states[metrics.SessionID]
					if !ok {
						states[metrics.SessionID] = sessionFPSState{framesReceived: metrics.FramesReceived, lastAt: now}
						continue
					}

					elapsed := now.Sub(prev.lastAt).Seconds()
					fps := 0.0
					if elapsed > 0 && metrics.FramesReceived >= prev.framesReceived {
						fps = float64(metrics.FramesReceived-prev.framesReceived) / elapsed
					}

					logger.Info(
						"session telemetry",
						"sessionId", metrics.SessionID,
						"fps", math.Round(fps*100)/100,
						"framesReceived", metrics.FramesReceived,
						"framesForwarded", metrics.FramesForwarded,
						"framesDropped", metrics.FramesDropped,
						"viewerCount", metrics.ViewerCount,
					)

					states[metrics.SessionID] = sessionFPSState{framesReceived: metrics.FramesReceived, lastAt: now}
				}

				for sessionID := range states {
					if _, ok := alive[sessionID]; !ok {
						delete(states, sessionID)
					}
				}
			}
		}
	}()
}

func resolveServerAddr() string {
	if addr := strings.TrimSpace(os.Getenv("SERVER_ADDR")); addr != "" {
		if strings.HasPrefix(addr, ":") || strings.Contains(addr, ":") {
			return addr
		}

		return ":" + addr
	}

	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		if strings.HasPrefix(port, ":") {
			return port
		}

		return ":" + port
	}

	return ":8080"
}
