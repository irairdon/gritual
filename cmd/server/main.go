package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/webui"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{Key: "ts", Value: a.Value}
			}
			return a
		},
	})))

	httpAddr := env("HTTP_ADDR", ":8080")
	metricsAddr := env("METRICS_ADDR", "127.0.0.1:9090")
	_ = env("APP_BASE_URL", "http://localhost:8080")
	mediaDir := env("MEDIA_DIR", "./data/media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		slog.Error("mkdir media", "err", err)
		os.Exit(1)
	}

	ui, err := webui.FS()
	if err != nil {
		slog.Error("webui embed", "err", err)
		os.Exit(1)
	}

	public := &http.Server{
		Addr:              httpAddr,
		Handler:           httpx.NewRouter(ui),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      0,
	}
	metrics := &http.Server{
		Addr:              metricsAddr,
		Handler:           httpx.MetricsMux(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		slog.Info("http listen", "addr", httpAddr)
		if err := public.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		slog.Info("metrics listen", "addr", metricsAddr)
		if err := metrics.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		slog.Error("server", "err", err)
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = public.Shutdown(shutdownCtx)
	_ = metrics.Shutdown(shutdownCtx)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
