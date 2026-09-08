package challenges

import (
	"net/http"
	"time"

	_ "time/tzdata"

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
	r.Get("/circles/{id}/challenges", require(a.handleList))
	r.Post("/circles/{id}/challenges", require(a.handleCreate))
	r.Get("/challenges/{id}", require(a.handleGet))
	r.Patch("/challenges/{id}", require(a.handlePatch))
	r.Delete("/challenges/{id}", require(a.handleDelete))
	r.Post("/challenges/{id}/join", require(a.handleJoin))
	r.Post("/challenges/{id}/leave", require(a.handleLeave))
	r.Get("/challenges/{id}/standings", require(a.handleStandings))
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

type challengeOut struct {
	ID               uuid.UUID  `json:"id"`
	CircleID         uuid.UUID  `json:"circle_id"`
	RitualID         *uuid.UUID `json:"ritual_id"`
	Type             string     `json:"type"`
	ScoringKey       string     `json:"scoring_key"`
	Direction        *string    `json:"direction"`
	Name             string     `json:"name"`
	StartsAt         time.Time  `json:"starts_at"`
	EndsAt           time.Time  `json:"ends_at"`
	RequirePhoto     bool       `json:"require_photo"`
	JoinPolicy       string     `json:"join_policy"`
	CreatedAt        time.Time  `json:"created_at"`
	Joined           bool       `json:"joined"`
	ParticipantCount int        `json:"participant_count"`
}

type standingEntry struct {
	UserID      uuid.UUID      `json:"user_id"`
	DisplayName string         `json:"display_name"`
	Points      float64        `json:"points"`
	LastEventAt *time.Time     `json:"last_event_at"`
	Detail      map[string]any `json:"detail,omitempty"`
}

func validType(s string) bool {
	switch s {
	case "weight", "workout", "habit", "fishing", "custom":
		return true
	default:
		return false
	}
}

func validScoringKey(s string) bool {
	switch s {
	case "habit.completion", "fishing.days", "fishing.catches",
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

func scoringKeyOK(typ, key string) bool {
	switch typ {
	case "weight":
		return key == "weight.progress"
	case "workout":
		return key == "workout.volume"
	case "habit":
		return key == "habit.completion"
	case "fishing":
		return key == "fishing.days" || key == "fishing.catches"
	case "custom":
		return key == "custom.sum" || key == "custom.average"
	default:
		return false
	}
}

func canManage(role string) bool {
	return role == "owner" || role == "admin"
}
