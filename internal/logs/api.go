package logs

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

// lb = kg * 2.2046226218; kg is canonical storage.
const lbPerKg = 2.2046226218

type API struct {
	cfg  config.Config
	pool *pgxpool.Pool
}

func New(cfg config.Config, pool *pgxpool.Pool) *API {
	return &API{cfg: cfg, pool: pool}
}

func (a *API) Mount(r chi.Router) {
	r.Get("/logs", require(a.handleList))
	r.Get("/logs/{id}", require(a.handleGet))
	r.Patch("/logs/{id}", require(a.handlePatch))
	r.Delete("/logs/{id}", require(a.handleDelete))
	r.Post("/weights", require(a.handleCreateWeight))
	r.Post("/workouts", require(a.handleCreateWorkout))
	r.Post("/habits", require(a.handleCreateHabit))
	r.Post("/fishing", require(a.handleCreateFishing))
	r.Post("/customs", require(a.handleCreateCustom))
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

type logOut struct {
	ID          uuid.UUID   `json:"id"`
	UserID      uuid.UUID   `json:"user_id"`
	RitualID    *uuid.UUID  `json:"ritual_id"`
	ChallengeID *uuid.UUID  `json:"challenge_id"`
	Type        string      `json:"type"`
	LoggedAt    time.Time   `json:"logged_at"`
	Visibility  string      `json:"visibility"`
	Notes       *string     `json:"notes"`
	Source      string      `json:"source"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Weight      *weightOut  `json:"weight,omitempty"`
	Workout     *workoutOut `json:"workout,omitempty"`
	Habit       *habitOut   `json:"habit,omitempty"`
	Fishing     *fishingOut `json:"fishing,omitempty"`
	Custom      *customOut  `json:"custom,omitempty"`
}

type weightOut struct {
	KG float64 `json:"kg"`
	LB float64 `json:"lb"`
}

type workoutOut struct {
	Title string   `json:"title"`
	Sets  []setOut `json:"sets"`
}

type setOut struct {
	ID       uuid.UUID `json:"id"`
	Exercise string    `json:"exercise"`
	Reps     *int      `json:"reps"`
	WeightKG *float64  `json:"weight_kg"`
	RPE      *float64  `json:"rpe"`
	Ordinal  int       `json:"ordinal"`
}

type habitOut struct {
	Status string `json:"status"`
}

type fishingOut struct {
	WaterBody *string    `json:"water_body"`
	Lat       *float64   `json:"lat"`
	Lng       *float64   `json:"lng"`
	StartedAt *time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	Catches   []catchOut `json:"catches"`
}

type catchOut struct {
	ID       uuid.UUID `json:"id"`
	Species  *string   `json:"species"`
	Count    int       `json:"count"`
	LengthCM *float64  `json:"length_cm"`
	Released bool      `json:"released"`
}

type customOut struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

func validLogType(s string) bool {
	switch s {
	case "weight", "workout", "habit", "fishing", "meal", "custom":
		return true
	default:
		return false
	}
}

func validVisibility(s string) bool {
	switch s {
	case "private", "circle":
		return true
	default:
		return false
	}
}

func defaultVisibility(typ string, ritualCircleID *uuid.UUID) string {
	switch typ {
	case "weight", "meal":
		return "private"
	default:
		if ritualCircleID != nil {
			return "circle"
		}
		return "private"
	}
}

func kgToLb(kg float64) float64 {
	return kg * lbPerKg
}

func lbToKg(lb float64) float64 {
	return lb / lbPerKg
}
