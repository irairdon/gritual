package meals

import (
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/ai"
	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/media"
)

const handlerTimeout = 30 * time.Second

type API struct {
	cfg    config.Config
	pool   *pgxpool.Pool
	media  *media.API
	vision ai.Provider
	limit  *limiter
}

func New(cfg config.Config, pool *pgxpool.Pool, mediaAPI *media.API, vision ai.Provider) *API {
	return &API{cfg: cfg, pool: pool, media: mediaAPI, vision: vision, limit: newLimiter()}
}

func (a *API) Mount(r chi.Router) {
	r.Post("/meals/photo", require(a.handlePhoto))
	r.Get("/meals/day", require(a.handleDay))
	r.Get("/meals/{id}", require(a.handleGet))
	r.Post("/meals", require(a.handleConfirm))
}

func require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFrom(r.Context()) == nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}
		next(w, r)
	}
}

type mealOut struct {
	ID           uuid.UUID     `json:"id"`
	Status       string        `json:"status"`
	LoggedAt     time.Time     `json:"logged_at"`
	Visibility   string        `json:"visibility"`
	Notes        *string       `json:"notes"`
	Kcal         float64       `json:"kcal"`
	ProteinG     float64       `json:"protein_g"`
	CarbsG       float64       `json:"carbs_g"`
	FatG         float64       `json:"fat_g"`
	Confidence   *float64      `json:"confidence"`
	PhotoMediaID *uuid.UUID    `json:"photo_media_id"`
	Items        []mealItemOut `json:"items"`
}

type mealItemOut struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Grams    *float64  `json:"grams"`
	Kcal     float64   `json:"kcal"`
	ProteinG float64   `json:"protein_g"`
	CarbsG   float64   `json:"carbs_g"`
	FatG     float64   `json:"fat_g"`
	Source   string    `json:"source"`
}

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter {
	return &limiter{hits: make(map[string][]time.Time)}
}

func (l *limiter) pruneLocked(userID string, now time.Time) []time.Time {
	cutoff := now.Add(-time.Hour)
	cur := l.hits[userID][:0]
	for _, t := range l.hits[userID] {
		if t.After(cutoff) {
			cur = append(cur, t)
		}
	}
	if len(cur) == 0 {
		delete(l.hits, userID)
		return nil
	}
	l.hits[userID] = cur
	return cur
}

func (l *limiter) atLimit(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pruneLocked(userID, time.Now())) >= 20
}

func (l *limiter) allow(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cur := l.pruneLocked(userID, now)
	if len(cur) >= 20 {
		return false
	}
	l.hits[userID] = append(cur, now)
	return true
}
