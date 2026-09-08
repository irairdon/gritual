package rituals

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

type API struct {
	cfg  config.Config
	pool *pgxpool.Pool
}

func New(cfg config.Config, pool *pgxpool.Pool) *API {
	return &API{cfg: cfg, pool: pool}
}

func (a *API) Mount(r chi.Router) {
	r.Get("/rituals", require(a.handleList))
	r.Post("/rituals", require(a.handleCreate))
	r.Get("/rituals/{id}", require(a.handleGet))
	r.Patch("/rituals/{id}", require(a.handlePatch))
	r.Delete("/rituals/{id}", require(a.handleDelete))
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

type ritualOut struct {
	ID          uuid.UUID  `json:"id"`
	OwnerUserID *uuid.UUID `json:"owner_user_id"`
	CircleID    *uuid.UUID `json:"circle_id"`
	Type        string     `json:"type"`
	Title       string     `json:"title"`
	TargetValue *float64   `json:"target_value"`
	TargetUnit  *string    `json:"target_unit"`
	Direction   string     `json:"direction"`
	Period      string     `json:"period"`
	ScoringKey  string     `json:"scoring_key"`
	CreatedAt   time.Time  `json:"created_at"`
}

func validType(s string) bool {
	switch s {
	case "weight", "workout", "habit", "fishing", "meal", "custom":
		return true
	default:
		return false
	}
}

func validDirection(s string) bool {
	switch s {
	case "at_least", "at_most", "hit":
		return true
	default:
		return false
	}
}

func validPeriod(s string) bool {
	switch s {
	case "none", "daily", "weekly", "season", "date_range":
		return true
	default:
		return false
	}
}

func validScoringKey(s string) bool {
	switch s {
	case "", "habit.completion", "fishing.days", "fishing.catches",
		"weight.progress", "workout.volume", "custom.sum", "custom.average":
		return true
	default:
		return false
	}
}

func defaultScoringKey(typ string) string {
	switch typ {
	case "weight":
		return "weight.progress"
	case "workout":
		return "workout.volume"
	case "habit":
		return "habit.completion"
	case "fishing":
		return "fishing.days"
	case "custom":
		return "custom.sum"
	default:
		return ""
	}
}
