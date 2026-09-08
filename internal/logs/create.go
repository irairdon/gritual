package logs

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/httpx"
)

type logFields struct {
	RitualID   *uuid.UUID
	LoggedAt   *time.Time
	Notes      *string
	Visibility *string
}

type weightReq struct {
	KG         *float64   `json:"kg"`
	LB         *float64   `json:"lb"`
	RitualID   *uuid.UUID `json:"ritual_id"`
	LoggedAt   *time.Time `json:"logged_at"`
	Notes      *string    `json:"notes"`
	Visibility *string    `json:"visibility"`
}

type workoutReq struct {
	Title      string     `json:"title"`
	Sets       []setIn    `json:"sets"`
	RitualID   *uuid.UUID `json:"ritual_id"`
	LoggedAt   *time.Time `json:"logged_at"`
	Notes      *string    `json:"notes"`
	Visibility *string    `json:"visibility"`
}

type setIn struct {
	Exercise string   `json:"exercise"`
	Reps     *int     `json:"reps"`
	WeightKG *float64 `json:"weight_kg"`
	RPE      *float64 `json:"rpe"`
	Ordinal  *int     `json:"ordinal"`
}

type habitReq struct {
	Status     string     `json:"status"`
	RitualID   *uuid.UUID `json:"ritual_id"`
	LoggedAt   *time.Time `json:"logged_at"`
	Notes      *string    `json:"notes"`
	Visibility *string    `json:"visibility"`
}

type fishingReq struct {
	WaterBody  *string    `json:"water_body"`
	Lat        *float64   `json:"lat"`
	Lng        *float64   `json:"lng"`
	StartedAt  *time.Time `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at"`
	Catches    []catchIn  `json:"catches"`
	RitualID   *uuid.UUID `json:"ritual_id"`
	LoggedAt   *time.Time `json:"logged_at"`
	Notes      *string    `json:"notes"`
	Visibility *string    `json:"visibility"`
}

type catchIn struct {
	Species  *string  `json:"species"`
	Count    *int     `json:"count"`
	LengthCM *float64 `json:"length_cm"`
	Released *bool    `json:"released"`
}

type customReq struct {
	Value      *float64   `json:"value"`
	Unit       string     `json:"unit"`
	RitualID   *uuid.UUID `json:"ritual_id"`
	LoggedAt   *time.Time `json:"logged_at"`
	Notes      *string    `json:"notes"`
	Visibility *string    `json:"visibility"`
}

func (a *API) handleCreateWeight(w http.ResponseWriter, r *http.Request) {
	var req weightReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	kg, err := resolveKg(req.KG, req.LB)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	out, err := a.insertLog(r.Context(), auth.UserFrom(r.Context()).ID, "weight", logFields{
		RitualID: req.RitualID, LoggedAt: req.LoggedAt, Notes: req.Notes, Visibility: req.Visibility,
	}, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
		_, err := tx.Exec(ctx, `INSERT INTO weight_logs (log_id, kg) VALUES ($1, $2)`, logID, kg)
		return err
	})
	if err != nil {
		writeLogErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleCreateWorkout(w http.ResponseWriter, r *http.Request) {
	var req workoutReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "title required")
		return
	}
	for i, s := range req.Sets {
		if strings.TrimSpace(s.Exercise) == "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "set exercise required")
			return
		}
		if s.Reps != nil && *s.Reps < 0 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "reps must be >= 0")
			return
		}
		if s.WeightKG != nil && *s.WeightKG < 0 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "weight_kg must be >= 0")
			return
		}
		req.Sets[i].Exercise = strings.TrimSpace(s.Exercise)
	}
	out, err := a.insertLog(r.Context(), auth.UserFrom(r.Context()).ID, "workout", logFields{
		RitualID: req.RitualID, LoggedAt: req.LoggedAt, Notes: req.Notes, Visibility: req.Visibility,
	}, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
		if _, err := tx.Exec(ctx, `INSERT INTO workout_logs (log_id, title) VALUES ($1, $2)`, logID, title); err != nil {
			return err
		}
		return insertSets(ctx, tx, logID, req.Sets)
	})
	if err != nil {
		writeLogErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleCreateHabit(w http.ResponseWriter, r *http.Request) {
	var req habitReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	status := strings.TrimSpace(req.Status)
	if status != "done" && status != "skip" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "status must be done or skip")
		return
	}
	out, err := a.insertLog(r.Context(), auth.UserFrom(r.Context()).ID, "habit", logFields{
		RitualID: req.RitualID, LoggedAt: req.LoggedAt, Notes: req.Notes, Visibility: req.Visibility,
	}, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
		_, err := tx.Exec(ctx, `INSERT INTO habit_logs (log_id, status) VALUES ($1, $2)`, logID, status)
		return err
	})
	if err != nil {
		writeLogErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleCreateFishing(w http.ResponseWriter, r *http.Request) {
	var req fishingReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	if err := validateLatLng(req.Lat, req.Lng); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	out, err := a.insertLog(r.Context(), auth.UserFrom(r.Context()).ID, "fishing", logFields{
		RitualID: req.RitualID, LoggedAt: req.LoggedAt, Notes: req.Notes, Visibility: req.Visibility,
	}, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO fishing_logs (log_id, water_body, lat, lng, started_at, ended_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, logID, trimPtr(req.WaterBody), req.Lat, req.Lng, req.StartedAt, req.EndedAt); err != nil {
			return err
		}
		return insertCatches(ctx, tx, logID, req.Catches)
	})
	if err != nil {
		writeLogErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleCreateCustom(w http.ResponseWriter, r *http.Request) {
	var req customReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	if req.Value == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "value required")
		return
	}
	unit := strings.TrimSpace(req.Unit)
	out, err := a.insertLog(r.Context(), auth.UserFrom(r.Context()).ID, "custom", logFields{
		RitualID: req.RitualID, LoggedAt: req.LoggedAt, Notes: req.Notes, Visibility: req.Visibility,
	}, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
		_, err := tx.Exec(ctx, `INSERT INTO custom_logs (log_id, value, unit) VALUES ($1, $2, $3)`, logID, *req.Value, unit)
		return err
	})
	if err != nil {
		writeLogErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) insertLog(ctx context.Context, userID uuid.UUID, typ string, fields logFields, child func(context.Context, pgx.Tx, uuid.UUID) error) (*logOut, error) {
	var ritualCircle *uuid.UUID
	if fields.RitualID != nil {
		ref, err := a.loadRitualRef(ctx, *fields.RitualID, userID)
		if err != nil {
			return nil, err
		}
		if ref.Type != typ {
			return nil, invalid("ritual type mismatch")
		}
		ritualCircle = ref.CircleID
	}

	vis := defaultVisibility(typ, ritualCircle)
	if fields.Visibility != nil {
		v := strings.TrimSpace(*fields.Visibility)
		if !validVisibility(v) {
			return nil, invalid("invalid visibility")
		}
		vis = v
	}
	if vis == "circle" && ritualCircle == nil {
		return nil, invalid("circle visibility requires a circle ritual")
	}

	loggedAt := time.Now().UTC()
	if fields.LoggedAt != nil {
		loggedAt = fields.LoggedAt.UTC()
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// If the author is in overlapping challenges, pin the ritual-specific one, else earliest start.
	challengeID, challengeCircle, err := matchChallenge(ctx, tx, userID, typ, fields.RitualID, loggedAt)
	if err != nil {
		return nil, err
	}
	if challengeID != nil {
		vis = "challenge"
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO logs (user_id, ritual_id, challenge_id, type, logged_at, visibility, notes, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'app')
		RETURNING id
	`, userID, fields.RitualID, challengeID, typ, loggedAt, vis, trimPtr(fields.Notes)).Scan(&id)
	if err != nil {
		return nil, err
	}
	if err := child(ctx, tx, id); err != nil {
		return nil, err
	}
	if vis == "circle" && ritualCircle != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO log_circles (log_id, circle_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, id, *ritualCircle); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO feed_posts (circle_id, user_id, log_id) VALUES ($1, $2, $3)
		`, *ritualCircle, userID, id); err != nil {
			return nil, err
		}
	}
	if challengeCircle != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO log_circles (log_id, circle_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, id, *challengeCircle); err != nil {
			return nil, err
		}
		if vis == "challenge" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO feed_posts (circle_id, user_id, log_id, challenge_id) VALUES ($1, $2, $3, $4)
			`, *challengeCircle, userID, id, challengeID); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a.loadLog(ctx, id, userID)
}

func matchChallenge(ctx context.Context, tx pgx.Tx, userID uuid.UUID, typ string, ritualID *uuid.UUID, loggedAt time.Time) (*uuid.UUID, *uuid.UUID, error) {
	var id, circleID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT c.id, c.circle_id
		FROM challenges c
		JOIN challenge_participants p ON p.challenge_id = c.id AND p.user_id = $1
		WHERE $2 >= c.starts_at AND $2 < c.ends_at
		  AND (
		    (c.ritual_id IS NOT NULL AND c.ritual_id = $3)
		    OR (c.ritual_id IS NULL AND c.type = $4)
		  )
		ORDER BY (c.ritual_id IS NOT NULL) DESC, c.starts_at ASC, c.id ASC
		LIMIT 1
	`, userID, loggedAt, ritualID, typ).Scan(&id, &circleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return &id, &circleID, nil
}

type ritualRef struct {
	ID       uuid.UUID
	Type     string
	CircleID *uuid.UUID
}

func (a *API) loadRitualRef(ctx context.Context, id, userID uuid.UUID) (*ritualRef, error) {
	var ref ritualRef
	var owner *uuid.UUID
	err := a.pool.QueryRow(ctx, `
		SELECT id, type, circle_id, owner_user_id
		FROM rituals
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&ref.ID, &ref.Type, &ref.CircleID, &owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &apiError{status: http.StatusNotFound, code: "not_found", msg: "ritual not found"}
		}
		return nil, err
	}
	if owner != nil && *owner == userID {
		return &ref, nil
	}
	if ref.CircleID == nil {
		return nil, &apiError{status: http.StatusNotFound, code: "not_found", msg: "ritual not found"}
	}
	if _, err := a.circleRole(ctx, *ref.CircleID, userID); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, &apiError{status: http.StatusNotFound, code: "not_found", msg: "ritual not found"}
		}
		return nil, err
	}
	return &ref, nil
}

func insertSets(ctx context.Context, tx pgx.Tx, logID uuid.UUID, sets []setIn) error {
	for i, s := range sets {
		ord := i
		if s.Ordinal != nil {
			ord = *s.Ordinal
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO workout_sets (workout_log_id, exercise, reps, weight_kg, rpe, ordinal)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, logID, s.Exercise, s.Reps, s.WeightKG, s.RPE, ord); err != nil {
			return err
		}
	}
	return nil
}

func insertCatches(ctx context.Context, tx pgx.Tx, logID uuid.UUID, catches []catchIn) error {
	for _, c := range catches {
		count := 1
		if c.Count != nil {
			if *c.Count < 0 {
				return invalid("catch count must be >= 0")
			}
			count = *c.Count
		}
		released := true
		if c.Released != nil {
			released = *c.Released
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO fishing_catches (fishing_log_id, species, count, length_cm, released)
			VALUES ($1, $2, $3, $4, $5)
		`, logID, trimPtr(c.Species), count, c.LengthCM, released); err != nil {
			return err
		}
	}
	return nil
}

func resolveKg(kg, lb *float64) (float64, error) {
	if (kg == nil) == (lb == nil) {
		return 0, errors.New("exactly one of kg or lb")
	}
	var v float64
	if kg != nil {
		v = *kg
	} else {
		v = lbToKg(*lb)
	}
	if v <= 0 || v >= 500 {
		return 0, errors.New("kg must be > 0 and < 500")
	}
	return v, nil
}

func validateLatLng(lat, lng *float64) error {
	if lat != nil && (*lat < -90 || *lat > 90) {
		return errors.New("lat out of range")
	}
	if lng != nil && (*lng < -180 || *lng > 180) {
		return errors.New("lng out of range")
	}
	return nil
}
