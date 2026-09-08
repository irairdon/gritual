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

	"github.com/go-chi/chi/v5"
	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/circles"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/logs"
	"github.com/irairdon/gritual/internal/media"
	"github.com/irairdon/gritual/internal/rituals"
	"github.com/irairdon/gritual/internal/webui"
	"github.com/jackc/pgx/v5/pgxpool"
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

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.MediaDir, 0o755); err != nil {
		slog.Error("mkdir media", "err", err)
		os.Exit(1)
	}

	ui, err := webui.FS()
	if err != nil {
		slog.Error("webui embed", "err", err)
		os.Exit(1)
	}

	var pool *pgxpool.Pool
	if cfg.DatabaseURL != "" {
		openCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		p, err := db.Open(openCtx, cfg.DatabaseURL)
		cancel()
		if err != nil {
			slog.Error("db open", "err", err)
			os.Exit(1)
		}
		defer p.Close()
		migCtx, migCancel := context.WithTimeout(context.Background(), 60*time.Second)
		err = db.RunMigrations(migCtx, p)
		migCancel()
		if err != nil {
			slog.Error("migrations", "err", err)
			os.Exit(1)
		}
		pool = p
	} else {
		slog.Warn("DATABASE_URL unset; starting without database")
	}

	var ready httpxPinger
	var mountAPI func(chi.Router)
	var mediaGET http.HandlerFunc
	if pool != nil {
		ready = pool
		authAPI := auth.New(cfg, pool)
		circAPI := circles.New(cfg, pool)
		ritAPI := rituals.New(cfg, pool)
		logAPI := logs.New(cfg, pool)
		mediaAPI := media.New(cfg, pool, authAPI.RequestUserID)
		mountAPI = func(r chi.Router) {
			authAPI.Mount(r)
			circAPI.Mount(r)
			ritAPI.Mount(r)
			logAPI.Mount(r)
			mediaAPI.Mount(r)
		}
		mediaGET = mediaAPI.HandleGet
	}

	public := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpx.NewRouter(ui, ready, mountAPI, mediaGET),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      0,
	}
	metrics := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           httpx.MetricsMux(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		slog.Info("http listen", "addr", cfg.HTTPAddr)
		if err := public.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		slog.Info("metrics listen", "addr", cfg.MetricsAddr)
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

type httpxPinger interface {
	Ping(context.Context) error
}
