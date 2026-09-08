package chat

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

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/ai"
	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/challenges"
	"github.com/irairdon/gritual/internal/circles"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/logs"
	"github.com/irairdon/gritual/internal/mcp"
	"github.com/irairdon/gritual/internal/meals"
	"github.com/irairdon/gritual/internal/media"
	"github.com/irairdon/gritual/internal/rituals"
	"github.com/irairdon/gritual/internal/tools"
)

const origin = "http://localhost:8080"

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
	stub := &ai.Stub{ChatRounds: []ai.ChatRound{{Text: "Hey."}}}
	cfg := config.Config{
		AppBaseURL:    origin,
		ViteDevOrigin: "http://localhost:5173",
		AuthDevLogin:  true,
		SessionSecret: bytes.Repeat([]byte("s"), 32),
		MediaDir:      t.TempDir(),
		AIEnabled:     true,
		XAIAPIKey:     "test-key",
		XAIModel:      "grok-4.5",
		MCPEnabled:    true,
	}
	authAPI := auth.New(cfg, pool)
	circAPI := circles.New(cfg, pool)
	ritAPI := rituals.New(cfg, pool)
	logAPI := logs.New(cfg, pool)
	mediaAPI := media.New(cfg, pool, authAPI.RequestUserID)
	mealAPI := meals.New(cfg, pool, mediaAPI, stub)
	chalAPI := challenges.New(cfg, pool)
	runner := tools.New(logAPI, mealAPI, ritAPI, chalAPI, func(ctx context.Context, userID uuid.UUID) (string, error) {
		var tz string
		err := pool.QueryRow(ctx, `SELECT tz FROM users WHERE id = $1`, userID).Scan(&tz)
		return tz, err
	})
	chatAPI := New(cfg, pool, stub, runner)
	mcpH := mcp.New(cfg, authAPI, runner)
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
	return &harness{
		t:    t,
		pool: pool,
		stub: stub,
		cfg:  cfg,
		h: httpx.NewRouterMCP(ui, pool, func(r chi.Router) {
			authAPI.Mount(r)
			circAPI.Mount(r)
			ritAPI.Mount(r)
			logAPI.Mount(r)
			mediaAPI.Mount(r)
			mealAPI.Mount(r)
			chalAPI.Mount(r)
			chatAPI.Mount(r)
		}, mediaAPI.HandleGet, mcpH),
	}
}

func (h *harness) do(method, path string, cookie *http.Cookie, body any) *http.Response {
	return h.doOrigin(method, path, cookie, origin, "", body)
}

func (h *harness) doOrigin(method, path string, cookie *http.Cookie, originHeader, bearer string, body any) *http.Response {
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
	if originHeader != "" {
		req.Header.Set("Origin", originHeader)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
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

func (h *harness) consent(cookie *http.Cookie) {
	h.t.Helper()
	res := h.do(http.MethodPost, "/api/v1/me/ai-consent", cookie, map[string]any{})
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("consent status = %d", res.StatusCode)
	}
	res.Body.Close()
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

func parseSSE(body string) []struct{ Event, Data string } {
	var out []struct{ Event, Data string }
	blocks := strings.Split(body, "\n\n")
	for _, b := range blocks {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		var ev, data string
		for _, line := range strings.Split(b, "\n") {
			if strings.HasPrefix(line, "event:") {
				ev = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			if strings.HasPrefix(line, "data:") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if ev != "" {
			out = append(out, struct{ Event, Data string }{ev, data})
		}
	}
	return out
}

func TestCoachChatAndMCP(t *testing.T) {
	h := newHarness(t)
	cookie := h.login("coach-" + uuid.NewString()[:8] + "@example.com")

	t.Run("chat requires consent", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/ai/chat", cookie, map[string]any{"message": "hi"})
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "ai_consent_required" {
			t.Fatalf("code = %q", got)
		}
	})

	h.consent(cookie)

	t.Run("sse headers and stub tokens", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/ai/chat", cookie, map[string]any{"message": "hello"})
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
			t.Fatalf("content-type = %q", ct)
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("cache-control = %q", res.Header.Get("Cache-Control"))
		}
		if res.Header.Get("X-Accel-Buffering") != "no" {
			t.Fatalf("x-accel-buffering = %q", res.Header.Get("X-Accel-Buffering"))
		}
		b, _ := io.ReadAll(res.Body)
		events := parseSSE(string(b))
		var sawToken, sawDone bool
		for _, e := range events {
			if e.Event == "token" {
				sawToken = true
			}
			if e.Event == "done" {
				sawDone = true
			}
		}
		if !sawToken || !sawDone {
			t.Fatalf("events = %+v", events)
		}
		if h.stub.ChatCalls < 1 {
			t.Fatal("expected stub chat call")
		}
		if h.stub.LastChat.User == "" {
			t.Fatal("expected hashed user")
		}
		got := map[string]bool{}
		for _, spec := range h.stub.LastChat.Tools {
			got[spec.Name] = true
		}
		for _, name := range ai.ToolNames {
			if !got[name] {
				t.Fatalf("missing tool %s", name)
			}
		}
		if got["create_goal"] || got["fetch_today_summary"] {
			t.Fatal("aliases must not be registered")
		}
	})

	t.Run("tool log_weight as session user", func(t *testing.T) {
		h.stub.ChatCalls = 0
		h.stub.ChatRounds = []ai.ChatRound{
			{ToolCalls: []ai.ToolCall{{ID: "c1", Name: ai.ToolLogWeight, Args: `{"lb":204}`}}},
			{Text: "Logged your weight."},
		}
		res := h.do(http.MethodPost, "/api/v1/ai/chat", cookie, map[string]any{"message": "I weigh 204 lb"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d body=%s", res.StatusCode, b)
		}
		events := parseSSE(string(b))
		var sawCall, sawResult bool
		var logID string
		for _, e := range events {
			if e.Event == "tool_call" && strings.Contains(e.Data, ai.ToolLogWeight) {
				sawCall = true
			}
			if e.Event == "tool_result" {
				sawResult = true
				var payload struct {
					OK    bool   `json:"ok"`
					LogID string `json:"log_id"`
				}
				_ = json.Unmarshal([]byte(e.Data), &payload)
				if !payload.OK {
					t.Fatalf("tool_result = %s", e.Data)
				}
				logID = payload.LogID
			}
		}
		if !sawCall || !sawResult || logID == "" {
			t.Fatalf("events = %+v", events)
		}

		res = h.do(http.MethodGet, "/api/v1/logs?type=weight", cookie, nil)
		var list struct {
			Items []struct {
				ID     string `json:"id"`
				Source string `json:"source"`
				Weight *struct {
					LB float64 `json:"lb"`
				} `json:"weight"`
			} `json:"items"`
		}
		readJSON(t, res, &list)
		found := false
		for _, it := range list.Items {
			if it.ID == logID {
				found = true
				if it.Source != "ai_chat" {
					t.Fatalf("source = %q", it.Source)
				}
			}
		}
		if !found {
			t.Fatal("weight log missing from GET /logs")
		}
	})

	t.Run("GET /logs still no drafts", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var userID uuid.UUID
		if err := h.pool.QueryRow(ctx, `SELECT id FROM users WHERE email LIKE 'coach-%' LIMIT 1`).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		var logID uuid.UUID
		if err := h.pool.QueryRow(ctx, `
			INSERT INTO logs (user_id, type, logged_at, visibility) VALUES ($1, 'meal', now(), 'private') RETURNING id
		`, userID).Scan(&logID); err != nil {
			t.Fatal(err)
		}
		if _, err := h.pool.Exec(ctx, `INSERT INTO meals (log_id, status) VALUES ($1, 'draft')`, logID); err != nil {
			t.Fatal(err)
		}
		res := h.do(http.MethodGet, "/api/v1/logs", cookie, nil)
		var list struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		readJSON(t, res, &list)
		for _, it := range list.Items {
			if it.ID == logID.String() {
				t.Fatal("draft listed in GET /logs")
			}
		}
	})

	t.Run("conversations get delete", func(t *testing.T) {
		h.stub.ChatRounds = []ai.ChatRound{{Text: "later"}}
		res := h.do(http.MethodPost, "/api/v1/ai/chat", cookie, map[string]any{"message": "ping"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		var conv string
		for _, e := range parseSSE(string(b)) {
			if e.Event == "done" {
				var p struct {
					ConversationID string `json:"conversation_id"`
				}
				_ = json.Unmarshal([]byte(e.Data), &p)
				conv = p.ConversationID
			}
		}
		if conv == "" {
			t.Fatal("missing conversation id")
		}
		res = h.do(http.MethodGet, "/api/v1/ai/conversations", cookie, nil)
		var list struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		readJSON(t, res, &list)
		if len(list.Items) == 0 {
			t.Fatal("expected conversations")
		}
		res = h.do(http.MethodGet, "/api/v1/ai/conversations/"+conv, cookie, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("get status = %d", res.StatusCode)
		}
		res.Body.Close()
		res = h.do(http.MethodDelete, "/api/v1/ai/conversations/"+conv, cookie, nil)
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("delete status = %d", res.StatusCode)
		}
		res.Body.Close()
		res = h.do(http.MethodGet, "/api/v1/ai/conversations/"+conv, cookie, nil)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("get deleted status = %d", res.StatusCode)
		}
		res.Body.Close()
	})

	var pat string
	t.Run("create PAT shown once", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/me/tokens", cookie, map[string]any{"name": "cursor"})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d", res.StatusCode)
		}
		var created struct {
			ID     string `json:"id"`
			Token  string `json:"token"`
			Prefix string `json:"prefix"`
		}
		readJSON(t, res, &created)
		if !strings.HasPrefix(created.Token, "grt_live_") {
			t.Fatalf("token = %q", created.Token)
		}
		pat = created.Token
		res = h.do(http.MethodGet, "/api/v1/me/tokens", cookie, nil)
		var list struct {
			Items []map[string]any `json:"items"`
		}
		readJSON(t, res, &list)
		if len(list.Items) != 1 {
			t.Fatalf("items = %d", len(list.Items))
		}
		if _, ok := list.Items[0]["token"]; ok {
			t.Fatal("raw token leaked in list")
		}
	})

	t.Run("MCP origin absent PAT ok", func(t *testing.T) {
		res := h.doOrigin(http.MethodPost, "/mcp", nil, "", pat, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/list",
		})
		if res.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("status = %d body=%s", res.StatusCode, b)
		}
		var rpc struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		readJSON(t, res, &rpc)
		got := map[string]bool{}
		for _, tl := range rpc.Result.Tools {
			got[tl.Name] = true
		}
		for _, name := range ai.ToolNames {
			if !got[name] {
				t.Fatalf("mcp missing tool %s", name)
			}
		}
		if got["create_goal"] {
			t.Fatal("create_goal alias present")
		}
	})

	t.Run("MCP origin bad rejected", func(t *testing.T) {
		res := h.doOrigin(http.MethodPost, "/mcp", nil, "https://evil.example", pat, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
		})
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if got := errCode(t, res); got != "forbidden" {
			t.Fatalf("code = %q", got)
		}
	})

	t.Run("MCP origin allowlist with PAT ok", func(t *testing.T) {
		res := h.doOrigin(http.MethodPost, "/mcp", nil, origin, pat, map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "initialize",
		})
		if res.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("status = %d body=%s", res.StatusCode, b)
		}
		res.Body.Close()
	})
}

func TestToolNames(t *testing.T) {
	want := []string{
		"create_ritual", "get_today_macros", "get_challenge_standings",
		"log_meal", "log_activity", "log_weight",
	}
	if len(ai.ToolNames) != len(want) {
		t.Fatalf("ToolNames = %v", ai.ToolNames)
	}
	got := map[string]bool{}
	for _, n := range ai.ToolNames {
		got[n] = true
	}
	for _, n := range want {
		if !got[n] {
			t.Fatalf("missing %s", n)
		}
	}
	if ai.KnownTool("create_goal") || ai.KnownTool("fetch_today_summary") {
		t.Fatal("aliases must not be known")
	}
}
