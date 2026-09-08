package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/httpx"
)

const (
	SessionCookie  = "gritual_session"
	accessTTL      = time.Hour
	cookieTTL      = 30 * 24 * time.Hour
	refreshTTL     = 30 * 24 * time.Hour
	magicTTL       = time.Hour
	loginExpiresIn = 3600
	minPasswordLen = 8
)

type ctxKey int

const userKey ctxKey = 1

type API struct {
	cfg   config.Config
	pool  *pgxpool.Pool
	mail  Mailer
	limit *limiter
}

func New(cfg config.Config, pool *pgxpool.Pool) *API {
	return newAPI(cfg, pool, nil)
}

func newAPI(cfg config.Config, pool *pgxpool.Pool, mail Mailer) *API {
	if mail == nil {
		mail = newMailer(cfg)
	}
	return &API{cfg: cfg, pool: pool, mail: mail, limit: newLimiter()}
}

func (a *API) Mount(r chi.Router) {
	r.Use(httpx.CORS(a.cfg.AllowedOrigins()))
	r.Use(httpx.OriginCSRF(a.cfg.AllowedOrigins(), SessionCookie))
	r.Use(a.authenticate)

	r.Post("/auth/register", a.handleRegister)
	r.Post("/auth/login", a.handleLogin)
	r.Post("/auth/logout", a.handleLogout)
	r.Post("/auth/magic-link", a.handleMagicLink)
	r.Post("/auth/magic-link/consume", a.handleMagicConsume)
	r.Post("/auth/dev-login", a.handleDevLogin)
	r.Post("/auth/refresh", a.handleRefresh)

	r.Get("/me", a.require(a.handleGetMe))
	r.Patch("/me", a.require(a.handlePatchMe))
	r.Post("/me/ai-consent", a.require(a.handleAIConsent))
	r.Delete("/me", a.require(a.handleDeleteMe))
	r.Get("/admin/audit", a.require(a.requireAdmin(a.handleAdminAudit)))
}

type User struct {
	ID            uuid.UUID
	Email         string
	DisplayName   string
	EmailVerified bool
	IsAdmin       bool
	Units         string
	TZ            string
	CalorieGoal   *int
	ProteinGoalG  *int
	Bio           *string
	HeightCM      *float64
	AvatarMediaID *uuid.UUID
	AIConsentAt   *time.Time
}

type loginUser struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	EmailVerified bool   `json:"email_verified"`
}

type loginResponse struct {
	User         loginUser `json:"user"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in"`
}

type meResponse struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	DisplayName   string     `json:"display_name"`
	EmailVerified bool       `json:"email_verified"`
	Units         string     `json:"units"`
	TZ            string     `json:"tz"`
	CalorieGoal   *int       `json:"calorie_goal"`
	ProteinGoalG  *int       `json:"protein_goal_g"`
	Bio           *string    `json:"bio"`
	HeightCM      *float64   `json:"height_cm"`
	AvatarMediaID *string    `json:"avatar_media_id"`
	IsAdmin       bool       `json:"is_admin"`
	AIConsentAt   *time.Time `json:"ai_consent_at"`
}

func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(userKey).(*User)
	return u
}

func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := a.userFromRequest(r)
		if err != nil {
			slog.Debug("auth lookup", "err", err)
		}
		if u != nil {
			r = r.WithContext(context.WithValue(r.Context(), userKey, u))
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if UserFrom(r.Context()) == nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}
		next(w, r)
	}
}

func (a *API) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := UserFrom(r.Context())
		if u == nil || !u.IsAdmin {
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "admin only")
			return
		}
		next(w, r)
	}
}

func (a *API) userFromRequest(r *http.Request) (*User, error) {
	if h := strings.TrimSpace(r.Header.Get("Authorization")); len(h) >= 7 && strings.EqualFold(h[:7], "Bearer ") {
		return a.userFromToken(r.Context(), strings.TrimSpace(h[7:]), "bearer")
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	return a.userFromToken(r.Context(), c.Value, "cookie")
}

func (a *API) RequestUserID(r *http.Request) (uuid.UUID, bool) {
	if u := UserFrom(r.Context()); u != nil {
		return u.ID, true
	}
	u, err := a.userFromRequest(r)
	if err != nil || u == nil {
		return uuid.Nil, false
	}
	return u.ID, true
}

func (a *API) userFromToken(ctx context.Context, raw, kind string) (*User, error) {
	if raw == "" {
		return nil, nil
	}
	var u User
	var verifiedAt *time.Time
	err := a.pool.QueryRow(ctx, `
		SELECT u.id, u.email::text, u.display_name, u.email_verified_at, u.is_admin,
		       u.units, u.tz, u.calorie_goal, u.protein_goal_g, p.bio, u.ai_consent_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN profiles p ON p.user_id = u.id
		WHERE s.token_hash = $1
		  AND s.kind = $2
		  AND s.expires_at > now()
		  AND u.deleted_at IS NULL
	`, hashToken(a.cfg.SessionSecret, raw), kind).Scan(
		&u.ID, &u.Email, &u.DisplayName, &verifiedAt, &u.IsAdmin,
		&u.Units, &u.TZ, &u.CalorieGoal, &u.ProteinGoalG, &u.Bio, &u.AIConsentAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	u.EmailVerified = verifiedAt != nil
	return &u, nil
}

func (a *API) issueSession(ctx context.Context, w http.ResponseWriter, r *http.Request, u *User) (*loginResponse, error) {
	cookieRaw, err := newRawToken()
	if err != nil {
		return nil, err
	}
	accessRaw, err := newRawToken()
	if err != nil {
		return nil, err
	}
	refreshRaw, err := newRawToken()
	if err != nil {
		return nil, err
	}
	familyID := uuid.New()
	now := time.Now()
	ua := r.UserAgent()

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (user_id, kind, token_hash, user_agent, expires_at)
		VALUES ($1, 'cookie', $2, $3, $4)
	`, u.ID, hashToken(a.cfg.SessionSecret, cookieRaw), ua, now.Add(cookieTTL)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (user_id, kind, token_hash, user_agent, expires_at)
		VALUES ($1, 'bearer', $2, $3, $4)
	`, u.ID, hashToken(a.cfg.SessionSecret, accessRaw), ua, now.Add(accessTTL)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, family_id, expires_at)
		VALUES ($1, $2, $3, $4)
	`, u.ID, hashToken(a.cfg.SessionSecret, refreshRaw), familyID, now.Add(refreshTTL)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	http.SetCookie(w, a.sessionCookie(cookieRaw, cookieTTL))
	return &loginResponse{
		User: loginUser{
			ID:            u.ID.String(),
			Email:         u.Email,
			DisplayName:   u.DisplayName,
			EmailVerified: u.EmailVerified,
		},
		AccessToken:  accessRaw,
		RefreshToken: refreshRaw,
		ExpiresIn:    loginExpiresIn,
	}, nil
}

func (a *API) sessionCookie(raw string, ttl time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name:     SessionCookie,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.cfg.CookieSecure,
	}
	if ttl > 0 {
		c.MaxAge = int(ttl.Seconds())
		c.Expires = time.Now().Add(ttl)
	} else {
		c.MaxAge = -1
		c.Expires = time.Unix(0, 0)
	}
	return c
}

func (a *API) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, a.sessionCookie("", -1))
}

func (u *User) me() meResponse {
	var avatar *string
	if u.AvatarMediaID != nil {
		s := u.AvatarMediaID.String()
		avatar = &s
	}
	return meResponse{
		ID:            u.ID.String(),
		Email:         u.Email,
		DisplayName:   u.DisplayName,
		EmailVerified: u.EmailVerified,
		Units:         u.Units,
		TZ:            u.TZ,
		CalorieGoal:   u.CalorieGoal,
		ProteinGoalG:  u.ProteinGoalG,
		Bio:           u.Bio,
		HeightCM:      u.HeightCM,
		AvatarMediaID: avatar,
		IsAdmin:       u.IsAdmin,
		AIConsentAt:   u.AIConsentAt,
	}
}

func (a *API) audit(ctx context.Context, userID *uuid.UUID, action, ip string, meta map[string]any) {
	if err := writeAuditTx(ctx, a.pool, userID, action, ip, meta); err != nil {
		slog.Error("audit_log", "action", action, "err", err)
	}
}

func writeAuditTx(ctx context.Context, tx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}, userID *uuid.UUID, action, ip string, meta map[string]any) error {
	if meta == nil {
		meta = map[string]any{}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	var ipArg any
	if parsed := net.ParseIP(ip); parsed != nil {
		ipArg = parsed
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_log (user_id, action, meta, ip)
		VALUES ($1, $2, $3, $4)
	`, userID, action, b, ipArg)
	return err
}

func loadUserByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*User, error) {
	var u User
	var verifiedAt *time.Time
	err := pool.QueryRow(ctx, `
		SELECT u.id, u.email::text, u.display_name, u.email_verified_at, u.is_admin,
		       u.units, u.tz, u.calorie_goal, u.protein_goal_g, p.bio, p.height_cm, p.avatar_media_id, u.ai_consent_at
		FROM users u
		LEFT JOIN profiles p ON p.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, id).Scan(
		&u.ID, &u.Email, &u.DisplayName, &verifiedAt, &u.IsAdmin,
		&u.Units, &u.TZ, &u.CalorieGoal, &u.ProteinGoalG, &u.Bio, &u.HeightCM, &u.AvatarMediaID, &u.AIConsentAt,
	)
	if err != nil {
		return nil, err
	}
	u.EmailVerified = verifiedAt != nil
	return &u, nil
}

func loadUserByEmail(ctx context.Context, pool *pgxpool.Pool, email string) (*User, error) {
	var u User
	var verifiedAt *time.Time
	err := pool.QueryRow(ctx, `
		SELECT u.id, u.email::text, u.display_name, u.email_verified_at, u.is_admin,
		       u.units, u.tz, u.calorie_goal, u.protein_goal_g, p.bio, p.height_cm, p.avatar_media_id, u.ai_consent_at
		FROM users u
		LEFT JOIN profiles p ON p.user_id = u.id
		WHERE u.email = $1 AND u.deleted_at IS NULL
	`, email).Scan(
		&u.ID, &u.Email, &u.DisplayName, &verifiedAt, &u.IsAdmin,
		&u.Units, &u.TZ, &u.CalorieGoal, &u.ProteinGoalG, &u.Bio, &u.HeightCM, &u.AvatarMediaID, &u.AIConsentAt,
	)
	if err != nil {
		return nil, err
	}
	u.EmailVerified = verifiedAt != nil
	return &u, nil
}
