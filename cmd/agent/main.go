package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"streamforge/internal/agent/scheduler"
	agenttransport "streamforge/internal/agent/transport"
)

const (
	defaultTargetFPS   = 20
	defaultJPEGQuality = 75
	defaultMaxRetries  = 5
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	serverURL := os.Getenv("SERVER_URL")
	sessionID := os.Getenv("SESSION_ID")
	agentToken := os.Getenv("AGENT_TOKEN")
	targetFPS := envInt("TARGET_FPS", defaultTargetFPS)
	jpegQuality := envInt("JPEG_QUALITY", defaultJPEGQuality)

	if serverURL == "" || sessionID == "" || agentToken == "" {
		logger.Error("missing required environment variables", "required", "SERVER_URL, SESSION_ID, AGENT_TOKEN")
		os.Exit(1)
	}

	transport, err := agenttransport.NewWSTransport(serverURL, sessionID, agentToken, defaultMaxRetries, logger)
	if err != nil {
		logger.Error("failed to initialize websocket transport", "error", err)
		os.Exit(1)
	}

	// Perform the initial auth handshake up front before starting scheduler loop.
	if err := transport.Connect(); err != nil {
		logger.Error("failed to establish initial websocket connection", "error", err)
		os.Exit(1)
	}

	sch := scheduler.NewScheduler(targetFPS, jpegQuality, transport)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("streamforge agent starting",
		"serverURL", serverURL,
		"sessionID", sessionID,
		"targetFPS", targetFPS,
		"jpegQuality", jpegQuality,
	)

	sch.Start(ctx)
	sch.Stop()

	logger.Info("streamforge agent stopped")
}

func envInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}

	v, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("invalid integer env var; using fallback", "name", name, "value", raw, "fallback", fallback, "error", fmt.Sprintf("%v", err))
		return fallback
	}

	return v
}
