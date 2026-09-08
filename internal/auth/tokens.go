package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/irairdon/gritual/internal/httpx"
)

type tokenOut struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

type tokenCreated struct {
	tokenOut
	Token string `json:"token"`
}

func (a *API) handleListTokens(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	rows, err := a.pool.Query(r.Context(), `
		SELECT id, name, prefix, created_at, last_used_at
		FROM api_tokens
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, u.ID)
	if err != nil {
		slog.Error("tokens list", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer rows.Close()
	items := make([]tokenOut, 0)
	for rows.Next() {
		var t tokenOut
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt); err != nil {
			slog.Error("tokens list scan", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (a *API) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	if !u.EmailVerified {
		httpx.WriteError(w, http.StatusForbidden, "email_unverified", "email not verified")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}
	raw, err := newPAT()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	prefix := raw
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	var out tokenCreated
	err = a.pool.QueryRow(r.Context(), `
		INSERT INTO api_tokens (user_id, name, token_hash, prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, prefix, created_at, last_used_at
	`, u.ID, name, hashToken(a.cfg.SessionSecret, raw), prefix).Scan(
		&out.ID, &out.Name, &out.Prefix, &out.CreatedAt, &out.LastUsedAt,
	)
	if err != nil {
		slog.Error("token create", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	out.Token = raw
	a.audit(r.Context(), &u.ID, "token_create", httpx.RealIP(r), map[string]any{"token_id": out.ID.String(), "prefix": prefix})
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	tag, err := a.pool.Exec(r.Context(), `
		UPDATE api_tokens SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, id, u.ID)
	if err != nil {
		slog.Error("token revoke", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	a.audit(r.Context(), &u.ID, "token_revoke", httpx.RealIP(r), map[string]any{"token_id": id.String()})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) LookupPAT(ctx context.Context, raw string) (*User, uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, uuid.Nil, nil
	}
	var u User
	var verifiedAt *time.Time
	var tokenID uuid.UUID
	err := a.pool.QueryRow(ctx, `
		SELECT t.id, u.id, u.email::text, u.display_name, u.email_verified_at, u.is_admin,
		       u.units, u.tz, u.calorie_goal, u.protein_goal_g, u.ai_consent_at
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1
		  AND t.revoked_at IS NULL
		  AND u.deleted_at IS NULL
	`, hashToken(a.cfg.SessionSecret, raw)).Scan(
		&tokenID, &u.ID, &u.Email, &u.DisplayName, &verifiedAt, &u.IsAdmin,
		&u.Units, &u.TZ, &u.CalorieGoal, &u.ProteinGoalG, &u.AIConsentAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, uuid.Nil, nil
		}
		return nil, uuid.Nil, err
	}
	u.EmailVerified = verifiedAt != nil
	_, _ = a.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = now() WHERE id = $1`, tokenID)
	return &u, tokenID, nil
}

func newPAT() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "grt_live_" + hex.EncodeToString(b), nil
}
