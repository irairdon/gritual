package media

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

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
	"github.com/irairdon/gritual/internal/httpx"
)

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
		AppBaseURL:    "http://localhost:8080",
		ViteDevOrigin: "http://localhost:5173",
		AuthDevLogin:  true,
		SessionSecret: bytes.Repeat([]byte("s"), 32),
		MediaDir:      t.TempDir(),
	}
	authAPI := auth.New(cfg, pool)
	mediaAPI := New(cfg, pool, authAPI.RequestUserID)
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
	router := httpx.NewRouter(ui, pool, func(r chi.Router) {
		authAPI.Mount(r)
		mediaAPI.Mount(r)
	}, mediaAPI.HandleGet)
	return &harness{t: t, h: router}
}

func (h *harness) register(email string) *http.Cookie {
	h.t.Helper()
	body, _ := json.Marshal(map[string]any{
		"email":        email,
		"password":     "password12",
		"display_name": "Pat",
		"dob":          "1990-01-15",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		h.t.Fatalf("register status = %d body=%s", res.StatusCode, b)
	}
	for _, ck := range res.Cookies() {
		if ck.Name == auth.SessionCookie {
			return ck
		}
	}
	h.t.Fatal("missing session cookie")
	return nil
}

func (h *harness) upload(cookie *http.Cookie, filename string, data []byte) *http.Response {
	h.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		h.t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		h.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Origin", "http://localhost:8080")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec.Result()
}

func (h *harness) patchMe(cookie *http.Cookie, body any) *http.Response {
	h.t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/me", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec.Result()
}

func (h *harness) get(path string, cookie *http.Cookie) *http.Response {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
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

func TestMedia(t *testing.T) {
	h := newHarness(t)
	cookieA := h.register("a-" + uuid.NewString()[:8] + "@example.com")
	cookieB := h.register("b-" + uuid.NewString()[:8] + "@example.com")

	t.Run("exif re-encoded on upload", func(t *testing.T) {
		in := jpegWithEXIF()
		res := h.upload(cookieA, "photo.jpg", in)
		defer res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("status = %d body=%s", res.StatusCode, b)
		}
		var obj objectJSON
		if err := json.NewDecoder(res.Body).Decode(&obj); err != nil {
			t.Fatal(err)
		}
		if obj.ContentType != "image/jpeg" || obj.ID == "" {
			t.Fatalf("obj = %+v", obj)
		}

		getRes := h.get("/media/"+obj.ID, cookieA)
		defer getRes.Body.Close()
		if getRes.StatusCode != http.StatusOK {
			t.Fatalf("GET owner status = %d", getRes.StatusCode)
		}
		if ct := getRes.Header.Get("Content-Type"); ct != "image/jpeg" {
			t.Fatalf("content-type = %q", ct)
		}
		stored, err := io.ReadAll(getRes.Body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := jpeg.Decode(bytes.NewReader(stored)); err != nil {
			t.Fatal(err)
		}
		if hasJPEGAPP1(stored) || bytes.Contains(stored, []byte("Exif")) || bytes.Contains(stored, []byte("GPS")) {
			t.Fatal("stored file kept EXIF/GPS")
		}
	})

	t.Run("non-image 400", func(t *testing.T) {
		res := h.upload(cookieA, "notes.txt", []byte("hello world"))
		defer res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "invalid" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("GET other user 404", func(t *testing.T) {
		res := h.upload(cookieA, "a.jpg", tinyJPEG())
		var obj objectJSON
		if err := json.NewDecoder(res.Body).Decode(&obj); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("upload status = %d", res.StatusCode)
		}

		other := h.get("/media/"+obj.ID, cookieB)
		defer other.Body.Close()
		if other.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", other.StatusCode)
		}
		anon := h.get("/media/"+obj.ID, nil)
		defer anon.Body.Close()
		if anon.StatusCode != http.StatusNotFound {
			t.Fatalf("anon status = %d, want 404", anon.StatusCode)
		}
	})

	t.Run("PATCH foreign avatar 403/404", func(t *testing.T) {
		res := h.upload(cookieA, "av.jpg", tinyJPEG())
		var obj objectJSON
		if err := json.NewDecoder(res.Body).Decode(&obj); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()

		patch := h.patchMe(cookieB, map[string]any{"avatar_media_id": obj.ID})
		defer patch.Body.Close()
		if patch.StatusCode != http.StatusForbidden && patch.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 403 or 404", patch.StatusCode)
		}

		own := h.patchMe(cookieA, map[string]any{
			"bio":             "hello",
			"height_cm":       180,
			"avatar_media_id": obj.ID,
		})
		defer own.Body.Close()
		if own.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(own.Body)
			t.Fatalf("own patch status = %d body=%s", own.StatusCode, b)
		}
		var me struct {
			Bio           *string  `json:"bio"`
			HeightCM      *float64 `json:"height_cm"`
			AvatarMediaID *string  `json:"avatar_media_id"`
		}
		if err := json.NewDecoder(own.Body).Decode(&me); err != nil {
			t.Fatal(err)
		}
		if me.Bio == nil || *me.Bio != "hello" {
			t.Fatalf("bio = %v", me.Bio)
		}
		if me.HeightCM == nil || *me.HeightCM != 180 {
			t.Fatalf("height = %v", me.HeightCM)
		}
		if me.AvatarMediaID == nil || *me.AvatarMediaID != obj.ID {
			t.Fatalf("avatar = %v", me.AvatarMediaID)
		}
	})
}

func tinyJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{1, 2, 3, 255})
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}
