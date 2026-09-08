package challenges

import (
	"context"
	"errors"
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

type createReq struct {
	Name              string     `json:"name"`
	Type              string     `json:"type"`
	ScoringKey        *string    `json:"scoring_key"`
	Direction         *string    `json:"direction"`
	RitualID          *uuid.UUID `json:"ritual_id"`
	StartsAt          time.Time  `json:"starts_at"`
	EndsAt            time.Time  `json:"ends_at"`
	RequirePhoto      *bool      `json:"require_photo"`
	JoinPolicy        string     `json:"join_policy"`
	ShareMatchingLogs *bool      `json:"share_matching_logs"`
}

type patchReq struct {
	Name         *string    `json:"name"`
	StartsAt     *time.Time `json:"starts_at"`
	EndsAt       *time.Time `json:"ends_at"`
	RequirePhoto *bool      `json:"require_photo"`
}

type joinReq struct {
	ShareMatchingLogs *bool `json:"share_matching_logs"`
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	circleID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	u := auth.UserFrom(r.Context())
	if _, err := a.circleRole(r.Context(), circleID, u.ID); err != nil {
		writeErr(w, err)
		return
	}
	rows, err := a.pool.Query(r.Context(), `
		SELECT c.id, c.circle_id, c.ritual_id, c.type, c.scoring_key, c.direction, c.name,
		       c.starts_at, c.ends_at, c.require_photo, c.join_policy, c.created_at,
		       EXISTS(SELECT 1 FROM challenge_participants p WHERE p.challenge_id = c.id AND p.user_id = $2),
		       (SELECT COUNT(*)::int FROM challenge_participants p WHERE p.challenge_id = c.id)
		FROM challenges c
		WHERE c.circle_id = $1
		ORDER BY c.starts_at DESC, c.id
	`, circleID, u.ID)
	if err != nil {
		slog.Error("challenges list", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer rows.Close()
	items := make([]challengeOut, 0)
	for rows.Next() {
		var c challengeOut
		if err := rows.Scan(&c.ID, &c.CircleID, &c.RitualID, &c.Type, &c.ScoringKey, &c.Direction, &c.Name,
			&c.StartsAt, &c.EndsAt, &c.RequirePhoto, &c.JoinPolicy, &c.CreatedAt, &c.Joined, &c.ParticipantCount); err != nil {
			slog.Error("challenges list scan", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		slog.Error("challenges list rows", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	circleID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	var req createReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	u := auth.UserFrom(r.Context())
	if _, err := a.circleRole(r.Context(), circleID, u.ID); err != nil {
		writeErr(w, err)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}
	typ := strings.TrimSpace(req.Type)
	if typ == "meal" || typ == "strength" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid type")
		return
	}
	if !validType(typ) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid type")
		return
	}
	scoring := defaultScoringKey(typ)
	if req.ScoringKey != nil && strings.TrimSpace(*req.ScoringKey) != "" {
		scoring = strings.TrimSpace(*req.ScoringKey)
	}
	if !validScoringKey(scoring) || !scoringKeyOK(typ, scoring) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid scoring_key")
		return
	}
	jp := strings.TrimSpace(req.JoinPolicy)
	if jp != "" && jp != "opt_in" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "join_policy must be opt_in")
		return
	}

	var direction *string
	if scoring == "weight.progress" {
		d := ""
		if req.Direction != nil {
			d = strings.TrimSpace(*req.Direction)
		}
		if d == "hit" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "weight.progress rejects direction hit")
			return
		}
		if d != "at_most" && d != "at_least" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "direction required for weight.progress")
			return
		}
		direction = &d
	} else if req.Direction != nil && strings.TrimSpace(*req.Direction) != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "direction only valid for weight.progress")
		return
	}

	if req.StartsAt.IsZero() || req.EndsAt.IsZero() {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "starts_at and ends_at required")
		return
	}
	if !req.EndsAt.After(req.StartsAt) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "ends_at must be after starts_at")
		return
	}

	if req.RitualID != nil {
		var rType, rDir string
		var rCircle *uuid.UUID
		var deleted *time.Time
		err := a.pool.QueryRow(r.Context(), `
			SELECT type, direction, circle_id, deleted_at FROM rituals WHERE id = $1
		`, *req.RitualID).Scan(&rType, &rDir, &rCircle, &deleted)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				httpx.WriteError(w, http.StatusBadRequest, "invalid", "ritual not found")
				return
			}
			slog.Error("challenge ritual", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		if deleted != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "ritual not found")
			return
		}
		if rType != typ {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "ritual type mismatch")
			return
		}
		if rCircle != nil && *rCircle != circleID {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "ritual not in this circle")
			return
		}
		if direction != nil && rDir != *direction {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "direction must match ritual")
			return
		}
	}

	requirePhoto := false
	if req.RequirePhoto != nil {
		requirePhoto = *req.RequirePhoto
	}

	ctx := r.Context()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out challengeOut
	err = tx.QueryRow(ctx, `
		INSERT INTO challenges (circle_id, ritual_id, type, scoring_key, direction, name, starts_at, ends_at, require_photo, join_policy)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'opt_in')
		RETURNING id, circle_id, ritual_id, type, scoring_key, direction, name, starts_at, ends_at, require_photo, join_policy, created_at
	`, circleID, req.RitualID, typ, scoring, direction, name, req.StartsAt.UTC(), req.EndsAt.UTC(), requirePhoto).Scan(
		&out.ID, &out.CircleID, &out.RitualID, &out.Type, &out.ScoringKey, &out.Direction, &out.Name,
		&out.StartsAt, &out.EndsAt, &out.RequirePhoto, &out.JoinPolicy, &out.CreatedAt,
	)
	if err != nil {
		if isCheckViolation(err) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid")
			return
		}
		slog.Error("challenge insert", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	// Creator enrolls only via the same grant as join; never silent-insert other members.
	if req.ShareMatchingLogs != nil && *req.ShareMatchingLogs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO challenge_participants (challenge_id, user_id, share_matching_logs)
			VALUES ($1, $2, true)
		`, out.ID, u.ID); err != nil {
			slog.Error("challenge enroll", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		out.Joined = true
		out.ParticipantCount = 1
	}

	if err := tx.Commit(ctx); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "challenge not found")
		return
	}
	out, err := a.loadChallenge(r.Context(), id, auth.UserFrom(r.Context()).ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handlePatch(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "challenge not found")
		return
	}
	var req patchReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	u := auth.UserFrom(r.Context())
	out, err := a.loadChallenge(r.Context(), id, u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	role, err := a.circleRole(r.Context(), out.CircleID, u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !canManage(role) {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	name := out.Name
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "name required")
			return
		}
	}
	starts, ends := out.StartsAt, out.EndsAt
	if req.StartsAt != nil {
		starts = req.StartsAt.UTC()
	}
	if req.EndsAt != nil {
		ends = req.EndsAt.UTC()
	}
	if !ends.After(starts) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "ends_at must be after starts_at")
		return
	}
	requirePhoto := out.RequirePhoto
	if req.RequirePhoto != nil {
		requirePhoto = *req.RequirePhoto
	}
	err = a.pool.QueryRow(r.Context(), `
		UPDATE challenges SET name = $2, starts_at = $3, ends_at = $4, require_photo = $5
		WHERE id = $1
		RETURNING id, circle_id, ritual_id, type, scoring_key, direction, name, starts_at, ends_at, require_photo, join_policy, created_at
	`, id, name, starts, ends, requirePhoto).Scan(
		&out.ID, &out.CircleID, &out.RitualID, &out.Type, &out.ScoringKey, &out.Direction, &out.Name,
		&out.StartsAt, &out.EndsAt, &out.RequirePhoto, &out.JoinPolicy, &out.CreatedAt,
	)
	if err != nil {
		if isCheckViolation(err) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid")
			return
		}
		slog.Error("challenge patch", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "challenge not found")
		return
	}
	u := auth.UserFrom(r.Context())
	out, err := a.loadChallenge(r.Context(), id, u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	role, err := a.circleRole(r.Context(), out.CircleID, u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !canManage(role) {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	ctx := r.Context()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE logs SET challenge_id = NULL WHERE challenge_id = $1`, id); err != nil {
		slog.Error("challenge delete logs", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE feed_posts SET challenge_id = NULL WHERE challenge_id = $1`, id); err != nil {
		slog.Error("challenge delete feed", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM challenge_participants WHERE challenge_id = $1`, id); err != nil {
		slog.Error("challenge delete participants", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM challenges WHERE id = $1`, id); err != nil {
		slog.Error("challenge delete", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleJoin(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "challenge not found")
		return
	}
	var req joinReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	if req.ShareMatchingLogs == nil || !*req.ShareMatchingLogs {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "share_matching_logs must be true")
		return
	}
	u := auth.UserFrom(r.Context())
	out, err := a.loadChallenge(r.Context(), id, u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	_, err = a.pool.Exec(r.Context(), `
		INSERT INTO challenge_participants (challenge_id, user_id, share_matching_logs)
		VALUES ($1, $2, true)
	`, id, u.ID)
	if err != nil {
		if isUniqueViolation(err) {
			httpx.WriteError(w, http.StatusConflict, "conflict", "already joined")
			return
		}
		slog.Error("challenge join", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	out.Joined = true
	out.ParticipantCount++
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handleLeave(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "challenge not found")
		return
	}
	u := auth.UserFrom(r.Context())
	if _, err := a.loadChallenge(r.Context(), id, u.ID); err != nil {
		writeErr(w, err)
		return
	}
	tag, err := a.pool.Exec(r.Context(), `
		DELETE FROM challenge_participants WHERE challenge_id = $1 AND user_id = $2
	`, id, u.ID)
	if err != nil {
		slog.Error("challenge leave", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "not a participant")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) loadChallenge(ctx context.Context, id, userID uuid.UUID) (*challengeOut, error) {
	var c challengeOut
	err := a.pool.QueryRow(ctx, `
		SELECT c.id, c.circle_id, c.ritual_id, c.type, c.scoring_key, c.direction, c.name,
		       c.starts_at, c.ends_at, c.require_photo, c.join_policy, c.created_at,
		       EXISTS(SELECT 1 FROM challenge_participants p WHERE p.challenge_id = c.id AND p.user_id = $2),
		       (SELECT COUNT(*)::int FROM challenge_participants p WHERE p.challenge_id = c.id)
		FROM challenges c
		JOIN circles circ ON circ.id = c.circle_id AND circ.deleted_at IS NULL
		JOIN circle_members m ON m.circle_id = c.circle_id AND m.user_id = $2
		WHERE c.id = $1
	`, id, userID).Scan(
		&c.ID, &c.CircleID, &c.RitualID, &c.Type, &c.ScoringKey, &c.Direction, &c.Name,
		&c.StartsAt, &c.EndsAt, &c.RequirePhoto, &c.JoinPolicy, &c.CreatedAt, &c.Joined, &c.ParticipantCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &c, nil
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

func writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	slog.Error("challenge", "err", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}

func parseUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
