package httpx

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ctxKey int

const requestIDKey ctxKey = 1

type pinger interface {
	Ping(ctx context.Context) error
}

func NewRouter(ui fs.FS, db pinger) http.Handler {
	r := chi.NewRouter()
	r.Use(requestID)

	r.Get("/healthz", handleHealth)
	r.Get("/readyz", handleReady(db))

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/ping", handlePing)
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, http.StatusNotFound, "not_found", "not found")
		})
	})

	r.Get("/mcp", handleMCP)
	r.Post("/mcp", handleMCP)

	r.Get("/media/{id}", handleMedia)

	r.Get("/.well-known/apple-app-site-association", handleAASA)
	r.Get("/.well-known/assetlinks.json", handleAssetLinks)

	r.NotFound(serveUI(ui))
	return r
}

func MetricsMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "# metrics stub\n")
	})
	return mux
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r.WithContext(ctx))
		slog.Info("request",
			"request_id", id,
			"path", r.URL.Path,
			"status", ww.status,
			"ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func handleReady(db pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hasPinger(db) {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := db.Ping(ctx); err != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, "not ready")
				return
			}
		}
		handleHealth(w, r)
	}
}

func hasPinger(db pinger) bool {
	if db == nil {
		return false
	}
	v := reflect.ValueOf(db)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return !v.IsNil()
	default:
		return true
	}
}

func handlePing(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"ok":true}`)
}

func handleMCP(w http.ResponseWriter, _ *http.Request) {
	WriteError(w, http.StatusNotImplemented, "not_implemented", "mcp not in this PR")
}

func handleMedia(w http.ResponseWriter, _ *http.Request) {
	http.NotFound(w, nil)
}

func handleAASA(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "{}")
}

func handleAssetLinks(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "[]")
}

func serveUI(ui fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}

		clean := path.Clean(r.URL.Path)
		if !strings.HasPrefix(clean, "/") {
			clean = "/" + clean
		}
		rel := strings.TrimPrefix(clean, "/")

		if rel != "" {
			if f, err := ui.Open(rel); err == nil {
				defer f.Close()
				if info, statErr := f.Stat(); statErr == nil && info.IsDir() {
					if path.Ext(clean) == "" {
						writeIndex(w, ui)
						return
					}
					http.NotFound(w, r)
					return
				}
				if strings.HasPrefix(clean, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				writeFSFile(w, rel, f)
				return
			}
		}

		if path.Ext(clean) == "" {
			writeIndex(w, ui)
			return
		}
		http.NotFound(w, r)
	}
}

func writeIndex(w http.ResponseWriter, ui fs.FS) {
	f, err := ui.Open("index.html")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", "no-store")
	writeFSFile(w, "index.html", f)
}

func writeFSFile(w http.ResponseWriter, name string, f fs.File) {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		_, _ = io.Copy(w, rs)
		return
	}
	b, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "read", http.StatusInternalServerError)
		return
	}
	_, _ = io.Copy(w, bytes.NewReader(b))
}
