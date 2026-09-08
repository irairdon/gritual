package rituals

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/httpx"
)

var errNotFound = errors.New("not found")

type createReq struct {
	Type        string     `json:"type"`
	Title       string     `json:"title"`
	CircleID    *uuid.UUID `json:"circle_id"`
	TargetValue *float64   `json:"target_value"`
	TargetUnit  *string    `json:"target_unit"`
	Direction   string     `json:"direction"`
	Period      string     `json:"period"`
	ScoringKey  *string    `json:"scoring_key"`
}

type patchReq struct {
	Title       *string  `json:"title"`
	TargetValue *float64 `json:"target_value"`
	TargetUnit  *string  `json:"target_unit"`
	Direction   *string  `json:"direction"`
	Period      *string  `json:"period"`
	ScoringKey  *string  `json:"scoring_key"`
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	rows, err := a.pool.Query(r.Context(), `
		SELECT r.id, r.owner_user_id, r.circle_id, r.type, r.title,
		       r.target_value, r.target_unit, r.direction, r.period, r.scoring_key, r.created_at
		FROM rituals r
		WHERE r.deleted_at IS NULL
		  AND (
		    r.owner_user_id = $1
		    OR r.circle_id IN (SELECT circle_id FROM circle_members WHERE user_id = $1)
		  )
		ORDER BY r.created_at DESC
	`, u.ID)
	if err != nil {
		slog.Error("rituals list", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer rows.Close()

	items := make([]ritualOut, 0)
	for rows.Next() {
		var item ritualOut
		if err := rows.Scan(
			&item.ID, &item.OwnerUserID, &item.CircleID, &item.Type, &item.Title,
			&item.TargetValue, &item.TargetUnit, &item.Direction, &item.Period, &item.ScoringKey, &item.CreatedAt,
		); err != nil {
			slog.Error("rituals list scan", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		slog.Error("rituals list rows", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	typ := strings.TrimSpace(req.Type)
	if typ == "strength" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "type must be workout, not strength")
		return
	}
	if !validType(typ) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid type")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "title required")
		return
	}
	direction := strings.TrimSpace(req.Direction)
	if direction == "" {
		direction = "at_least"
	}
	if !validDirection(direction) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid direction")
		return
	}
	period := strings.TrimSpace(req.Period)
	if period == "" {
		period = "none"
	}
	if !validPeriod(period) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid period")
		return
	}
	scoring := defaultScoringKey(typ)
	if req.ScoringKey != nil && strings.TrimSpace(*req.ScoringKey) != "" {
		scoring = strings.TrimSpace(*req.ScoringKey)
	}
	if !validScoringKey(scoring) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid scoring_key")
		return
	}

	u := auth.UserFrom(r.Context())
	if req.CircleID != nil {
		if _, err := a.circleRole(r.Context(), *req.CircleID, u.ID); err != nil {
			if errors.Is(err, errNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
				return
			}
			slog.Error("ritual circle role", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}

	var out ritualOut
	err := a.pool.QueryRow(r.Context(), `
		INSERT INTO rituals (owner_user_id, circle_id, type, title, target_value, target_unit, direction, period, scoring_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, owner_user_id, circle_id, type, title, target_value, target_unit, direction, period, scoring_key, created_at
	`, u.ID, req.CircleID, typ, title, req.TargetValue, trimPtr(req.TargetUnit), direction, period, scoring).Scan(
		&out.ID, &out.OwnerUserID, &out.CircleID, &out.Type, &out.Title,
		&out.TargetValue, &out.TargetUnit, &out.Direction, &out.Period, &out.ScoringKey, &out.CreatedAt,
	)
	if err != nil {
		if isCheckViolation(err) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid")
			return
		}
		slog.Error("ritual insert", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "ritual not found")
		return
	}
	u := auth.UserFrom(r.Context())
	out, err := a.loadRitual(r.Context(), id)
	if err != nil {
		writeRitualErr(w, err)
		return
	}
	if !a.canAccess(r.Context(), out, u.ID) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "ritual not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handlePatch(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "ritual not found")
		return
	}
	var req patchReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	u := auth.UserFrom(r.Context())
	cur, err := a.loadRitual(r.Context(), id)
	if err != nil {
		writeRitualErr(w, err)
		return
	}
	okMut, err := a.canMutate(r.Context(), cur, u.ID)
	if err != nil {
		writeRitualErr(w, err)
		return
	}
	if !okMut {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}

	title := cur.Title
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
		if title == "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "title required")
			return
		}
	}
	targetValue := cur.TargetValue
	if req.TargetValue != nil {
		targetValue = req.TargetValue
	}
	targetUnit := cur.TargetUnit
	if req.TargetUnit != nil {
		targetUnit = trimPtr(req.TargetUnit)
	}
	direction := cur.Direction
	if req.Direction != nil {
		direction = strings.TrimSpace(*req.Direction)
		if !validDirection(direction) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid direction")
			return
		}
	}
	period := cur.Period
	if req.Period != nil {
		period = strings.TrimSpace(*req.Period)
		if !validPeriod(period) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid period")
			return
		}
	}
	scoring := cur.ScoringKey
	if req.ScoringKey != nil {
		scoring = strings.TrimSpace(*req.ScoringKey)
		if !validScoringKey(scoring) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid scoring_key")
			return
		}
	}

	_, err = a.pool.Exec(r.Context(), `
		UPDATE rituals
		SET title = $2, target_value = $3, target_unit = $4, direction = $5, period = $6, scoring_key = $7
		WHERE id = $1 AND deleted_at IS NULL
	`, id, title, targetValue, targetUnit, direction, period, scoring)
	if err != nil {
		if isCheckViolation(err) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid")
			return
		}
		slog.Error("ritual patch", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	out, err := a.loadRitual(r.Context(), id)
	if err != nil {
		writeRitualErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "ritual not found")
		return
	}
	u := auth.UserFrom(r.Context())
	cur, err := a.loadRitual(r.Context(), id)
	if err != nil {
		writeRitualErr(w, err)
		return
	}
	okMut, err := a.canMutate(r.Context(), cur, u.ID)
	if err != nil {
		writeRitualErr(w, err)
		return
	}
	if !okMut {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `
		UPDATE rituals SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		slog.Error("ritual delete", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "ritual not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) loadRitual(ctx context.Context, id uuid.UUID) (*ritualOut, error) {
	var out ritualOut
	err := a.pool.QueryRow(ctx, `
		SELECT id, owner_user_id, circle_id, type, title, target_value, target_unit, direction, period, scoring_key, created_at
		FROM rituals
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(
		&out.ID, &out.OwnerUserID, &out.CircleID, &out.Type, &out.Title,
		&out.TargetValue, &out.TargetUnit, &out.Direction, &out.Period, &out.ScoringKey, &out.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (a *API) canAccess(ctx context.Context, r *ritualOut, userID uuid.UUID) bool {
	if r.OwnerUserID != nil && *r.OwnerUserID == userID {
		return true
	}
	if r.CircleID == nil {
		return false
	}
	_, err := a.circleRole(ctx, *r.CircleID, userID)
	return err == nil
}

func (a *API) canMutate(ctx context.Context, r *ritualOut, userID uuid.UUID) (bool, error) {
	if r.OwnerUserID != nil && *r.OwnerUserID == userID {
		return true, nil
	}
	if r.CircleID == nil {
		return false, nil
	}
	role, err := a.circleRole(ctx, *r.CircleID, userID)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return false, nil
		}
		return false, err
	}
	return role == "owner" || role == "admin", nil
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

func writeRitualErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "ritual not found")
		return
	}
	slog.Error("ritual", "err", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}

func parseUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
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

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
