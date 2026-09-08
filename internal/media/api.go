package media

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/httpx"
)

// Lookup resolves the caller from a session cookie or bearer token.
type Lookup func(*http.Request) (uuid.UUID, bool)

type API struct {
	cfg    config.Config
	pool   *pgxpool.Pool
	lookup Lookup
}

func New(cfg config.Config, pool *pgxpool.Pool, lookup Lookup) *API {
	if lookup == nil {
		lookup = func(*http.Request) (uuid.UUID, bool) { return uuid.Nil, false }
	}
	return &API{cfg: cfg, pool: pool, lookup: lookup}
}

func (a *API) Mount(r chi.Router) {
	r.Post("/media", a.handleUpload)
}

type objectJSON struct {
	ID          string `json:"id"`
	ContentType string `json:"content_type"`
	Bytes       int    `json:"bytes"`
}

func (a *API) handleUpload(w http.ResponseWriter, r *http.Request) {
	uid, ok := a.lookup(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "file too large or invalid multipart")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "file required")
		return
	}
	defer f.Close()

	encoded, err := transcode(io.LimitReader(f, maxUpload+1))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "not an image")
		return
	}

	sum := sha256.Sum256(encoded)
	// PR path: MEDIA_DIR/{user_id}/{sha256}.jpeg (not the sha256[0:2] backup sketch).
	rel := filepath.Join(uid.String(), hex.EncodeToString(sum[:])+".jpeg")
	dest := filepath.Join(a.cfg.MediaDir, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		slog.Error("media mkdir", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := os.WriteFile(dest, encoded, 0o644); err != nil {
		slog.Error("media write", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	var id uuid.UUID
	var contentType string
	var nbytes int
	err = a.pool.QueryRow(r.Context(), `
		INSERT INTO media_objects (user_id, sha256, content_type, bytes, path)
		VALUES ($1, $2, 'image/jpeg', $3, $4)
		ON CONFLICT (user_id, sha256) DO UPDATE SET path = media_objects.path
		RETURNING id, content_type, bytes
	`, uid, sum[:], len(encoded), rel).Scan(&id, &contentType, &nbytes)
	if err != nil {
		slog.Error("media insert", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, objectJSON{
		ID:          id.String(),
		ContentType: contentType,
		Bytes:       nbytes,
	})
}

func (a *API) HandleGet(w http.ResponseWriter, r *http.Request) {
	// Owner-only in this PR; circle/challenge grants come with logs later.
	uid, ok := a.lookup(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var owner uuid.UUID
	var rel string
	err = a.pool.QueryRow(r.Context(), `
		SELECT user_id, path FROM media_objects WHERE id = $1
	`, id).Scan(&owner, &rel)
	if err != nil || owner != uid {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(a.cfg.MediaDir, rel)
	if !underDir(a.cfg.MediaDir, full) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "image.jpeg", time.Time{}, f)
}

func underDir(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
