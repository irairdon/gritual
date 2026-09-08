package circles

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

type createReq struct {
	Name  string  `json:"name"`
	Emoji *string `json:"emoji"`
	TZ    string  `json:"tz"`
}

type patchReq struct {
	Name  *string `json:"name"`
	Emoji *string `json:"emoji"`
	TZ    *string `json:"tz"`
}

type transferReq struct {
	UserID string `json:"user_id"`
}

type createInviteReq struct {
	MaxUses *int `json:"max_uses"`
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	rows, err := a.pool.Query(r.Context(), `
		SELECT c.id, c.name, c.emoji, c.tz, m.role,
		       (SELECT COUNT(*)::int FROM circle_members cm WHERE cm.circle_id = c.id)
		FROM circles c
		JOIN circle_members m ON m.circle_id = c.id
		WHERE m.user_id = $1 AND c.deleted_at IS NULL
		ORDER BY c.created_at DESC
	`, u.ID)
	if err != nil {
		slog.Error("circles list", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer rows.Close()

	items := make([]circleOut, 0)
	for rows.Next() {
		var c circleOut
		if err := rows.Scan(&c.ID, &c.Name, &c.Emoji, &c.TZ, &c.Role, &c.MemberCount); err != nil {
			slog.Error("circles list scan", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		slog.Error("circles list rows", "err", err)
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
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}
	tz := strings.TrimSpace(req.TZ)
	if tz == "" {
		tz = defaultTZ
	}
	if !validTZ(tz) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid tz")
		return
	}
	emoji := trimPtr(req.Emoji)

	u := auth.UserFrom(r.Context())
	ctx := r.Context()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out circleOut
	err = tx.QueryRow(ctx, `
		INSERT INTO circles (name, emoji, tz, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, emoji, tz
	`, name, emoji, tz, u.ID).Scan(&out.ID, &out.Name, &out.Emoji, &out.TZ)
	if err != nil {
		slog.Error("circle insert", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO circle_members (circle_id, user_id, role)
		VALUES ($1, $2, $3)
	`, out.ID, u.ID, roleOwner); err != nil {
		slog.Error("circle owner insert", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	out.Role = roleOwner
	out.MemberCount = 1
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	out, err := a.loadCircle(r.Context(), id, auth.UserFrom(r.Context()).ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handlePatch(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	var req patchReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	u := auth.UserFrom(r.Context())
	ctx := r.Context()

	role, err := a.memberRole(ctx, id, u.ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	if !canManage(role) {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "name required")
			return
		}
		if _, err := a.pool.Exec(ctx, `UPDATE circles SET name = $2, updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id, name); err != nil {
			slog.Error("circle patch name", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}
	if req.Emoji != nil {
		emoji := trimPtr(req.Emoji)
		if _, err := a.pool.Exec(ctx, `UPDATE circles SET emoji = $2, updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id, emoji); err != nil {
			slog.Error("circle patch emoji", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}
	if req.TZ != nil {
		tz := strings.TrimSpace(*req.TZ)
		if !validTZ(tz) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid tz")
			return
		}
		if _, err := a.pool.Exec(ctx, `UPDATE circles SET tz = $2, updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id, tz); err != nil {
			slog.Error("circle patch tz", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}

	out, err := a.loadCircle(ctx, id, u.ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	u := auth.UserFrom(r.Context())
	role, err := a.memberRole(r.Context(), id, u.ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	if role != roleOwner {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `
		UPDATE circles SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		slog.Error("circle delete", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	if _, err := a.memberRole(r.Context(), id, auth.UserFrom(r.Context()).ID); err != nil {
		writeCircleErr(w, err)
		return
	}
	rows, err := a.pool.Query(r.Context(), `
		SELECT m.user_id, u.display_name, m.role, m.joined_at
		FROM circle_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.circle_id = $1 AND u.deleted_at IS NULL
		ORDER BY m.joined_at ASC
	`, id)
	if err != nil {
		slog.Error("circle members", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer rows.Close()

	items := make([]memberOut, 0)
	for rows.Next() {
		var m memberOut
		if err := rows.Scan(&m.UserID, &m.DisplayName, &m.Role, &m.JoinedAt); err != nil {
			slog.Error("circle members scan", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (a *API) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	circleID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	targetID, ok := parseUUID(chi.URLParam(r, "userID"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "member not found")
		return
	}
	actor := auth.UserFrom(r.Context())
	ctx := r.Context()

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockCircle(ctx, tx, circleID); err != nil {
		writeCircleErr(w, err)
		return
	}

	actorRole, err := memberRoleTx(ctx, tx, circleID, actor.ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	targetRole, err := memberRoleTx(ctx, tx, circleID, targetID)
	if err != nil {
		if errors.Is(err, errNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "member not found")
			return
		}
		writeCircleErr(w, err)
		return
	}

	self := actor.ID == targetID
	if self {
		if targetRole == roleOwner {
			var others int
			if err := tx.QueryRow(ctx, `
				SELECT COUNT(*)::int FROM circle_members
				WHERE circle_id = $1 AND user_id <> $2
			`, circleID, actor.ID).Scan(&others); err != nil {
				slog.Error("circle leave count", "err", err)
				httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
				return
			}
			if others > 0 {
				httpx.WriteError(w, http.StatusConflict, "owner_must_transfer", "transfer circle ownership before leaving")
				return
			}
		}
	} else {
		switch {
		case targetRole == roleOwner:
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "cannot remove owner")
			return
		case actorRole == roleOwner:
			// owner may kick admin or member
		case actorRole == roleAdmin && targetRole == roleMember:
			// admin may kick members
		default:
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
			return
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM circle_members WHERE circle_id = $1 AND user_id = $2`, circleID, targetID); err != nil {
		slog.Error("circle remove member", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE circles SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM circle_members m WHERE m.circle_id = circles.id)
	`, circleID); err != nil {
		slog.Error("circle empty delete", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleTransfer(w http.ResponseWriter, r *http.Request) {
	circleID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	var req transferReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	targetID, ok := parseUUID(strings.TrimSpace(req.UserID))
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "user_id required")
		return
	}
	actor := auth.UserFrom(r.Context())
	if actor.ID == targetID {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "cannot transfer to self")
		return
	}

	ctx := r.Context()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockCircle(ctx, tx, circleID); err != nil {
		writeCircleErr(w, err)
		return
	}
	actorRole, err := memberRoleTx(ctx, tx, circleID, actor.ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	if actorRole != roleOwner {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if _, err := memberRoleTx(ctx, tx, circleID, targetID); err != nil {
		if errors.Is(err, errNotFound) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "user is not a member")
			return
		}
		writeCircleErr(w, err)
		return
	}

	if _, err := tx.Exec(ctx, `
		UPDATE circle_members SET role = $3
		WHERE circle_id = $1 AND user_id = $2
	`, circleID, actor.ID, roleAdmin); err != nil {
		slog.Error("circle transfer demote", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE circle_members SET role = $3
		WHERE circle_id = $1 AND user_id = $2
	`, circleID, targetID, roleOwner); err != nil {
		slog.Error("circle transfer promote", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	out, err := a.loadCircle(ctx, circleID, actor.ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	circleID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	var req createInviteReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	if req.MaxUses != nil && *req.MaxUses <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "max_uses must be null or > 0")
		return
	}

	u := auth.UserFrom(r.Context())
	role, err := a.memberRole(r.Context(), circleID, u.ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	if !canManage(role) {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}

	raw, err := newRawToken()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	var out inviteOut
	err = a.pool.QueryRow(r.Context(), `
		INSERT INTO invites (circle_id, token_hash, created_by, expires_at, max_uses)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, expires_at, max_uses
	`, circleID, hashToken(a.cfg.SessionSecret, raw), u.ID, time.Now().Add(inviteTTL), req.MaxUses).Scan(&out.ID, &out.ExpiresAt, &out.MaxUses)
	if err != nil {
		slog.Error("invite insert", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	out.Token = raw
	out.URL = strings.TrimRight(a.cfg.AppBaseURL, "/") + "/join/" + raw
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	circleID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	inviteID, ok := parseUUID(chi.URLParam(r, "inviteID"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "invite not found")
		return
	}
	u := auth.UserFrom(r.Context())
	role, err := a.memberRole(r.Context(), circleID, u.ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	if !canManage(role) {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `
		DELETE FROM invites WHERE id = $1 AND circle_id = $2
	`, inviteID, circleID)
	if err != nil {
		slog.Error("invite revoke", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "invite not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(chi.URLParam(r, "token"))
	if raw == "" {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "invite not found")
		return
	}
	u := auth.UserFrom(r.Context())
	ctx := r.Context()

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inviteID, circleID uuid.UUID
	var expiresAt time.Time
	var maxUses *int
	var uses int
	err = tx.QueryRow(ctx, `
		SELECT i.id, i.circle_id, i.expires_at, i.max_uses, i.uses
		FROM invites i
		JOIN circles c ON c.id = i.circle_id AND c.deleted_at IS NULL
		WHERE i.token_hash = $1
		FOR UPDATE OF i
	`, hashToken(a.cfg.SessionSecret, raw)).Scan(&inviteID, &circleID, &expiresAt, &maxUses, &uses)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "invite not found")
			return
		}
		slog.Error("invite lookup", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if !expiresAt.After(time.Now()) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invite expired")
		return
	}
	if maxUses != nil && uses >= *maxUses {
		httpx.WriteError(w, http.StatusConflict, "conflict", "invite has no remaining uses")
		return
	}

	if err := lockCircle(ctx, tx, circleID); err != nil {
		writeCircleErr(w, err)
		return
	}

	if _, err := memberRoleTx(ctx, tx, circleID, u.ID); err == nil {
		if err := tx.Commit(ctx); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		out, err := a.loadCircle(ctx, circleID, u.ID)
		if err != nil {
			writeCircleErr(w, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	} else if !errors.Is(err, errNotFound) {
		writeCircleErr(w, err)
		return
	}

	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM circle_members WHERE circle_id = $1`, circleID).Scan(&n); err != nil {
		slog.Error("circle member count", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if n >= maxMembers {
		httpx.WriteError(w, http.StatusConflict, "conflict", "circle is full")
		return
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO circle_members (circle_id, user_id, role)
		VALUES ($1, $2, $3)
	`, circleID, u.ID, roleMember); err != nil {
		if isUniqueViolation(err) {
			_ = tx.Rollback(ctx)
			out, loadErr := a.loadCircle(ctx, circleID, u.ID)
			if loadErr != nil {
				writeCircleErr(w, loadErr)
				return
			}
			httpx.WriteJSON(w, http.StatusOK, out)
			return
		}
		slog.Error("circle join insert", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	tag, err := tx.Exec(ctx, `
		UPDATE invites SET uses = uses + 1
		WHERE id = $1 AND (max_uses IS NULL OR uses < max_uses)
	`, inviteID)
	if err != nil {
		slog.Error("invite use", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusConflict, "conflict", "invite has no remaining uses")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	out, err := a.loadCircle(ctx, circleID, u.ID)
	if err != nil {
		writeCircleErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

var errNotFound = errors.New("not found")

func (a *API) loadCircle(ctx context.Context, circleID, userID uuid.UUID) (*circleOut, error) {
	var c circleOut
	err := a.pool.QueryRow(ctx, `
		SELECT c.id, c.name, c.emoji, c.tz, m.role,
		       (SELECT COUNT(*)::int FROM circle_members cm WHERE cm.circle_id = c.id)
		FROM circles c
		JOIN circle_members m ON m.circle_id = c.id AND m.user_id = $2
		WHERE c.id = $1 AND c.deleted_at IS NULL
	`, circleID, userID).Scan(&c.ID, &c.Name, &c.Emoji, &c.TZ, &c.Role, &c.MemberCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (a *API) memberRole(ctx context.Context, circleID, userID uuid.UUID) (string, error) {
	var deleted *time.Time
	err := a.pool.QueryRow(ctx, `SELECT deleted_at FROM circles WHERE id = $1`, circleID).Scan(&deleted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", err
	}
	if deleted != nil {
		return "", errNotFound
	}
	var role string
	err = a.pool.QueryRow(ctx, `
		SELECT role FROM circle_members WHERE circle_id = $1 AND user_id = $2
	`, circleID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", err
	}
	return role, nil
}

func memberRoleTx(ctx context.Context, tx pgx.Tx, circleID, userID uuid.UUID) (string, error) {
	var role string
	err := tx.QueryRow(ctx, `
		SELECT role FROM circle_members WHERE circle_id = $1 AND user_id = $2
	`, circleID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", err
	}
	return role, nil
}

func lockCircle(ctx context.Context, tx pgx.Tx, circleID uuid.UUID) error {
	var deleted *time.Time
	err := tx.QueryRow(ctx, `
		SELECT deleted_at FROM circles WHERE id = $1 FOR UPDATE
	`, circleID).Scan(&deleted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return err
	}
	if deleted != nil {
		return errNotFound
	}
	return nil
}

func writeCircleErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	slog.Error("circle", "err", err)
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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
