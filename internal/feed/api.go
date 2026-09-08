package feed

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/httpx"
)

const (
	maxPostBody    = 4000
	maxCommentBody = 2000
	defaultLimit   = 30
	maxLimit       = 100
)

type API struct {
	cfg  config.Config
	pool *pgxpool.Pool
}

func New(cfg config.Config, pool *pgxpool.Pool) *API {
	return &API{cfg: cfg, pool: pool}
}

func (a *API) Mount(r chi.Router) {
	r.Get("/circles/{id}/feed", require(a.handleList))
	r.Post("/circles/{id}/feed", require(a.handleCreate))
	r.Post("/posts/{id}/comments", require(a.handleCreateComment))
	r.Delete("/comments/{id}", require(a.handleDeleteComment))
	r.Put("/posts/{id}/reactions", require(a.handlePutReaction))
	r.Delete("/posts/{id}/reactions", require(a.handleDeleteReaction))
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

type postOut struct {
	ID          uuid.UUID      `json:"id"`
	CircleID    uuid.UUID      `json:"circle_id"`
	UserID      *uuid.UUID     `json:"user_id"`
	DisplayName string         `json:"display_name"`
	LogID       *uuid.UUID     `json:"log_id"`
	ChallengeID *uuid.UUID     `json:"challenge_id"`
	Body        *string        `json:"body"`
	CreatedAt   time.Time      `json:"created_at"`
	Log         *logSnippet    `json:"log,omitempty"`
	Comments    []commentOut   `json:"comments"`
	Reactions   map[string]int `json:"reactions"`
	MyReaction  *string        `json:"my_reaction"`
}

type logSnippet struct {
	ID         uuid.UUID `json:"id"`
	Type       string    `json:"type"`
	LoggedAt   time.Time `json:"logged_at"`
	Visibility string    `json:"visibility"`
	Notes      *string   `json:"notes"`
}

type commentOut struct {
	ID          uuid.UUID  `json:"id"`
	PostID      uuid.UUID  `json:"post_id"`
	UserID      *uuid.UUID `json:"user_id"`
	DisplayName string     `json:"display_name"`
	Body        string     `json:"body"`
	CreatedAt   time.Time  `json:"created_at"`
}

func emptyReactions() map[string]int {
	return map[string]int{"like": 0, "fire": 0, "fish": 0, "strong": 0, "heart": 0}
}

func validReaction(s string) bool {
	switch s {
	case "like", "fire", "fish", "strong", "heart":
		return true
	default:
		return false
	}
}

func canManage(role string) bool {
	return role == "owner" || role == "admin"
}
