package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
	"github.com/irairdon/gritual/internal/httpx"
)

type recMailer struct {
	to, url string
}

func (m *recMailer) Send(_ context.Context, to, _, body string) error {
	m.to = to
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "#token=") {
			m.url = strings.TrimSpace(line)
		}
	}
	return nil
}

func (m *recMailer) token() string {
	_, rest, ok := strings.Cut(m.url, "#token=")
	if !ok {
		return ""
	}
	return rest
}

type harness struct {
	t    *testing.T
	pool *pgxpool.Pool
	cfg  config.Config
	mail *recMailer
	h    http.Handler
}

func newHarness(t *testing.T, cfg config.Config) *harness {
	t.Helper()
	pool := db.TestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if len(cfg.SessionSecret) < 32 {
		cfg.SessionSecret = bytes.Repeat([]byte("s"), 32)
	}
	if cfg.AppBaseURL == "" {
		cfg.AppBaseURL = "http://localhost:8080"
	}
	if cfg.ViteDevOrigin == "" {
		cfg.ViteDevOrigin = "http://localhost:5173"
	}
	mail := &recMailer{}
	api := newAPI(cfg, pool, mail)
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
	return &harness{t: t, pool: pool, cfg: cfg, mail: mail, h: httpx.NewRouter(ui, pool, api.Mount, nil)}
}

func (h *harness) do(method, path, origin string, cookie *http.Cookie, body any, extra func(*http.Request)) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if extra != nil {
		extra(req)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec.Result()
}

func (h *harness) sessionCookie(res *http.Response) *http.Cookie {
	h.t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == SessionCookie {
			return c
		}
	}
	return nil
}

func errCode(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	b, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatalf("decode error envelope: %v body=%s", err, b)
	}
	return payload.Error.Code
}

func readJSON(t *testing.T, res *http.Response, dst any) {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("json: %v body=%s", err, b)
	}
}

func adultDOB() string { return "1990-01-15" }

func minorDOB() string {
	return time.Now().UTC().AddDate(-17, 0, 0).Format("2006-01-02")
}

func TestAuth(t *testing.T) {
	h := newHarness(t, config.Config{
		AppBaseURL:    "http://localhost:8080",
		ViteDevOrigin: "http://localhost:5173",
		AuthDevLogin:  true,
		AdminEmail:    "admin@example.com",
	})
	origin := "http://localhost:8080"

	t.Run("register under 18 rejected", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/auth/register", origin, nil, map[string]any{
			"email":        "kid-" + uuid.NewString()[:8] + "@example.com",
			"password":     "password12",
			"display_name": "Kid",
			"dob":          minorDOB(),
		}, nil)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "invalid" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("magic-link always ok", func(t *testing.T) {
		tests := []struct {
			name string
			body any
		}{
			{name: "unknown email", body: map[string]any{"email": "nobody-" + uuid.NewString()[:8] + "@example.com"}},
			{name: "empty", body: map[string]any{"email": ""}},
			{name: "malformed", body: map[string]any{"email": "not-an-email"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := h.do(http.MethodPost, "/api/v1/auth/magic-link", origin, nil, tt.body, nil)
				if res.StatusCode != http.StatusOK {
					t.Fatalf("status = %d", res.StatusCode)
				}
				var payload struct {
					OK bool `json:"ok"`
				}
				readJSON(t, res, &payload)
				if !payload.OK {
					t.Fatal("ok = false")
				}
			})
		}
	})

	t.Run("magic consume POST and GET 404", func(t *testing.T) {
		email := "magic-" + uuid.NewString()[:8] + "@example.com"
		res := h.do(http.MethodPost, "/api/v1/auth/register", origin, nil, map[string]any{
			"email":        email,
			"password":     "password12",
			"display_name": "Mag",
			"dob":          adultDOB(),
		}, nil)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("register status = %d", res.StatusCode)
		}
		res.Body.Close()
		tok := h.mail.token()
		if tok == "" {
			t.Fatal("missing magic token")
		}

		getRes := h.do(http.MethodGet, "/api/v1/auth/magic-link/consume?token="+tok, origin, nil, nil, nil)
		if getRes.StatusCode != http.StatusNotFound {
			t.Fatalf("GET consume status = %d, want 404", getRes.StatusCode)
		}
		if got := errCode(t, getRes); got != "not_found" {
			t.Fatalf("GET consume code = %q", got)
		}

		postRes := h.do(http.MethodPost, "/api/v1/auth/magic-link/consume", origin, nil, map[string]any{
			"token": tok,
		}, nil)
		if postRes.StatusCode != http.StatusOK {
			t.Fatalf("POST consume status = %d", postRes.StatusCode)
		}
		var login loginResponse
		readJSON(t, postRes, &login)
		if !login.User.EmailVerified {
			t.Fatal("expected verified after consume")
		}
		if login.AccessToken == "" || login.RefreshToken == "" || login.ExpiresIn != 3600 {
			t.Fatalf("login json tokens missing: %+v", login)
		}
		if h.sessionCookie(postRes) == nil {
			t.Fatal("missing session cookie")
		}
	})

	t.Run("cookie Secure from https APP_BASE_URL", func(t *testing.T) {
		httpsH := newHarness(t, config.Config{
			AppBaseURL:    "https://gritual.fit",
			ViteDevOrigin: "http://localhost:5173",
			CookieSecure:  true,
		})
		res := httpsH.do(http.MethodPost, "/api/v1/auth/register", "https://gritual.fit", nil, map[string]any{
			"email":        "sec-" + uuid.NewString()[:8] + "@example.com",
			"password":     "password12",
			"display_name": "Sec",
			"dob":          adultDOB(),
		}, nil)
		defer res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d", res.StatusCode)
		}
		c := httpsH.sessionCookie(res)
		if c == nil {
			t.Fatal("missing cookie")
		}
		if !c.Secure {
			t.Fatal("cookie Secure = false, want true")
		}
		if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
			t.Fatalf("cookie flags: %+v", c)
		}

		httpRes := h.do(http.MethodPost, "/api/v1/auth/register", origin, nil, map[string]any{
			"email":        "insecure-" + uuid.NewString()[:8] + "@example.com",
			"password":     "password12",
			"display_name": "Insec",
			"dob":          adultDOB(),
		}, nil)
		defer httpRes.Body.Close()
		c2 := h.sessionCookie(httpRes)
		if c2 == nil {
			t.Fatal("missing cookie")
		}
		if c2.Secure {
			t.Fatal("http APP_BASE_URL cookie should not be Secure")
		}
	})

	t.Run("CSRF rejects bad Origin", func(t *testing.T) {
		email := "csrf-" + uuid.NewString()[:8] + "@example.com"
		res := h.do(http.MethodPost, "/api/v1/auth/register", origin, nil, map[string]any{
			"email":        email,
			"password":     "password12",
			"display_name": "Csrf",
			"dob":          adultDOB(),
		}, nil)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("register status = %d", res.StatusCode)
		}
		var login loginResponse
		readJSON(t, res, &login)
		cookie := h.sessionCookie(res)
		if cookie == nil {
			t.Fatal("missing cookie")
		}

		tests := []struct {
			name       string
			origin     string
			bearer     bool
			wantStatus int
		}{
			{name: "evil origin", origin: "http://evil.example", wantStatus: http.StatusForbidden},
			{name: "missing origin", origin: "", wantStatus: http.StatusForbidden},
			{name: "capacitor", origin: "capacitor://localhost", wantStatus: http.StatusForbidden},
			{name: "good origin", origin: origin, wantStatus: http.StatusOK},
			{name: "bearer skips", origin: "http://evil.example", bearer: true, wantStatus: http.StatusOK},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := h.do(http.MethodPatch, "/api/v1/me", tt.origin, cookie, map[string]any{
					"display_name": "Csrf2",
				}, func(req *http.Request) {
					if tt.bearer {
						req.Header.Del("Cookie")
						req.Header.Set("Authorization", "Bearer "+login.AccessToken)
					}
				})
				defer res.Body.Close()
				if res.StatusCode != tt.wantStatus {
					t.Fatalf("status = %d, want %d", res.StatusCode, tt.wantStatus)
				}
			})
		}
	})

	t.Run("AUTH_DEV_LOGIN off returns 404", func(t *testing.T) {
		off := newHarness(t, config.Config{
			AppBaseURL:   "http://localhost:8080",
			AuthDevLogin: false,
		})
		res := off.do(http.MethodPost, "/api/v1/auth/dev-login", origin, nil, map[string]any{
			"email": "dev@example.com",
		}, nil)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", res.StatusCode)
		}
		if got := errCode(t, res); got != "not_found" {
			t.Fatalf("code = %q", got)
		}

		on := h.do(http.MethodPost, "/api/v1/auth/dev-login", origin, nil, map[string]any{
			"email": "dev-" + uuid.NewString()[:8] + "@example.com",
		}, nil)
		if on.StatusCode != http.StatusOK {
			t.Fatalf("dev-login on status = %d", on.StatusCode)
		}
		on.Body.Close()
	})

	t.Run("DELETE me anonymizes email", func(t *testing.T) {
		email := "del-" + uuid.NewString()[:8] + "@example.com"
		res := h.do(http.MethodPost, "/api/v1/auth/register", origin, nil, map[string]any{
			"email":        email,
			"password":     "password12",
			"display_name": "Del",
			"dob":          adultDOB(),
		}, nil)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("register status = %d", res.StatusCode)
		}
		var login loginResponse
		readJSON(t, res, &login)
		cookie := h.sessionCookie(res)
		if cookie == nil {
			t.Fatal("missing cookie")
		}
		uid := login.User.ID

		del := h.do(http.MethodDelete, "/api/v1/me", origin, cookie, map[string]any{
			"confirm_email": email,
		}, nil)
		defer del.Body.Close()
		if del.StatusCode != http.StatusNoContent {
			t.Fatalf("delete status = %d", del.StatusCode)
		}

		var gotEmail string
		var deletedAt *time.Time
		var hash *string
		var isAdmin bool
		err := h.pool.QueryRow(context.Background(), `
			SELECT email::text, deleted_at, password_hash, is_admin FROM users WHERE id = $1
		`, uid).Scan(&gotEmail, &deletedAt, &hash, &isAdmin)
		if err != nil {
			t.Fatal(err)
		}
		want := "deleted+" + uid + "@invalid.local"
		if gotEmail != want {
			t.Fatalf("email = %q, want %q", gotEmail, want)
		}
		if deletedAt == nil {
			t.Fatal("deleted_at is null")
		}
		if hash != nil {
			t.Fatal("password_hash still set")
		}
		if isAdmin {
			t.Fatal("is_admin still true")
		}
	})
}

func TestAtLeast18(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		dob  time.Time
		want bool
	}{
		{time.Date(2008, 9, 8, 0, 0, 0, 0, time.UTC), true},
		{time.Date(2008, 9, 9, 0, 0, 0, 0, time.UTC), false},
		{time.Date(2007, 1, 1, 0, 0, 0, 0, time.UTC), true},
	}
	for _, tt := range tests {
		if got := atLeast18(tt.dob, now); got != tt.want {
			t.Fatalf("dob=%s got %v want %v", tt.dob.Format("2006-01-02"), got, tt.want)
		}
	}
}
