package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/circles"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/rituals"
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
	circAPI := circles.New(cfg, pool)
	ritAPI := rituals.New(cfg, pool)
	logAPI := New(cfg, pool)
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
	return &harness{
		t:    t,
		pool: pool,
		h: httpx.NewRouter(ui, pool, func(r chi.Router) {
			authAPI.Mount(r)
			circAPI.Mount(r)
			ritAPI.Mount(r)
			logAPI.Mount(r)
		}, nil),
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
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			return c, payload.User.ID
		}
	}
	h.t.Fatal("missing session cookie")
	return nil, ""
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

func TestLogs(t *testing.T) {
	h := newHarness(t)
	cookie, userID := h.login("log-" + uuid.NewString()[:8] + "@example.com")
	uid, err := uuid.Parse(userID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("weight stores kg from lb", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/weights", cookie, map[string]any{
			"lb":        220.46226218,
			"logged_at": "2026-09-08T13:00:00Z",
			"notes":     "",
		})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d code=%s", res.StatusCode, errCode(t, res))
		}
		var out logOut
		readJSON(t, res, &out)
		if out.Type != "weight" || out.Visibility != "private" {
			t.Fatalf("out = %+v", out)
		}
		if out.Weight == nil || math.Abs(out.Weight.KG-100) > 0.001 {
			t.Fatalf("weight = %+v", out.Weight)
		}
		var kg float64
		if err := h.pool.QueryRow(ctx, `SELECT kg FROM weight_logs WHERE log_id = $1`, out.ID).Scan(&kg); err != nil {
			t.Fatal(err)
		}
		if math.Abs(kg-100) > 0.001 {
			t.Fatalf("stored kg = %v", kg)
		}
	})

	t.Run("GET /logs excludes deleted", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/habits", cookie, map[string]any{"status": "done"})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d", res.StatusCode)
		}
		var created logOut
		readJSON(t, res, &created)

		res = h.do(http.MethodDelete, "/api/v1/logs/"+created.ID.String(), cookie, nil)
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("delete status = %d", res.StatusCode)
		}
		res.Body.Close()

		res = h.do(http.MethodGet, "/api/v1/logs?type=habit", cookie, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("list status = %d", res.StatusCode)
		}
		var list struct {
			Items []logOut `json:"items"`
		}
		readJSON(t, res, &list)
		for _, it := range list.Items {
			if it.ID == created.ID {
				t.Fatal("deleted log still listed")
			}
		}

		res = h.do(http.MethodGet, "/api/v1/logs/"+created.ID.String(), cookie, nil)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("get deleted status = %d", res.StatusCode)
		}
		res.Body.Close()

		var deletedAt *time.Time
		if err := h.pool.QueryRow(ctx, `SELECT deleted_at FROM logs WHERE id = $1`, created.ID).Scan(&deletedAt); err != nil {
			t.Fatal(err)
		}
		if deletedAt == nil {
			t.Fatal("expected soft-delete")
		}
	})

	t.Run("GET /logs excludes meal drafts", func(t *testing.T) {
		var logID uuid.UUID
		err := h.pool.QueryRow(ctx, `
			INSERT INTO logs (user_id, type, logged_at, visibility)
			VALUES ($1, 'meal', now(), 'private')
			RETURNING id
		`, uid).Scan(&logID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.pool.Exec(ctx, `INSERT INTO meals (log_id, status) VALUES ($1, 'draft')`, logID); err != nil {
			t.Fatal(err)
		}
		res := h.do(http.MethodGet, "/api/v1/logs?type=meal", cookie, nil)
		var list struct {
			Items []logOut `json:"items"`
		}
		readJSON(t, res, &list)
		for _, it := range list.Items {
			if it.ID == logID {
				t.Fatal("draft meal listed")
			}
		}
	})

	t.Run("fishing lat bounds", func(t *testing.T) {
		tests := []struct {
			name    string
			lat     float64
			lng     float64
			wantErr bool
		}{
			{name: "too high", lat: 91, lng: 0, wantErr: true},
			{name: "too low", lat: -90.1, lng: 0, wantErr: true},
			{name: "lng high", lat: 0, lng: 181, wantErr: true},
			{name: "edge", lat: 90, lng: 180, wantErr: false},
			{name: "boyd", lat: 40.3, lng: -105.1, wantErr: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := h.do(http.MethodPost, "/api/v1/fishing", cookie, map[string]any{
					"water_body": "Boyd",
					"lat":        tt.lat,
					"lng":        tt.lng,
					"catches":    []map[string]any{{"species": "rainbow", "count": 2}},
				})
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
					t.Fatalf("status = %d code=%s", res.StatusCode, errCode(t, res))
				}
				var out logOut
				readJSON(t, res, &out)
				if out.Type != "fishing" || out.Fishing == nil || out.Fishing.Lat == nil || *out.Fishing.Lat != tt.lat {
					t.Fatalf("out = %+v", out)
				}
			})
		}
	})

	t.Run("custom_logs row", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/customs", cookie, map[string]any{
			"value": 8.5,
			"unit":  "mi",
		})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d code=%s", res.StatusCode, errCode(t, res))
		}
		var out logOut
		readJSON(t, res, &out)
		if out.Type != "custom" || out.Custom == nil || out.Custom.Value != 8.5 || out.Custom.Unit != "mi" {
			t.Fatalf("out = %+v", out)
		}
		var value float64
		var unit string
		if err := h.pool.QueryRow(ctx, `SELECT value, unit FROM custom_logs WHERE log_id = $1`, out.ID).Scan(&value, &unit); err != nil {
			t.Fatal(err)
		}
		if value != 8.5 || unit != "mi" {
			t.Fatalf("row value=%v unit=%q", value, unit)
		}
	})

	t.Run("type workout not strength", func(t *testing.T) {
		_, err := h.pool.Exec(ctx, `
			INSERT INTO logs (user_id, type, logged_at) VALUES ($1, 'strength', now())
		`, uid)
		if err == nil {
			t.Fatal("expected CHECK to reject type strength")
		}

		res := h.do(http.MethodPost, "/api/v1/workouts", cookie, map[string]any{
			"title": "Lower",
			"sets": []map[string]any{
				{"exercise": "squat", "reps": 5, "weight_kg": 125, "ordinal": 0},
			},
		})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d code=%s", res.StatusCode, errCode(t, res))
		}
		var out logOut
		readJSON(t, res, &out)
		if out.Type != "workout" {
			t.Fatalf("type = %q", out.Type)
		}
		if out.Workout == nil || out.Workout.Title != "Lower" || len(out.Workout.Sets) != 1 {
			t.Fatalf("workout = %+v", out.Workout)
		}
		var typ string
		if err := h.pool.QueryRow(ctx, `SELECT type FROM logs WHERE id = $1`, out.ID).Scan(&typ); err != nil {
			t.Fatal(err)
		}
		if typ != "workout" {
			t.Fatalf("stored type = %q", typ)
		}

		res = h.do(http.MethodGet, "/api/v1/logs?type=strength", cookie, nil)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("filter strength status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "invalid" {
			t.Fatalf("code = %q", got)
		}
	})
}

func TestLogsFromToFilter(t *testing.T) {
	h := newHarness(t)
	cookie, _ := h.login("range-" + uuid.NewString()[:8] + "@example.com")

	res := h.do(http.MethodPost, "/api/v1/habits", cookie, map[string]any{
		"status":    "done",
		"logged_at": "2026-09-01T12:00:00Z",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", res.StatusCode)
	}
	res.Body.Close()

	res = h.do(http.MethodGet, "/api/v1/logs?type=habit&from=2026-09-01T00:00:00Z&to=2026-09-02T00:00:00Z", cookie, nil)
	var list struct {
		Items []logOut `json:"items"`
	}
	readJSON(t, res, &list)
	if len(list.Items) != 1 {
		t.Fatalf("in range items = %d", len(list.Items))
	}

	res = h.do(http.MethodGet, "/api/v1/logs?type=habit&from=2026-09-10T00:00:00Z&to=2026-09-11T00:00:00Z", cookie, nil)
	readJSON(t, res, &list)
	if len(list.Items) != 0 {
		t.Fatalf("out of range items = %d", len(list.Items))
	}
}

func TestWeightBothKgAndLbRejected(t *testing.T) {
	h := newHarness(t)
	cookie, _ := h.login("both-" + uuid.NewString()[:8] + "@example.com")
	res := h.do(http.MethodPost, "/api/v1/weights", cookie, map[string]any{"kg": 80, "lb": 176})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := errCode(t, res); got != "invalid" {
		t.Fatalf("code = %q", got)
	}
}
