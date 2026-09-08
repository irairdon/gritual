package circles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
	"github.com/irairdon/gritual/internal/httpx"
)

const origin = "http://localhost:8080"

type harness struct {
	t    *testing.T
	pool *pgxpool.Pool
	h    http.Handler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := db.TestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		AppBaseURL:    origin,
		ViteDevOrigin: "http://localhost:5173",
		AuthDevLogin:  true,
		SessionSecret: bytes.Repeat([]byte("s"), 32),
	}
	authAPI := auth.New(cfg, pool)
	circAPI := New(cfg, pool)
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
	return &harness{
		t:    t,
		pool: pool,
		h: httpx.NewRouter(ui, pool, func(r chi.Router) {
			authAPI.Mount(r)
			circAPI.Mount(r)
		}),
	}
}

func (h *harness) do(method, path string, cookie *http.Cookie, body any) *http.Response {
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
	req.Header.Set("Origin", origin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec.Result()
}

func (h *harness) sessionCookie(res *http.Response) *http.Cookie {
	h.t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	return nil
}

func (h *harness) login(email string) (*http.Cookie, string) {
	h.t.Helper()
	res := h.do(http.MethodPost, "/api/v1/auth/dev-login", nil, map[string]any{"email": email})
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("dev-login status = %d", res.StatusCode)
	}
	var payload struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	readJSON(h.t, res, &payload)
	c := h.sessionCookie(res)
	if c == nil {
		h.t.Fatal("missing session cookie")
	}
	return c, payload.User.ID
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

func TestCircles(t *testing.T) {
	h := newHarness(t)

	t.Run("create join via invite", func(t *testing.T) {
		ownerCookie, _ := h.login("owner-" + uuid.NewString()[:8] + "@example.com")
		res := h.do(http.MethodPost, "/api/v1/circles", ownerCookie, map[string]any{
			"name":  "Thursday Fish",
			"emoji": "🎣",
			"tz":    "America/Denver",
		})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d", res.StatusCode)
		}
		var created circleOut
		readJSON(t, res, &created)
		if created.Role != roleOwner || created.MemberCount != 1 {
			t.Fatalf("created = %+v", created)
		}

		res = h.do(http.MethodPost, "/api/v1/circles/"+created.ID.String()+"/invites", ownerCookie, map[string]any{})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("invite status = %d", res.StatusCode)
		}
		var inv inviteOut
		readJSON(t, res, &inv)
		if inv.Token == "" || inv.URL != origin+"/join/"+inv.Token {
			t.Fatalf("invite = %+v", inv)
		}

		joinerCookie, joinerID := h.login("join-" + uuid.NewString()[:8] + "@example.com")
		res = h.do(http.MethodPost, "/api/v1/invites/"+inv.Token+"/accept", joinerCookie, map[string]any{})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("accept status = %d code=%s", res.StatusCode, errCode(t, res))
		}
		var joined circleOut
		readJSON(t, res, &joined)
		if joined.Role != roleMember || joined.MemberCount != 2 {
			t.Fatalf("joined = %+v", joined)
		}

		res = h.do(http.MethodGet, "/api/v1/circles/"+created.ID.String()+"/members", ownerCookie, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("members status = %d", res.StatusCode)
		}
		var members struct {
			Items []memberOut `json:"items"`
		}
		readJSON(t, res, &members)
		if len(members.Items) != 2 {
			t.Fatalf("members = %d", len(members.Items))
		}
		found := false
		for _, m := range members.Items {
			if m.UserID.String() == joinerID && m.Role == roleMember {
				found = true
			}
		}
		if !found {
			t.Fatal("joiner not in members")
		}
	})

	t.Run("revoke invite", func(t *testing.T) {
		ownerCookie, _ := h.login("rev-owner-" + uuid.NewString()[:8] + "@example.com")
		res := h.do(http.MethodPost, "/api/v1/circles", ownerCookie, map[string]any{"name": "Revoke Me"})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d", res.StatusCode)
		}
		var created circleOut
		readJSON(t, res, &created)

		res = h.do(http.MethodPost, "/api/v1/circles/"+created.ID.String()+"/invites", ownerCookie, map[string]any{})
		var inv inviteOut
		readJSON(t, res, &inv)

		res = h.do(http.MethodDelete, "/api/v1/circles/"+created.ID.String()+"/invites/"+inv.ID.String(), ownerCookie, nil)
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("revoke status = %d", res.StatusCode)
		}
		res.Body.Close()

		joinerCookie, _ := h.login("rev-join-" + uuid.NewString()[:8] + "@example.com")
		res = h.do(http.MethodPost, "/api/v1/invites/"+inv.Token+"/accept", joinerCookie, map[string]any{})
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("accept after revoke status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "not_found" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("transfer required before owner leave", func(t *testing.T) {
		ownerCookie, ownerID := h.login("xfer-owner-" + uuid.NewString()[:8] + "@example.com")
		res := h.do(http.MethodPost, "/api/v1/circles", ownerCookie, map[string]any{"name": "Transfer"})
		var created circleOut
		readJSON(t, res, &created)

		res = h.do(http.MethodPost, "/api/v1/circles/"+created.ID.String()+"/invites", ownerCookie, map[string]any{})
		var inv inviteOut
		readJSON(t, res, &inv)

		memberCookie, memberID := h.login("xfer-mem-" + uuid.NewString()[:8] + "@example.com")
		res = h.do(http.MethodPost, "/api/v1/invites/"+inv.Token+"/accept", memberCookie, map[string]any{})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("accept status = %d", res.StatusCode)
		}
		res.Body.Close()

		res = h.do(http.MethodDelete, "/api/v1/circles/"+created.ID.String()+"/members/"+ownerID, ownerCookie, nil)
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("owner leave status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "owner_must_transfer" {
			t.Fatalf("code = %q", got)
		}

		res = h.do(http.MethodPost, "/api/v1/circles/"+created.ID.String()+"/transfer", ownerCookie, map[string]any{
			"user_id": memberID,
		})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("transfer status = %d code=%s", res.StatusCode, errCode(t, res))
		}
		var after circleOut
		readJSON(t, res, &after)
		if after.Role != roleAdmin {
			t.Fatalf("former owner role = %s", after.Role)
		}

		res = h.do(http.MethodDelete, "/api/v1/circles/"+created.ID.String()+"/members/"+ownerID, ownerCookie, nil)
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("leave after transfer status = %d", res.StatusCode)
		}
		res.Body.Close()

		res = h.do(http.MethodGet, "/api/v1/circles/"+created.ID.String(), memberCookie, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("get as new owner status = %d", res.StatusCode)
		}
		var got circleOut
		readJSON(t, res, &got)
		if got.Role != roleOwner || got.MemberCount != 1 {
			t.Fatalf("after leave = %+v", got)
		}
	})

	t.Run("max 50 members", func(t *testing.T) {
		ownerCookie, _ := h.login("full-owner-" + uuid.NewString()[:8] + "@example.com")
		res := h.do(http.MethodPost, "/api/v1/circles", ownerCookie, map[string]any{"name": "Full"})
		var created circleOut
		readJSON(t, res, &created)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for i := 0; i < maxMembers-2; i++ {
			var uid uuid.UUID
			err := h.pool.QueryRow(ctx, `
				INSERT INTO users (email, display_name, dob)
				VALUES ($1, 'filler', DATE '1990-01-01')
				RETURNING id
			`, fmt.Sprintf("fill-%s-%d@example.com", created.ID.String()[:8], i)).Scan(&uid)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.pool.Exec(ctx, `
				INSERT INTO circle_members (circle_id, user_id, role)
				VALUES ($1, $2, 'member')
			`, created.ID, uid); err != nil {
				t.Fatal(err)
			}
		}

		res = h.do(http.MethodPost, "/api/v1/circles/"+created.ID.String()+"/invites", ownerCookie, map[string]any{})
		var inv inviteOut
		readJSON(t, res, &inv)

		okCookie, _ := h.login("full-ok-" + uuid.NewString()[:8] + "@example.com")
		res = h.do(http.MethodPost, "/api/v1/invites/"+inv.Token+"/accept", okCookie, map[string]any{})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("50th join status = %d code=%s", res.StatusCode, errCode(t, res))
		}
		var joined circleOut
		readJSON(t, res, &joined)
		if joined.MemberCount != maxMembers {
			t.Fatalf("member_count = %d", joined.MemberCount)
		}

		failCookie, _ := h.login("full-fail-" + uuid.NewString()[:8] + "@example.com")
		res = h.do(http.MethodPost, "/api/v1/invites/"+inv.Token+"/accept", failCookie, map[string]any{})
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("51st join status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "conflict" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("tz persisted", func(t *testing.T) {
		cookie, _ := h.login("tz-" + uuid.NewString()[:8] + "@example.com")

		tests := []struct {
			name    string
			body    map[string]any
			wantTZ  string
			wantErr bool
		}{
			{name: "explicit", body: map[string]any{"name": "Chicago", "tz": "America/Chicago"}, wantTZ: "America/Chicago"},
			{name: "default", body: map[string]any{"name": "Default TZ"}, wantTZ: defaultTZ},
			{name: "invalid", body: map[string]any{"name": "Bad", "tz": "Not/AZone"}, wantErr: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := h.do(http.MethodPost, "/api/v1/circles", cookie, tt.body)
				if tt.wantErr {
					if res.StatusCode != http.StatusBadRequest {
						t.Fatalf("status = %d", res.StatusCode)
					}
					if got := errCode(t, res); got != "invalid" {
						t.Fatalf("code = %q", got)
					}
					return
				}
				if res.StatusCode != http.StatusCreated {
					t.Fatalf("create status = %d", res.StatusCode)
				}
				var created circleOut
				readJSON(t, res, &created)
				if created.TZ != tt.wantTZ {
					t.Fatalf("create tz = %q want %q", created.TZ, tt.wantTZ)
				}
				res = h.do(http.MethodGet, "/api/v1/circles/"+created.ID.String(), cookie, nil)
				if res.StatusCode != http.StatusOK {
					t.Fatalf("get status = %d", res.StatusCode)
				}
				var got circleOut
				readJSON(t, res, &got)
				if got.TZ != tt.wantTZ {
					t.Fatalf("get tz = %q want %q", got.TZ, tt.wantTZ)
				}
			})
		}
	})
}
