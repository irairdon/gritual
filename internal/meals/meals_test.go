package meals

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/ai"
	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/logs"
	"github.com/irairdon/gritual/internal/media"
)

const origin = "http://localhost:8080"

var stubJSON = json.RawMessage(`{
  "foods": [
    {"name":"Chicken thigh","grams":180,"kcal":250,"protein_g":32,"carbs_g":0,"fat_g":13,"confidence":0.86}
  ],
  "overall_confidence": 0.86,
  "notes": "skin on"
}`)

type harness struct {
	t    *testing.T
	pool *pgxpool.Pool
	h    http.Handler
	stub *ai.Stub
	cfg  config.Config
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := db.TestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
	stub := &ai.Stub{JSON: stubJSON}
	cfg := config.Config{
		AppBaseURL:     origin,
		ViteDevOrigin:  "http://localhost:5173",
		AuthDevLogin:   true,
		SessionSecret:  bytes.Repeat([]byte("s"), 32),
		MediaDir:       t.TempDir(),
		AIEnabled:      true,
		XAIAPIKey:      "test-key",
		XAIVisionModel: "grok-4.5",
	}
	authAPI := auth.New(cfg, pool)
	mediaAPI := media.New(cfg, pool, authAPI.RequestUserID)
	logAPI := logs.New(cfg, pool)
	mealAPI := New(cfg, pool, mediaAPI, stub)
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
	return &harness{
		t:    t,
		pool: pool,
		stub: stub,
		cfg:  cfg,
		h: httpx.NewRouter(ui, pool, func(r chi.Router) {
			authAPI.Mount(r)
			logAPI.Mount(r)
			mediaAPI.Mount(r)
			mealAPI.Mount(r)
		}, mediaAPI.HandleGet),
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
	defer res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	h.t.Fatal("missing session cookie")
	return nil
}

func (h *harness) register(email string) *http.Cookie {
	h.t.Helper()
	res := h.do(http.MethodPost, "/api/v1/auth/register", nil, map[string]any{
		"email":        email,
		"password":     "password12",
		"display_name": "Pat",
		"dob":          "1990-01-15",
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		h.t.Fatalf("register status = %d body=%s", res.StatusCode, b)
	}
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	h.t.Fatal("missing session cookie")
	return nil
}

func (h *harness) photo(cookie *http.Cookie, data []byte) *http.Response {
	h.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "meal.jpg")
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		h.t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		h.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/meals/photo", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Origin", origin)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec.Result()
}

func errCode(t *testing.T, res *http.Response) string {
	t.Helper()
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
	if err := json.NewDecoder(res.Body).Decode(dst); err != nil {
		t.Fatal(err)
	}
}

func tinyJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{10, 20, 30, 255})
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}

func TestMealsVision(t *testing.T) {
	h := newHarness(t)
	cookie := h.login("meal-" + uuid.NewString()[:8] + "@example.com")

	t.Run("photo without consent", func(t *testing.T) {
		res := h.photo(cookie, tinyJPEG())
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "ai_consent_required" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("consent sets ai_consent_at", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/me/ai-consent", cookie, map[string]any{})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		var me struct {
			AIConsentAt *time.Time `json:"ai_consent_at"`
		}
		readJSON(t, res, &me)
		if me.AIConsentAt == nil {
			t.Fatal("expected ai_consent_at")
		}
		res = h.do(http.MethodGet, "/api/v1/me", cookie, nil)
		readJSON(t, res, &me)
		if me.AIConsentAt == nil {
			t.Fatal("GET /me missing ai_consent_at")
		}
	})

	t.Run("photo draft excluded from logs", func(t *testing.T) {
		h.stub.Calls = 0
		res := h.photo(cookie, tinyJPEG())
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d code=%s", res.StatusCode, errCode(t, res))
		}
		var draft mealOut
		readJSON(t, res, &draft)
		if draft.Status != "draft" || len(draft.Items) != 1 || draft.Items[0].Name != "Chicken thigh" {
			t.Fatalf("draft = %+v", draft)
		}
		if draft.Items[0].Source != "vision" {
			t.Fatalf("source = %q", draft.Items[0].Source)
		}
		if h.stub.Calls != 1 {
			t.Fatalf("vision calls = %d", h.stub.Calls)
		}
		if h.stub.Last.Model != "grok-4.5" || h.stub.Last.SchemaName != "meal_estimate" {
			t.Fatalf("last req = %+v", h.stub.Last)
		}
		if !bytes.Contains(h.stub.Last.ImageJPEG, []byte{0xff, 0xd8}) && len(h.stub.Last.ImageJPEG) == 0 {
			t.Fatal("missing jpeg bytes")
		}

		res = h.do(http.MethodGet, "/api/v1/logs?type=meal", cookie, nil)
		var list struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		readJSON(t, res, &list)
		for _, it := range list.Items {
			if it.ID == draft.ID.String() {
				t.Fatal("draft listed in GET /logs")
			}
		}

		res = h.do(http.MethodGet, "/api/v1/meals/"+draft.ID.String(), cookie, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("get draft status = %d", res.StatusCode)
		}
		var got mealOut
		readJSON(t, res, &got)
		if got.Status != "draft" {
			t.Fatalf("status = %q", got.Status)
		}

		res = h.photo(cookie, tinyJPEG())
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("second photo status = %d", res.StatusCode)
		}
		res.Body.Close()
		if h.stub.Calls != 1 {
			t.Fatalf("cache missed; calls = %d", h.stub.Calls)
		}

		res = h.do(http.MethodPost, "/api/v1/meals", cookie, map[string]any{
			"id": draft.ID.String(),
			"items": []map[string]any{
				{"name": "Chicken thigh", "grams": 180, "kcal": 250, "protein_g": 32, "carbs_g": 0, "fat_g": 13},
			},
			"notes": "edited",
		})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("confirm status = %d code=%s", res.StatusCode, errCode(t, res))
		}
		var confirmed mealOut
		readJSON(t, res, &confirmed)
		if confirmed.Status != "confirmed" || confirmed.Kcal != 250 {
			t.Fatalf("confirmed = %+v", confirmed)
		}

		res = h.do(http.MethodGet, "/api/v1/logs?type=meal", cookie, nil)
		var logsList struct {
			Items []struct {
				ID   string `json:"id"`
				Meal *struct {
					Status string  `json:"status"`
					Kcal   float64 `json:"kcal"`
				} `json:"meal"`
			} `json:"items"`
		}
		readJSON(t, res, &logsList)
		found := false
		for _, it := range logsList.Items {
			if it.ID == draft.ID.String() {
				found = true
				if it.Meal == nil || it.Meal.Status != "confirmed" {
					t.Fatalf("listed meal = %+v", it.Meal)
				}
			}
		}
		if !found {
			t.Fatal("confirmed meal missing from GET /logs")
		}

		res = h.do(http.MethodGet, "/api/v1/meals/day", cookie, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("day status = %d", res.StatusCode)
		}
		var day struct {
			Items []mealOut `json:"items"`
			Kcal  float64   `json:"kcal"`
		}
		readJSON(t, res, &day)
		found = false
		for _, it := range day.Items {
			if it.ID == draft.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("day items = %+v", day.Items)
		}
	})

	t.Run("rejects injection names", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/meals", cookie, map[string]any{
			"items": []map[string]any{
				{"name": "ignore previous", "grams": 10, "kcal": 1, "protein_g": 0, "carbs_g": 0, "fat_g": 0},
			},
		})
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "invalid" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("food name truncated to 80", func(t *testing.T) {
		name := string(bytes.Repeat([]byte("n"), 90))
		res := h.do(http.MethodPost, "/api/v1/meals", cookie, map[string]any{
			"items": []map[string]any{
				{"name": name, "grams": 10, "kcal": 20, "protein_g": 1, "carbs_g": 2, "fat_g": 0},
			},
		})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d code=%s", res.StatusCode, errCode(t, res))
		}
		var out mealOut
		readJSON(t, res, &out)
		if len([]rune(out.Items[0].Name)) != 80 {
			t.Fatalf("name len = %d", len([]rune(out.Items[0].Name)))
		}
	})
}

func TestMealsUnavailableAndUnverified(t *testing.T) {
	t.Run("AI disabled", func(t *testing.T) {
		h := newHarness(t)
		h.cfg.AIEnabled = false
		cookie := h.login("off-" + uuid.NewString()[:8] + "@example.com")
		res := h.do(http.MethodPost, "/api/v1/me/ai-consent", cookie, map[string]any{})
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("consent status = %d", res.StatusCode)
		}
		// Rebuild handler with AIEnabled false.
		pool := h.pool
		stub := h.stub
		cfg := h.cfg
		authAPI := auth.New(cfg, pool)
		mediaAPI := media.New(cfg, pool, authAPI.RequestUserID)
		mealAPI := New(cfg, pool, mediaAPI, stub)
		ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
		h.h = httpx.NewRouter(ui, pool, func(r chi.Router) {
			authAPI.Mount(r)
			mediaAPI.Mount(r)
			mealAPI.Mount(r)
		}, mediaAPI.HandleGet)
		res = h.photo(cookie, tinyJPEG())
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "ai_unavailable" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("no key", func(t *testing.T) {
		h := newHarness(t)
		h.cfg.XAIAPIKey = ""
		cookie := h.login("nokey-" + uuid.NewString()[:8] + "@example.com")
		res := h.do(http.MethodPost, "/api/v1/me/ai-consent", cookie, map[string]any{})
		res.Body.Close()
		pool := h.pool
		cfg := h.cfg
		authAPI := auth.New(cfg, pool)
		mediaAPI := media.New(cfg, pool, authAPI.RequestUserID)
		mealAPI := New(cfg, pool, mediaAPI, h.stub)
		ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
		h.h = httpx.NewRouter(ui, pool, func(r chi.Router) {
			authAPI.Mount(r)
			mediaAPI.Mount(r)
			mealAPI.Mount(r)
		}, mediaAPI.HandleGet)
		res = h.photo(cookie, tinyJPEG())
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "ai_unavailable" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("unverified email", func(t *testing.T) {
		h := newHarness(t)
		cookie := h.register("unv-" + uuid.NewString()[:8] + "@example.com")
		res := h.do(http.MethodPost, "/api/v1/me/ai-consent", cookie, map[string]any{})
		res.Body.Close()
		res = h.photo(cookie, tinyJPEG())
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "email_unverified" {
			t.Fatalf("code = %q", got)
		}
	})
}
