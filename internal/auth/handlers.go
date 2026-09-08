package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/irairdon/gritual/internal/httpx"
)

type registerReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	DOB         string `json:"dob"`
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type emailReq struct {
	Email string `json:"email"`
}

type tokenReq struct {
	Token string `json:"token"`
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

type patchMeReq struct {
	DisplayName  *string `json:"display_name"`
	Units        *string `json:"units"`
	TZ           *string `json:"tz"`
	CalorieGoal  *int    `json:"calorie_goal"`
	ProteinGoalG *int    `json:"protein_goal_g"`
	Bio          *string `json:"bio"`
}

func (a *API) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	email := normalizeEmail(req.Email)
	if !validEmail(email) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid email")
		return
	}
	display := strings.TrimSpace(req.DisplayName)
	if display == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "display_name required")
		return
	}
	if len(req.Password) < minPasswordLen {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "password too short")
		return
	}
	dob, err := time.Parse("2006-01-02", strings.TrimSpace(req.DOB))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "dob must be YYYY-MM-DD")
		return
	}
	if !atLeast18(dob, time.Now()) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "must be 18 or older")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	ctx := r.Context()
	var id uuid.UUID
	err = a.pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name, dob)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, email, hash, display, dob).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			httpx.WriteError(w, http.StatusConflict, "conflict", "email already registered")
			return
		}
		slog.Error("register insert", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO profiles (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, id); err != nil {
		slog.Error("register profile", "err", err)
	}

	uid := id
	a.audit(ctx, &uid, "register", httpx.RealIP(r), map[string]any{"email": email})
	if err := a.sendMagic(ctx, email); err != nil {
		slog.Error("register verify mail", "err", err)
	}

	u, err := loadUserByID(ctx, a.pool, id)
	if err != nil {
		slog.Error("register load", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	resp, err := a.issueSession(ctx, w, r, u)
	if err != nil {
		slog.Error("register session", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, resp)
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	ip := httpx.RealIP(r)
	if a.limit.loginLocked(ip) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts")
		return
	}
	email := normalizeEmail(req.Email)
	ctx := r.Context()

	var id uuid.UUID
	var hash *string
	err := a.pool.QueryRow(ctx, `
		SELECT id, password_hash FROM users
		WHERE email = $1 AND deleted_at IS NULL
	`, email).Scan(&id, &hash)
	fail := func() {
		a.limit.loginFail(ip)
		a.audit(ctx, nil, "login_fail", ip, map[string]any{"email": email})
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
	}
	if err != nil || hash == nil || !verifyPassword(req.Password, *hash) {
		fail()
		return
	}

	if err := a.reconcileAdmin(ctx, id, email, true); err != nil {
		slog.Error("login admin reconcile", "err", err)
	}
	u, err := loadUserByID(ctx, a.pool, id)
	if err != nil {
		fail()
		return
	}
	resp, err := a.issueSession(ctx, w, r, u)
	if err != nil {
		slog.Error("login session", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	a.audit(ctx, &u.ID, "login_ok", ip, map[string]any{"email": email})
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		if _, err := a.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(a.cfg.SessionSecret, c.Value)); err != nil {
			slog.Error("logout delete session", "err", err)
		}
	}
	if u := UserFrom(ctx); u != nil {
		a.audit(ctx, &u.ID, "logout", httpx.RealIP(r), nil)
	}
	a.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleMagicLink(w http.ResponseWriter, r *http.Request) {
	var req emailReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	email := normalizeEmail(req.Email)
	ctx := r.Context()
	a.audit(ctx, nil, "magic_request", httpx.RealIP(r), map[string]any{"email": email})
	if validEmail(email) && !a.limit.magicLimited(email) {
		a.limit.magicHit(email)
		if err := a.sendMagic(ctx, email); err != nil {
			slog.Error("magic mail", "err", err)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleMagicConsume(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	raw := strings.TrimSpace(req.Token)
	if raw == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	ctx := r.Context()
	var email string
	var id uuid.UUID
	err := a.pool.QueryRow(ctx, `
		UPDATE magic_links
		SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING id, email::text
	`, hashToken(a.cfg.SessionSecret, raw)).Scan(&id, &email)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}

	u, err := loadUserByEmail(ctx, a.pool, email)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	if _, err := a.pool.Exec(ctx, `
		UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now()
		WHERE id = $1
	`, u.ID); err != nil {
		slog.Error("magic verify", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := a.reconcileAdmin(ctx, u.ID, u.Email, true); err != nil {
		slog.Error("magic admin", "err", err)
	}
	u, err = loadUserByID(ctx, a.pool, u.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	resp, err := a.issueSession(ctx, w, r, u)
	if err != nil {
		slog.Error("magic session", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	a.audit(ctx, &u.ID, "magic_consume", httpx.RealIP(r), map[string]any{"email": email})
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (a *API) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.AuthDevLogin {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req emailReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	email := normalizeEmail(req.Email)
	if !validEmail(email) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid email")
		return
	}
	ctx := r.Context()
	u, err := loadUserByEmail(ctx, a.pool, email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("dev-login load", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		display := email
		if i := strings.IndexByte(email, '@'); i > 0 {
			display = email[:i]
		}
		var id uuid.UUID
		err = a.pool.QueryRow(ctx, `
			INSERT INTO users (email, display_name, dob, email_verified_at)
			VALUES ($1, $2, DATE '1990-01-01', now())
			RETURNING id
		`, email, display).Scan(&id)
		if err != nil {
			slog.Error("dev-login insert", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		_, _ = a.pool.Exec(ctx, `INSERT INTO profiles (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, id)
		u, err = loadUserByID(ctx, a.pool, id)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	} else {
		if _, err := a.pool.Exec(ctx, `
			UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now()
			WHERE id = $1
		`, u.ID); err != nil {
			slog.Error("dev-login verify", "err", err)
		}
	}
	if err := a.reconcileAdmin(ctx, u.ID, u.Email, true); err != nil {
		slog.Error("dev-login admin", "err", err)
	}
	u, err = loadUserByID(ctx, a.pool, u.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	resp, err := a.issueSession(ctx, w, r, u)
	if err != nil {
		slog.Error("dev-login session", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (a *API) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	raw := strings.TrimSpace(req.RefreshToken)
	if raw == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	ctx := r.Context()
	sum := hashToken(a.cfg.SessionSecret, raw)

	var userID, familyID, tokenID uuid.UUID
	var expiresAt time.Time
	var revokedAt *time.Time
	err := a.pool.QueryRow(ctx, `
		SELECT id, user_id, family_id, expires_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1
	`, sum).Scan(&tokenID, &userID, &familyID, &expiresAt, &revokedAt)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	if revokedAt != nil {
		_, _ = a.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`, familyID)
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	if time.Now().After(expiresAt) {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}

	u, err := loadUserByID(ctx, a.pool, userID)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}

	accessRaw, err := newRawToken()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	refreshRaw, err := newRawToken()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1`, tokenID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, family_id, expires_at)
		VALUES ($1, $2, $3, $4)
	`, userID, hashToken(a.cfg.SessionSecret, refreshRaw), familyID, time.Now().Add(refreshTTL)); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (user_id, kind, token_hash, user_agent, expires_at)
		VALUES ($1, 'bearer', $2, $3, $4)
	`, userID, hashToken(a.cfg.SessionSecret, accessRaw), r.UserAgent(), time.Now().Add(accessTTL)); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, loginResponse{
		User: loginUser{
			ID:            u.ID.String(),
			Email:         u.Email,
			DisplayName:   u.DisplayName,
			EmailVerified: u.EmailVerified,
		},
		AccessToken:  accessRaw,
		RefreshToken: refreshRaw,
		ExpiresIn:    loginExpiresIn,
	})
}

func (a *API) handleGetMe(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	fresh, err := loadUserByID(r.Context(), a.pool, u.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fresh.me())
}

func (a *API) handlePatchMe(w http.ResponseWriter, r *http.Request) {
	var req patchMeReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	u := UserFrom(r.Context())
	ctx := r.Context()

	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name == "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "display_name required")
			return
		}
		if _, err := a.pool.Exec(ctx, `UPDATE users SET display_name = $2, updated_at = now() WHERE id = $1`, u.ID, name); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}
	if req.Units != nil {
		switch *req.Units {
		case "imperial", "metric":
		default:
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid units")
			return
		}
		if _, err := a.pool.Exec(ctx, `UPDATE users SET units = $2, updated_at = now() WHERE id = $1`, u.ID, *req.Units); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}
	if req.TZ != nil {
		if _, err := time.LoadLocation(*req.TZ); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid tz")
			return
		}
		if _, err := a.pool.Exec(ctx, `UPDATE users SET tz = $2, updated_at = now() WHERE id = $1`, u.ID, *req.TZ); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}
	if req.CalorieGoal != nil {
		if _, err := a.pool.Exec(ctx, `UPDATE users SET calorie_goal = $2, updated_at = now() WHERE id = $1`, u.ID, *req.CalorieGoal); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}
	if req.ProteinGoalG != nil {
		if _, err := a.pool.Exec(ctx, `UPDATE users SET protein_goal_g = $2, updated_at = now() WHERE id = $1`, u.ID, *req.ProteinGoalG); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}
	if req.Bio != nil {
		if _, err := a.pool.Exec(ctx, `
			INSERT INTO profiles (user_id, bio) VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET bio = EXCLUDED.bio
		`, u.ID, *req.Bio); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
	}

	fresh, err := loadUserByID(ctx, a.pool, u.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fresh.me())
}

func (a *API) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid limit")
			return
		}
		if n > 100 {
			n = 100
		}
		limit = n
	}
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	var rows pgx.Rows
	var err error
	if cursor == "" {
		rows, err = a.pool.Query(ctx, `
			SELECT id, user_id, action, meta, created_at
			FROM audit_log
			ORDER BY created_at DESC, id DESC
			LIMIT $1
		`, limit+1)
	} else {
		cid, parseErr := uuid.Parse(cursor)
		if parseErr != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid cursor")
			return
		}
		rows, err = a.pool.Query(ctx, `
			SELECT id, user_id, action, meta, created_at
			FROM audit_log
			WHERE (created_at, id) < (SELECT created_at, id FROM audit_log WHERE id = $1)
			ORDER BY created_at DESC, id DESC
			LIMIT $2
		`, cid, limit+1)
	}
	if err != nil {
		slog.Error("admin audit", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer rows.Close()

	type item struct {
		ID        string         `json:"id"`
		UserID    *string        `json:"user_id"`
		Action    string         `json:"action"`
		Meta      map[string]any `json:"meta"`
		CreatedAt time.Time      `json:"created_at"`
	}
	var items []item
	for rows.Next() {
		var it item
		var id uuid.UUID
		var uid *uuid.UUID
		var meta []byte
		if err := rows.Scan(&id, &uid, &it.Action, &meta, &it.CreatedAt); err != nil {
			slog.Error("admin audit scan", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		it.ID = id.String()
		if uid != nil {
			s := uid.String()
			it.UserID = &s
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &it.Meta)
		}
		if it.Meta == nil {
			it.Meta = map[string]any{}
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		n := items[len(items)-1].ID
		next = &n
	}
	if items == nil {
		items = []item{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (a *API) sendMagic(ctx context.Context, email string) error {
	raw, err := newRawToken()
	if err != nil {
		return err
	}
	if _, err := a.pool.Exec(ctx, `
		INSERT INTO magic_links (email, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, email, hashToken(a.cfg.SessionSecret, raw), time.Now().Add(magicTTL)); err != nil {
		return err
	}
	link := a.cfg.AppBaseURL + "/auth/magic#token=" + raw
	body := "Sign in to Gritual:\n\n" + link + "\n\nThis link expires in one hour.\n"
	return a.mail.Send(ctx, email, "Your Gritual sign-in link", body)
}

func (a *API) reconcileAdmin(ctx context.Context, userID uuid.UUID, email string, verified bool) error {
	if !verified || a.cfg.AdminEmail == "" {
		return nil
	}
	if !strings.EqualFold(email, a.cfg.AdminEmail) {
		return nil
	}
	tag, err := a.pool.Exec(ctx, `
		UPDATE users SET is_admin = true, updated_at = now()
		WHERE id = $1 AND email_verified_at IS NOT NULL AND is_admin = false
	`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		a.audit(ctx, &userID, "admin_grant", "", map[string]any{"email": email})
	}
	return nil
}

func atLeast18(dob, now time.Time) bool {
	dob = dob.UTC().Truncate(24 * time.Hour)
	now = now.UTC()
	anniversary := dob.AddDate(18, 0, 0)
	return !now.Before(anniversary)
}

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func validEmail(s string) bool {
	at := strings.LastIndexByte(s, '@')
	if at < 1 || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
