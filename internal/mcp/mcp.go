package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/irairdon/gritual/internal/ai"
	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/tools"
)

const (
	protocolVersion = "2025-03-26"
	sourceMCP       = "mcp"
	perMin          = 60
)

type Handler struct {
	cfg   config.Config
	auth  *auth.API
	tools *tools.Runner
	limit *limiter
}

func New(cfg config.Config, authAPI *auth.API, runner *tools.Runner) *Handler {
	return &Handler{cfg: cfg, auth: authAPI, tools: runner, limit: newLimiter()}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.MCPEnabled {
		http.NotFound(w, r)
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" && !config.OriginAllowed(origin, h.cfg.AllowedOrigins()) {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "invalid origin")
		return
	}

	raw := bearer(r)
	if raw == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "PAT required")
		return
	}
	u, tokenID, err := h.auth.LookupPAT(r.Context(), raw)
	if err != nil {
		slog.Error("mcp pat", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if u == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
		return
	}
	if !u.EmailVerified {
		httpx.WriteError(w, http.StatusForbidden, "email_unverified", "email not verified")
		return
	}
	if u.AIConsentAt == nil {
		httpx.WriteError(w, http.StatusForbidden, "ai_consent_required", "AI consent required")
		return
	}
	if h.limit.atLimit(tokenID.String()) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many MCP requests")
		return
	}

	if r.Method == http.MethodGet {
		if !h.limit.allow(tokenID.String()) {
			httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many MCP requests")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, ": connected\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if !h.limit.allow(tokenID.String()) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many MCP requests")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, -32600, "invalid request")
		return
	}
	if req.Method == "" || strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rpcErr := h.dispatch(r, u, req)
	if rpcErr != nil {
		writeRPCError(w, req.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func (h *Handler) dispatch(r *http.Request, u *auth.User, req rpcRequest) (any, *rpcErr) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "gritual", "version": "1.0.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		specs := ai.ToolSpecs()
		toolsOut := make([]map[string]any, 0, len(specs))
		for _, t := range specs {
			toolsOut = append(toolsOut, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": json.RawMessage(t.Parameters),
			})
		}
		return map[string]any{"tools": toolsOut}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return nil, &rpcErr{Code: -32602, Message: "invalid params"}
			}
		}
		if !ai.KnownTool(p.Name) {
			return map[string]any{
				"content": []map[string]any{{"type": "text", "text": "unknown tool"}},
				"isError": true,
			}, nil
		}
		tr := h.tools.Call(r.Context(), u.ID, sourceMCP, p.Name, p.Arguments)
		b, _ := json.Marshal(tr)
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(b)}},
			"isError": !tr.OK,
		}, nil
	case "resources/list":
		return map[string]any{
			"resources": []map[string]any{{
				"uri":  "gritual://me/today",
				"name": "today",
			}},
		}, nil
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.URI != "gritual://me/today" {
			return nil, &rpcErr{Code: -32602, Message: "unknown resource"}
		}
		tr := h.tools.Call(r.Context(), u.ID, sourceMCP, ai.ToolGetTodayMacros, json.RawMessage(`{}`))
		b, _ := json.Marshal(tr)
		return map[string]any{
			"contents": []map[string]any{{"uri": p.URI, "mimeType": "application/json", "text": string(b)}},
		}, nil
	default:
		return nil, &rpcErr{Code: -32601, Message: "method not found"}
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   rpcErr{Code: code, Message: message},
	})
}

func bearer(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) < 7 || !strings.EqualFold(h[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter {
	return &limiter{hits: map[string][]time.Time{}}
}

func (l *limiter) pruneLocked(key string, now time.Time) []time.Time {
	cutoff := now.Add(-time.Minute)
	cur := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			cur = append(cur, t)
		}
	}
	if len(cur) == 0 {
		delete(l.hits, key)
		return nil
	}
	l.hits[key] = cur
	return cur
}

func (l *limiter) atLimit(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pruneLocked(key, time.Now())) >= perMin
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cur := l.pruneLocked(key, now)
	if len(cur) >= perMin {
		return false
	}
	l.hits[key] = append(cur, now)
	return true
}
