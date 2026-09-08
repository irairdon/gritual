package httpx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRouter(t *testing.T) {
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<!doctype html><title>Gritual</title><p>spa</p>"),
		},
		"assets/app-deadbeef.js": &fstest.MapFile{
			Data: []byte("console.log(1)"),
		},
		"sw.js": &fstest.MapFile{
			Data: []byte("/* sw */"),
		},
		"manifest.webmanifest": &fstest.MapFile{
			Data: []byte(`{"name":"Gritual"}`),
		},
	}
	h := NewRouter(ui, nil, nil, nil)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCT     string
		wantBody   string
		wantCache  string
		notHTML    bool
		jsonOK     bool
		jsonCode   string
		echoReqID  string
	}{
		{
			name:       "healthz",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusOK,
			wantCT:     "text/plain",
			wantBody:   "ok",
		},
		{
			name:       "readyz",
			method:     http.MethodGet,
			path:       "/readyz",
			wantStatus: http.StatusOK,
			wantCT:     "text/plain",
			wantBody:   "ok",
		},
		{
			name:       "ping",
			method:     http.MethodGet,
			path:       "/api/v1/ping",
			wantStatus: http.StatusOK,
			wantCT:     "application/json",
			wantBody:   `{"ok":true}`,
			jsonOK:     true,
		},
		{
			name:       "api unknown is json not spa",
			method:     http.MethodGet,
			path:       "/api/v1/nope",
			wantStatus: http.StatusNotFound,
			wantCT:     "application/json",
			notHTML:    true,
			jsonCode:   "not_found",
		},
		{
			name:       "mcp get stub",
			method:     http.MethodGet,
			path:       "/mcp",
			wantStatus: http.StatusNotImplemented,
			wantCT:     "application/json",
			jsonCode:   "not_implemented",
			notHTML:    true,
		},
		{
			name:       "mcp post stub",
			method:     http.MethodPost,
			path:       "/mcp",
			wantStatus: http.StatusNotImplemented,
			wantCT:     "application/json",
			jsonCode:   "not_implemented",
			notHTML:    true,
		},
		{
			name:       "media stub",
			method:     http.MethodGet,
			path:       "/media/abc",
			wantStatus: http.StatusNotFound,
			notHTML:    true,
		},
		{
			name:       "aasa well-known json object",
			method:     http.MethodGet,
			path:       "/.well-known/apple-app-site-association",
			wantStatus: http.StatusOK,
			wantCT:     "application/json",
			wantBody:   "app.gritual.mobile",
			notHTML:    true,
		},
		{
			name:       "assetlinks well-known json array",
			method:     http.MethodGet,
			path:       "/.well-known/assetlinks.json",
			wantStatus: http.StatusOK,
			wantCT:     "application/json",
			wantBody:   "app.gritual.mobile",
			notHTML:    true,
		},
		{
			name:       "service worker is not spa",
			method:     http.MethodGet,
			path:       "/sw.js",
			wantStatus: http.StatusOK,
			wantCT:     "javascript",
			wantBody:   "/* sw */",
			wantCache:  "no-cache",
			notHTML:    true,
		},
		{
			name:       "manifest is not spa",
			method:     http.MethodGet,
			path:       "/manifest.webmanifest",
			wantStatus: http.StatusOK,
			wantCT:     "application/manifest+json",
			wantBody:   "Gritual",
			notHTML:    true,
		},
		{
			name:       "spa root",
			method:     http.MethodGet,
			path:       "/",
			wantStatus: http.StatusOK,
			wantCT:     "text/html",
			wantBody:   "Gritual",
			wantCache:  "no-store",
		},
		{
			name:       "spa fallback no extension",
			method:     http.MethodGet,
			path:       "/circles",
			wantStatus: http.StatusOK,
			wantCT:     "text/html",
			wantBody:   "Gritual",
			wantCache:  "no-store",
		},
		{
			name:       "hashed asset hit",
			method:     http.MethodGet,
			path:       "/assets/app-deadbeef.js",
			wantStatus: http.StatusOK,
			wantCT:     "javascript",
			wantBody:   "console.log(1)",
			wantCache:  "public, max-age=31536000, immutable",
		},
		{
			name:       "hashed asset miss is 404 not html",
			method:     http.MethodGet,
			path:       "/assets/missing-hash.js",
			wantStatus: http.StatusNotFound,
			notHTML:    true,
		},
		{
			name:       "extension miss is 404 not html",
			method:     http.MethodGet,
			path:       "/gone.css",
			wantStatus: http.StatusNotFound,
			notHTML:    true,
		},
		{
			name:       "other method unknown path 404",
			method:     http.MethodPost,
			path:       "/circles",
			wantStatus: http.StatusNotFound,
			notHTML:    true,
		},
		{
			name:       "echo request id",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusOK,
			echoReqID:  "test-req-id-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.echoReqID != "" {
				req.Header.Set("X-Request-Id", tt.echoReqID)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			res := rec.Result()
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", res.StatusCode, tt.wantStatus, body)
			}
			ct := res.Header.Get("Content-Type")
			if tt.wantCT != "" && !strings.Contains(ct, tt.wantCT) {
				t.Fatalf("content-type = %q, want substring %q", ct, tt.wantCT)
			}
			if tt.wantBody != "" && !strings.Contains(string(body), tt.wantBody) {
				t.Fatalf("body = %q, want substring %q", body, tt.wantBody)
			}
			if tt.wantCache != "" {
				if got := res.Header.Get("Cache-Control"); got != tt.wantCache {
					t.Fatalf("cache-control = %q, want %q", got, tt.wantCache)
				}
			}
			if tt.notHTML {
				if strings.Contains(ct, "text/html") || strings.Contains(string(body), "<html") || strings.Contains(string(body), "<!doctype") {
					t.Fatalf("got HTML for miss: ct=%q body=%q", ct, body)
				}
			}
			if tt.jsonOK {
				var payload struct {
					OK bool `json:"ok"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				if !payload.OK {
					t.Fatalf("ok = false")
				}
			}
			if tt.jsonCode != "" {
				var payload errorResponse
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Error.Code != tt.jsonCode {
					t.Fatalf("error.code = %q, want %q", payload.Error.Code, tt.jsonCode)
				}
			}
			reqID := res.Header.Get("X-Request-Id")
			if reqID == "" {
				t.Fatal("missing X-Request-Id")
			}
			if tt.echoReqID != "" && reqID != tt.echoReqID {
				t.Fatalf("X-Request-Id = %q, want %q", reqID, tt.echoReqID)
			}
		})
	}
}

func TestWellKnownNativeAppIDs(t *testing.T) {
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Gritual</title><p>spa</p>")},
	}
	h := NewRouter(ui, nil, nil, nil)

	t.Run("aasa", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/apple-app-site-association", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.Bytes())
		}
		var doc struct {
			Applinks struct {
				Details []struct {
					AppID string   `json:"appID"`
					Paths []string `json:"paths"`
				} `json:"details"`
			} `json:"applinks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range doc.Applinks.Details {
			if d.AppID == "app.gritual.mobile" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing app.gritual.mobile: %s", rec.Body.Bytes())
		}
	})

	t.Run("assetlinks", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.Bytes())
		}
		var docs []struct {
			Target struct {
				Namespace   string `json:"namespace"`
				PackageName string `json:"package_name"`
			} `json:"target"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range docs {
			if d.Target.Namespace == "android_app" && d.Target.PackageName == "app.gritual.mobile" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing app.gritual.mobile: %s", rec.Body.Bytes())
		}
	})
}

type stubPinger struct{ err error }

func (s stubPinger) Ping(context.Context) error { return s.err }

func TestReadyz(t *testing.T) {
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")},
	}
	tests := []struct {
		name       string
		db         pinger
		wantStatus int
		wantBody   string
	}{
		{name: "no db", db: nil, wantStatus: http.StatusOK, wantBody: "ok"},
		{name: "typed nil pointer", db: (*stubPinger)(nil), wantStatus: http.StatusOK, wantBody: "ok"},
		{name: "ping ok", db: stubPinger{}, wantStatus: http.StatusOK, wantBody: "ok"},
		{name: "ping fail", db: stubPinger{err: io.ErrUnexpectedEOF}, wantStatus: http.StatusServiceUnavailable, wantBody: "not ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			rec := httptest.NewRecorder()
			NewRouter(ui, tt.db, nil, nil).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want substring %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestMetricsMux(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	MetricsMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
}
