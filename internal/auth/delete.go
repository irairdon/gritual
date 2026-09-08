package auth

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/irairdon/gritual/internal/httpx"
)

type deleteMeReq struct {
	ConfirmEmail string `json:"confirm_email"`
}

func (a *API) handleDeleteMe(w http.ResponseWriter, r *http.Request) {
	var req deleteMeReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	u := UserFrom(r.Context())
	if !strings.EqualFold(strings.TrimSpace(req.ConfirmEmail), u.Email) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "confirm_email must match")
		return
	}

	ctx := r.Context()
	ip := httpx.RealIP(r)

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var blocked bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM circle_members me
			JOIN circles c ON c.id = me.circle_id AND c.deleted_at IS NULL
			WHERE me.user_id = $1 AND me.role = 'owner'
			  AND EXISTS (
				SELECT 1 FROM circle_members o
				WHERE o.circle_id = me.circle_id AND o.user_id <> $1
			  )
		)
	`, u.ID).Scan(&blocked); err != nil {
		slog.Error("delete me owner check", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if blocked {
		httpx.WriteError(w, http.StatusConflict, "owner_must_transfer", "transfer circle ownership before deleting account")
		return
	}

	mediaPaths, err := collectMediaPaths(ctx, tx, u.ID)
	if err != nil {
		slog.Error("delete me media list", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	type step struct {
		sql  string
		args []any
	}
	steps := []step{
		{`DELETE FROM sessions WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM refresh_tokens WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM api_tokens WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM magic_links WHERE email = (SELECT email FROM users WHERE id = $1)`, []any{u.ID}},
		{`DELETE FROM ai_messages WHERE conversation_id IN (SELECT id FROM ai_conversations WHERE user_id = $1)`, []any{u.ID}},
		{`DELETE FROM ai_conversations WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM user_identities WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM reactions WHERE user_id = $1`, []any{u.ID}},
		{`UPDATE feed_posts SET body = '[deleted]', log_id = NULL WHERE user_id = $1`, []any{u.ID}},
		{`UPDATE comments SET body = '[deleted]' WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM challenge_participants WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM log_circles WHERE log_id IN (SELECT id FROM logs WHERE user_id = $1)`, []any{u.ID}},
		{`DELETE FROM logs WHERE user_id = $1`, []any{u.ID}},
		{`UPDATE profiles SET avatar_media_id = NULL WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM media_objects WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM profiles WHERE user_id = $1`, []any{u.ID}},
		{`DELETE FROM circle_members WHERE user_id = $1`, []any{u.ID}},
		{`UPDATE circles SET deleted_at = now(), updated_at = now()
		   WHERE deleted_at IS NULL
		     AND NOT EXISTS (SELECT 1 FROM circle_members m WHERE m.circle_id = circles.id)`, nil},
		{`UPDATE rituals SET deleted_at = now() WHERE owner_user_id = $1 AND deleted_at IS NULL`, []any{u.ID}},
	}
	for _, s := range steps {
		if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
			slog.Error("delete me exec", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}

	if err := writeAuditTx(ctx, tx, &u.ID, "account_delete", ip, map[string]any{"email": u.Email}); err != nil {
		slog.Error("delete me audit", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE audit_log SET user_id = NULL, ip = NULL WHERE user_id = $1`, u.ID); err != nil {
		slog.Error("delete me audit anonymize", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET deleted_at = now(),
		    email = 'deleted+' || id::text || '@invalid.local',
		    password_hash = NULL,
		    is_admin = false,
		    ai_consent_at = NULL,
		    updated_at = now()
		WHERE id = $1
	`, u.ID); err != nil {
		slog.Error("delete me user", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	for _, p := range mediaPaths {
		if p == "" {
			continue
		}
		full := p
		if !filepath.IsAbs(p) {
			full = filepath.Join(a.cfg.MediaDir, p)
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			slog.Error("delete me media file", "path", full, "err", err)
		}
	}

	a.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func collectMediaPaths(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT path FROM media_objects WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}
