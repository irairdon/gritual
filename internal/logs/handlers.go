package logs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/httpx"
)

var errNotFound = errors.New("not found")

type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func invalid(msg string) error {
	return &apiError{status: http.StatusBadRequest, code: "invalid", msg: msg}
}

type patchLogReq struct {
	Notes      *string     `json:"notes"`
	LoggedAt   *time.Time  `json:"logged_at"`
	Visibility *string     `json:"visibility"`
	Weight     *weightReq  `json:"weight"`
	Workout    *workoutReq `json:"workout"`
	Habit      *habitReq   `json:"habit"`
	Fishing    *fishingReq `json:"fishing"`
	Custom     *customReq  `json:"custom"`
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	q := r.URL.Query()

	sql := `
		SELECT l.id, l.user_id, l.ritual_id, l.challenge_id, l.type, l.logged_at,
		       l.visibility, l.notes, l.source, l.created_at, l.updated_at
		FROM logs l
		LEFT JOIN meals m ON m.log_id = l.id
		WHERE l.user_id = $1
		  AND l.deleted_at IS NULL
		  AND (m.status IS NULL OR m.status = 'confirmed')`
	args := []any{u.ID}
	n := 1

	if typ := strings.TrimSpace(q.Get("type")); typ != "" {
		if typ == "strength" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "type must be workout, not strength")
			return
		}
		if !validLogType(typ) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid type")
			return
		}
		n++
		sql += fmt.Sprintf(" AND l.type = $%d", n)
		args = append(args, typ)
	}
	if raw := strings.TrimSpace(q.Get("from")); raw != "" {
		t, ok := parseTime(raw)
		if !ok {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid from")
			return
		}
		n++
		sql += fmt.Sprintf(" AND l.logged_at >= $%d", n)
		args = append(args, t)
	}
	if raw := strings.TrimSpace(q.Get("to")); raw != "" {
		t, ok := parseTime(raw)
		if !ok {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid to")
			return
		}
		n++
		sql += fmt.Sprintf(" AND l.logged_at < $%d", n)
		args = append(args, t)
	}
	if raw := strings.TrimSpace(q.Get("circle_id")); raw != "" {
		cid, ok := parseUUID(raw)
		if !ok {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid circle_id")
			return
		}
		n++
		sql += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM log_circles lc WHERE lc.log_id = l.id AND lc.circle_id = $%d)", n)
		args = append(args, cid)
	}
	sql += " ORDER BY l.logged_at DESC, l.id DESC LIMIT 100"

	rows, err := a.pool.Query(r.Context(), sql, args...)
	if err != nil {
		slog.Error("logs list", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	items, err := scanLogs(rows)
	if err != nil {
		slog.Error("logs list scan", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := a.hydrate(r.Context(), items); err != nil {
		slog.Error("logs hydrate", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "log not found")
		return
	}
	out, err := a.loadLog(r.Context(), id, auth.UserFrom(r.Context()).ID)
	if err != nil {
		writeLogErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handlePatch(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "log not found")
		return
	}
	var req patchLogReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	u := auth.UserFrom(r.Context())
	cur, err := a.loadLog(r.Context(), id, u.ID)
	if err != nil {
		writeLogErr(w, err)
		return
	}

	ctx := r.Context()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	loggedAt := cur.LoggedAt
	if req.LoggedAt != nil {
		loggedAt = req.LoggedAt.UTC()
	}
	notes := cur.Notes
	if req.Notes != nil {
		notes = trimPtr(req.Notes)
	}
	vis := cur.Visibility
	if req.Visibility != nil {
		v := strings.TrimSpace(*req.Visibility)
		if !validVisibility(v) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid visibility")
			return
		}
		vis = v
	}
	if vis == "circle" {
		circleID, err := a.ritualCircle(ctx, cur.RitualID)
		if err != nil {
			writeLogErr(w, err)
			return
		}
		if circleID == nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "circle visibility requires a circle ritual")
			return
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO log_circles (log_id, circle_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, id, *circleID); err != nil {
			slog.Error("log_circles insert", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	} else if req.Visibility != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM log_circles WHERE log_id = $1`, id); err != nil {
			slog.Error("log_circles delete", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE logs SET logged_at = $2, notes = $3, visibility = $4, updated_at = now()
		WHERE id = $1 AND user_id = $5 AND deleted_at IS NULL
	`, id, loggedAt, notes, vis, u.ID); err != nil {
		writeLogErr(w, err)
		return
	}

	if err := a.patchTyped(ctx, tx, cur, req); err != nil {
		writeLogErr(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	out, err := a.loadLog(ctx, id, u.ID)
	if err != nil {
		writeLogErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) patchTyped(ctx context.Context, tx pgx.Tx, cur *logOut, req patchLogReq) error {
	switch cur.Type {
	case "weight":
		if req.Weight == nil {
			return nil
		}
		kg, err := resolveKg(req.Weight.KG, req.Weight.LB)
		if err != nil {
			return invalid(err.Error())
		}
		_, err = tx.Exec(ctx, `UPDATE weight_logs SET kg = $2 WHERE log_id = $1`, cur.ID, kg)
		return err
	case "habit":
		if req.Habit == nil {
			return nil
		}
		status := strings.TrimSpace(req.Habit.Status)
		if status != "done" && status != "skip" {
			return invalid("status must be done or skip")
		}
		_, err := tx.Exec(ctx, `UPDATE habit_logs SET status = $2 WHERE log_id = $1`, cur.ID, status)
		return err
	case "custom":
		if req.Custom == nil {
			return nil
		}
		if req.Custom.Value == nil {
			return invalid("value required")
		}
		_, err := tx.Exec(ctx, `UPDATE custom_logs SET value = $2, unit = $3 WHERE log_id = $1`, cur.ID, *req.Custom.Value, strings.TrimSpace(req.Custom.Unit))
		return err
	case "workout":
		if req.Workout == nil {
			return nil
		}
		title := strings.TrimSpace(req.Workout.Title)
		if title == "" {
			return invalid("title required")
		}
		if _, err := tx.Exec(ctx, `UPDATE workout_logs SET title = $2 WHERE log_id = $1`, cur.ID, title); err != nil {
			return err
		}
		if req.Workout.Sets != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM workout_sets WHERE workout_log_id = $1`, cur.ID); err != nil {
				return err
			}
			return insertSets(ctx, tx, cur.ID, req.Workout.Sets)
		}
		return nil
	case "fishing":
		if req.Fishing == nil {
			return nil
		}
		if err := validateLatLng(req.Fishing.Lat, req.Fishing.Lng); err != nil {
			return invalid(err.Error())
		}
		if _, err := tx.Exec(ctx, `
			UPDATE fishing_logs SET water_body = $2, lat = $3, lng = $4, started_at = $5, ended_at = $6
			WHERE log_id = $1
		`, cur.ID, trimPtr(req.Fishing.WaterBody), req.Fishing.Lat, req.Fishing.Lng, req.Fishing.StartedAt, req.Fishing.EndedAt); err != nil {
			return err
		}
		if req.Fishing.Catches != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM fishing_catches WHERE fishing_log_id = $1`, cur.ID); err != nil {
				return err
			}
			return insertCatches(ctx, tx, cur.ID, req.Fishing.Catches)
		}
		return nil
	}
	return nil
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "log not found")
		return
	}
	u := auth.UserFrom(r.Context())
	tag, err := a.pool.Exec(r.Context(), `
		UPDATE logs SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`, id, u.ID)
	if err != nil {
		slog.Error("log delete", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "log not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) loadLog(ctx context.Context, id, userID uuid.UUID) (*logOut, error) {
	var out logOut
	err := a.pool.QueryRow(ctx, `
		SELECT l.id, l.user_id, l.ritual_id, l.challenge_id, l.type, l.logged_at,
		       l.visibility, l.notes, l.source, l.created_at, l.updated_at
		FROM logs l
		LEFT JOIN meals m ON m.log_id = l.id
		WHERE l.id = $1 AND l.user_id = $2 AND l.deleted_at IS NULL
		  AND (m.status IS NULL OR m.status = 'confirmed')
	`, id, userID).Scan(
		&out.ID, &out.UserID, &out.RitualID, &out.ChallengeID, &out.Type, &out.LoggedAt,
		&out.Visibility, &out.Notes, &out.Source, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	items := []logOut{out}
	if err := a.hydrate(ctx, items); err != nil {
		return nil, err
	}
	return &items[0], nil
}

func scanLogs(rows pgx.Rows) ([]logOut, error) {
	defer rows.Close()
	items := make([]logOut, 0)
	for rows.Next() {
		var item logOut
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.RitualID, &item.ChallengeID, &item.Type, &item.LoggedAt,
			&item.Visibility, &item.Notes, &item.Source, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *API) hydrate(ctx context.Context, items []logOut) error {
	if len(items) == 0 {
		return nil
	}
	idx := make(map[uuid.UUID]int, len(items))
	var weightIDs, workoutIDs, habitIDs, fishingIDs, customIDs, mealIDs []uuid.UUID
	for i := range items {
		idx[items[i].ID] = i
		switch items[i].Type {
		case "weight":
			weightIDs = append(weightIDs, items[i].ID)
		case "workout":
			workoutIDs = append(workoutIDs, items[i].ID)
		case "habit":
			habitIDs = append(habitIDs, items[i].ID)
		case "fishing":
			fishingIDs = append(fishingIDs, items[i].ID)
		case "custom":
			customIDs = append(customIDs, items[i].ID)
		case "meal":
			mealIDs = append(mealIDs, items[i].ID)
		}
	}
	if err := a.hydrateWeights(ctx, items, idx, weightIDs); err != nil {
		return err
	}
	if err := a.hydrateWorkouts(ctx, items, idx, workoutIDs); err != nil {
		return err
	}
	if err := a.hydrateHabits(ctx, items, idx, habitIDs); err != nil {
		return err
	}
	if err := a.hydrateFishing(ctx, items, idx, fishingIDs); err != nil {
		return err
	}
	if err := a.hydrateCustoms(ctx, items, idx, customIDs); err != nil {
		return err
	}
	return a.hydrateMeals(ctx, items, idx, mealIDs)
}

func (a *API) hydrateMeals(ctx context.Context, items []logOut, idx map[uuid.UUID]int, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := a.pool.Query(ctx, `SELECT log_id, status, kcal, protein_g, carbs_g, fat_g FROM meals WHERE log_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var m mealLogOut
		if err := rows.Scan(&id, &m.Status, &m.Kcal, &m.ProteinG, &m.CarbsG, &m.FatG); err != nil {
			return err
		}
		items[idx[id]].Meal = &m
	}
	return rows.Err()
}

func (a *API) hydrateWeights(ctx context.Context, items []logOut, idx map[uuid.UUID]int, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := a.pool.Query(ctx, `SELECT log_id, kg FROM weight_logs WHERE log_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var kg float64
		if err := rows.Scan(&id, &kg); err != nil {
			return err
		}
		items[idx[id]].Weight = &weightOut{KG: kg, LB: kgToLb(kg)}
	}
	return rows.Err()
}

func (a *API) hydrateWorkouts(ctx context.Context, items []logOut, idx map[uuid.UUID]int, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := a.pool.Query(ctx, `SELECT log_id, title FROM workout_logs WHERE log_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			rows.Close()
			return err
		}
		wo := &workoutOut{Title: title, Sets: []setOut{}}
		items[idx[id]].Workout = wo
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	srows, err := a.pool.Query(ctx, `
		SELECT id, workout_log_id, exercise, reps, weight_kg, rpe, ordinal
		FROM workout_sets WHERE workout_log_id = ANY($1)
		ORDER BY ordinal, id
	`, ids)
	if err != nil {
		return err
	}
	defer srows.Close()
	for srows.Next() {
		var s setOut
		var wid uuid.UUID
		if err := srows.Scan(&s.ID, &wid, &s.Exercise, &s.Reps, &s.WeightKG, &s.RPE, &s.Ordinal); err != nil {
			return err
		}
		if wo := items[idx[wid]].Workout; wo != nil {
			wo.Sets = append(wo.Sets, s)
		}
	}
	return srows.Err()
}

func (a *API) hydrateHabits(ctx context.Context, items []logOut, idx map[uuid.UUID]int, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := a.pool.Query(ctx, `SELECT log_id, status FROM habit_logs WHERE log_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			return err
		}
		items[idx[id]].Habit = &habitOut{Status: status}
	}
	return rows.Err()
}

func (a *API) hydrateFishing(ctx context.Context, items []logOut, idx map[uuid.UUID]int, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := a.pool.Query(ctx, `
		SELECT log_id, water_body, lat, lng, started_at, ended_at
		FROM fishing_logs WHERE log_id = ANY($1)
	`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		var f fishingOut
		f.Catches = []catchOut{}
		if err := rows.Scan(&id, &f.WaterBody, &f.Lat, &f.Lng, &f.StartedAt, &f.EndedAt); err != nil {
			rows.Close()
			return err
		}
		items[idx[id]].Fishing = &f
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	crows, err := a.pool.Query(ctx, `
		SELECT id, fishing_log_id, species, count, length_cm, released
		FROM fishing_catches WHERE fishing_log_id = ANY($1)
		ORDER BY id
	`, ids)
	if err != nil {
		return err
	}
	defer crows.Close()
	for crows.Next() {
		var c catchOut
		var fid uuid.UUID
		if err := crows.Scan(&c.ID, &fid, &c.Species, &c.Count, &c.LengthCM, &c.Released); err != nil {
			return err
		}
		if f := items[idx[fid]].Fishing; f != nil {
			f.Catches = append(f.Catches, c)
		}
	}
	return crows.Err()
}

func (a *API) hydrateCustoms(ctx context.Context, items []logOut, idx map[uuid.UUID]int, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := a.pool.Query(ctx, `SELECT log_id, value, unit FROM custom_logs WHERE log_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var c customOut
		if err := rows.Scan(&id, &c.Value, &c.Unit); err != nil {
			return err
		}
		items[idx[id]].Custom = &c
	}
	return rows.Err()
}

func (a *API) ritualCircle(ctx context.Context, ritualID *uuid.UUID) (*uuid.UUID, error) {
	if ritualID == nil {
		return nil, nil
	}
	var circleID *uuid.UUID
	err := a.pool.QueryRow(ctx, `
		SELECT circle_id FROM rituals WHERE id = $1 AND deleted_at IS NULL
	`, *ritualID).Scan(&circleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return circleID, nil
}

func (a *API) circleRole(ctx context.Context, circleID, userID uuid.UUID) (string, error) {
	var role string
	err := a.pool.QueryRow(ctx, `
		SELECT m.role
		FROM circle_members m
		JOIN circles c ON c.id = m.circle_id AND c.deleted_at IS NULL
		WHERE m.circle_id = $1 AND m.user_id = $2
	`, circleID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", err
	}
	return role, nil
}

func writeLogErr(w http.ResponseWriter, err error) {
	var api *apiError
	if errors.As(err, &api) {
		httpx.WriteError(w, api.status, api.code, api.msg)
		return
	}
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "log not found")
		return
	}
	if isCheckViolation(err) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid")
		return
	}
	slog.Error("logs", "err", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

func parseUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func parseTime(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}
