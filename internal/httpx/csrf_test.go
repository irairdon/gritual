package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginCSRF(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := OriginCSRF([]string{"https://gritual.fit", "http://localhost:5173"}, "gritual_session")(next)

	tests := []struct {
		name       string
		method     string
		origin     string
		cookie     bool
		bearer     bool
		wantStatus int
	}{
		{name: "cookie mutate good origin", method: http.MethodPost, origin: "https://gritual.fit", cookie: true, wantStatus: http.StatusNoContent},
		{name: "cookie mutate vite origin", method: http.MethodPost, origin: "http://localhost:5173", cookie: true, wantStatus: http.StatusNoContent},
		{name: "cookie mutate bad origin", method: http.MethodPost, origin: "http://evil.example", cookie: true, wantStatus: http.StatusForbidden},
		{name: "cookie mutate missing origin", method: http.MethodPost, cookie: true, wantStatus: http.StatusForbidden},
		{name: "cookie mutate localhost no port", method: http.MethodPost, origin: "http://localhost", cookie: true, wantStatus: http.StatusForbidden},
		{name: "cookie mutate capacitor", method: http.MethodPost, origin: "capacitor://localhost", cookie: true, wantStatus: http.StatusForbidden},
		{name: "cookie mutate ionic", method: http.MethodPost, origin: "ionic://localhost", cookie: true, wantStatus: http.StatusForbidden},
		{name: "bearer skips csrf", method: http.MethodPost, origin: "http://evil.example", bearer: true, wantStatus: http.StatusNoContent},
		{name: "no cookie mutate missing origin", method: http.MethodPost, wantStatus: http.StatusNoContent},
		{name: "cookie get missing origin", method: http.MethodGet, cookie: true, wantStatus: http.StatusNoContent},
		{name: "cookie get bad origin", method: http.MethodGet, origin: "http://evil.example", cookie: true, wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/v1/me", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.cookie {
				req.AddCookie(&http.Cookie{Name: "gritual_session", Value: "tok"})
			}
			if tt.bearer {
				req.Header.Set("Authorization", "Bearer abc")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				body, _ := io.ReadAll(rec.Body)
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, body)
			}
		})
	}
}
