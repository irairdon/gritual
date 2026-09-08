package rituals

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/circles"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
	"github.com/irairdon/gritual/internal/httpx"
)

const origin = "http://localhost:8080"

type harness struct {
	t *testing.T
	h http.Handler
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
	circAPI := circles.New(cfg, pool)
	ritAPI := New(cfg, pool)
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
	return &harness{
		t: t,
		h: httpx.NewRouter(ui, pool, func(r chi.Router) {
			authAPI.Mount(r)
			circAPI.Mount(r)
			ritAPI.Mount(r)
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

func (h *harness) login(email string) *http.Cookie {
	h.t.Helper()
	res := h.do(http.MethodPost, "/api/v1/auth/dev-login", nil, map[string]any{"email": email})
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("dev-login status = %d", res.StatusCode)
	}
	res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	h.t.Fatal("missing session cookie")
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

func TestRituals(t *testing.T) {
	h := newHarness(t)
	cookie := h.login("rit-" + uuid.NewString()[:8] + "@example.com")

	t.Run("create defaults scoring_key and direction", func(t *testing.T) {
		tests := []struct {
			typ string
			key string
		}{
			{typ: "weight", key: "weight.progress"},
			{typ: "workout", key: "workout.volume"},
			{typ: "habit", key: "habit.completion"},
			{typ: "fishing", key: "fishing.days"},
			{typ: "custom", key: "custom.sum"},
			{typ: "meal", key: ""},
		}
		for _, tt := range tests {
			t.Run(tt.typ, func(t *testing.T) {
				res := h.do(http.MethodPost, "/api/v1/rituals", cookie, map[string]any{
					"type":  tt.typ,
					"title": tt.typ + " ritual",
				})
				if res.StatusCode != http.StatusCreated {
					t.Fatalf("status = %d code=%s", res.StatusCode, errCode(t, res))
				}
				var out ritualOut
				readJSON(t, res, &out)
				if out.Type != tt.typ {
					t.Fatalf("type = %q", out.Type)
				}
				if out.ScoringKey != tt.key {
					t.Fatalf("scoring_key = %q want %q", out.ScoringKey, tt.key)
				}
				if out.Direction != "at_least" || out.Period != "none" {
					t.Fatalf("defaults = %+v", out)
				}
			})
		}
	})

	t.Run("strength is not a type", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/rituals", cookie, map[string]any{
			"type":  "strength",
			"title": "nope",
		})
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "invalid" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("get patch delete", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/rituals", cookie, map[string]any{
			"type":      "habit",
			"title":     "Walk",
			"direction": "hit",
			"period":    "daily",
		})
		var created ritualOut
		readJSON(t, res, &created)

		res = h.do(http.MethodGet, "/api/v1/rituals/"+created.ID.String(), cookie, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("get status = %d", res.StatusCode)
		}
		var got ritualOut
		readJSON(t, res, &got)
		if got.Title != "Walk" || got.Direction != "hit" {
			t.Fatalf("got = %+v", got)
		}

		res = h.do(http.MethodPatch, "/api/v1/rituals/"+created.ID.String(), cookie, map[string]any{
			"title": "Walk 10k",
		})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("patch status = %d", res.StatusCode)
		}
		readJSON(t, res, &got)
		if got.Title != "Walk 10k" {
			t.Fatalf("patched title = %q", got.Title)
		}

		res = h.do(http.MethodDelete, "/api/v1/rituals/"+created.ID.String(), cookie, nil)
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("delete status = %d", res.StatusCode)
		}
		res.Body.Close()

		res = h.do(http.MethodGet, "/api/v1/rituals/"+created.ID.String(), cookie, nil)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("get deleted status = %d", res.StatusCode)
		}
		res.Body.Close()

		res = h.do(http.MethodGet, "/api/v1/rituals", cookie, nil)
		var list struct {
			Items []ritualOut `json:"items"`
		}
		readJSON(t, res, &list)
		for _, it := range list.Items {
			if it.ID == created.ID {
				t.Fatal("deleted ritual still listed")
			}
		}
	})
}
