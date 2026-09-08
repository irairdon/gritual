package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/ai"
	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/tools"
)

const (
	handlerTimeout = 60 * time.Second
	sourceChat     = "ai_chat"
	chatPerHour    = 60
)

type API struct {
	cfg   config.Config
	pool  *pgxpool.Pool
	ai    ai.Provider
	tools *tools.Runner
	limit *limiter
}

func New(cfg config.Config, pool *pgxpool.Pool, provider ai.Provider, runner *tools.Runner) *API {
	return &API{cfg: cfg, pool: pool, ai: provider, tools: runner, limit: newLimiter()}
}

func (a *API) Mount(r chi.Router) {
	r.Post("/ai/chat", requireAI(a.handleChat))
	r.Get("/ai/conversations", requireAI(a.handleList))
	r.Get("/ai/conversations/{id}", requireAI(a.handleGet))
	r.Delete("/ai/conversations/{id}", requireAI(a.handleDelete))
}

func requireAI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFrom(r.Context())
		if u == nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
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
		next(w, r)
	}
}

type chatReq struct {
	ConversationID *uuid.UUID `json:"conversation_id"`
	Message        string     `json:"message"`
}

func (a *API) handleChat(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if !a.cfg.AIEnabled || a.ai == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "ai_unavailable", "SpaceXAI unavailable")
		return
	}
	if a.limit.atLimit(u.ID.String()) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many chat requests")
		return
	}

	var req chatReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	if req.Message == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "message required")
		return
	}
	if !a.limit.allow(u.ID.String()) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many chat requests")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), handlerTimeout)
	defer cancel()

	convID, history, err := a.loadOrCreate(ctx, u.ID, req.ConversationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "conversation not found")
			return
		}
		slog.Error("chat conv", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if err := a.insertMessage(ctx, convID, dbMsg{Role: "user", Content: req.Message}); err != nil {
		slog.Error("chat user msg", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	writeEvent := func(event string, data any) {
		b, _ := json.Marshal(data)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	msgs := []ai.ChatMessage{{Role: "system", Content: ai.CoachSystem}}
	msgs = append(msgs, history...)
	msgs = append(msgs, ai.ChatMessage{Role: "user", Content: req.Message})

	model := a.cfg.XAIModel
	if model == "" {
		model = ai.DefaultModel
	}
	userHash := ai.HashUser(u.ID, a.cfg.SessionSecret)

	for round := 0; round < ai.ChatMaxRounds; round++ {
		res, err := a.ai.ChatStream(ctx, ai.ChatRequest{
			User:      userHash,
			Model:     model,
			Messages:  msgs,
			Tools:     ai.ToolSpecs(),
			MaxTokens: ai.ChatMaxTokens,
		}, func(d ai.StreamDelta) error {
			if d.Text != "" {
				writeEvent("token", map[string]string{"text": d.Text})
			}
			return nil
		})
		if err != nil {
			slog.Error("chat stream", "err", err)
			writeEvent("error", map[string]string{"code": "ai_unavailable", "message": "SpaceXAI error"})
			return
		}
		if len(res.ToolCalls) == 0 {
			if res.Text != "" {
				_ = a.insertMessage(ctx, convID, dbMsg{
					Role: "assistant", Content: res.Text, TokensIn: res.TokensIn, TokensOut: res.TokensOut,
				})
			}
			break
		}
		asst := ai.ChatMessage{Role: "assistant", Content: res.Text, ToolCalls: res.ToolCalls}
		msgs = append(msgs, asst)
		_ = a.insertMessage(ctx, convID, dbMsg{
			Role: "assistant", Content: res.Text, ToolCalls: res.ToolCalls,
			TokensIn: res.TokensIn, TokensOut: res.TokensOut,
		})
		for _, tc := range res.ToolCalls {
			writeEvent("tool_call", map[string]any{"id": tc.ID, "name": tc.Name, "args": jsonRaw(tc.Args)})
			tr := a.tools.Call(ctx, u.ID, sourceChat, tc.Name, json.RawMessage(tc.Args))
			payload, _ := json.Marshal(tr)
			writeEvent("tool_result", toolResultEvent(tc.ID, tr))
			_ = a.insertMessage(ctx, convID, dbMsg{
				Role: "tool", Content: string(payload), ToolName: tc.Name, ToolCallID: tc.ID,
			})
			msgs = append(msgs, ai.ChatMessage{Role: "tool", Content: string(payload), ToolCallID: tc.ID, Name: tc.Name})
		}
	}
	writeEvent("done", map[string]string{"conversation_id": convID.String()})
}

func jsonRaw(s string) any {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return s
}

func toolResultEvent(id string, tr tools.Result) map[string]any {
	out := map[string]any{"id": id, "ok": tr.OK}
	if tr.LogID != nil {
		out["log_id"] = tr.LogID.String()
	}
	if tr.ID != nil {
		out["ritual_id"] = tr.ID.String()
	}
	if tr.Error != "" {
		out["error"] = tr.Error
	}
	if tr.Data != nil {
		out["data"] = tr.Data
	}
	return out
}

func (a *API) loadOrCreate(ctx context.Context, userID uuid.UUID, id *uuid.UUID) (uuid.UUID, []ai.ChatMessage, error) {
	if id == nil {
		var conv uuid.UUID
		err := a.pool.QueryRow(ctx, `
			INSERT INTO ai_conversations (user_id) VALUES ($1) RETURNING id
		`, userID).Scan(&conv)
		return conv, nil, err
	}
	var owner uuid.UUID
	err := a.pool.QueryRow(ctx, `
		SELECT user_id FROM ai_conversations WHERE id = $1
	`, *id).Scan(&owner)
	if err != nil {
		return uuid.Nil, nil, err
	}
	if owner != userID {
		return uuid.Nil, nil, pgx.ErrNoRows
	}
	msgs, err := a.loadMessages(ctx, *id)
	return *id, msgs, err
}

func (a *API) loadMessages(ctx context.Context, convID uuid.UUID) ([]ai.ChatMessage, error) {
	rows, err := a.pool.Query(ctx, `
		SELECT role, content, tool_name, tool_call_id, tool_calls
		FROM ai_messages
		WHERE conversation_id = $1
		ORDER BY created_at, id
	`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ai.ChatMessage
	for rows.Next() {
		var role string
		var content, toolName, toolCallID *string
		var rawCalls []byte
		if err := rows.Scan(&role, &content, &toolName, &toolCallID, &rawCalls); err != nil {
			return nil, err
		}
		m := ai.ChatMessage{Role: role}
		if content != nil {
			m.Content = *content
		}
		if toolName != nil {
			m.Name = *toolName
		}
		if toolCallID != nil {
			m.ToolCallID = *toolCallID
		}
		if len(rawCalls) > 0 {
			if err := json.Unmarshal(rawCalls, &m.ToolCalls); err != nil {
				return nil, err
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type dbMsg struct {
	Role       string
	Content    string
	ToolName   string
	ToolCallID string
	ToolCalls  []ai.ToolCall
	TokensIn   int
	TokensOut  int
}

func (a *API) insertMessage(ctx context.Context, convID uuid.UUID, m dbMsg) error {
	var tool, callID, calls any
	if m.ToolName != "" {
		tool = m.ToolName
	}
	if m.ToolCallID != "" {
		callID = m.ToolCallID
	}
	if len(m.ToolCalls) > 0 {
		b, err := json.Marshal(m.ToolCalls)
		if err != nil {
			return err
		}
		calls = b
	}
	var tin, tout any
	if m.TokensIn > 0 {
		tin = m.TokensIn
	}
	if m.TokensOut > 0 {
		tout = m.TokensOut
	}
	_, err := a.pool.Exec(ctx, `
		INSERT INTO ai_messages (conversation_id, role, content, tool_name, tool_call_id, tool_calls, tokens_in, tokens_out)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, convID, m.Role, m.Content, tool, callID, calls, tin, tout)
	return err
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	rows, err := a.pool.Query(r.Context(), `
		SELECT id, created_at FROM ai_conversations
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, u.ID)
	if err != nil {
		slog.Error("chat list", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer rows.Close()
	type item struct {
		ID        uuid.UUID `json:"id"`
		CreatedAt time.Time `json:"created_at"`
	}
	items := make([]item, 0)
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.CreatedAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		items = append(items, it)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	u := auth.UserFrom(r.Context())
	var created time.Time
	err = a.pool.QueryRow(r.Context(), `
		SELECT created_at FROM ai_conversations WHERE id = $1 AND user_id = $2
	`, id, u.ID).Scan(&created)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	rows, err := a.pool.Query(r.Context(), `
		SELECT id, role, content, tool_name, tool_call_id, tool_calls, created_at
		FROM ai_messages WHERE conversation_id = $1
		ORDER BY created_at, id
	`, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer rows.Close()
	type msg struct {
		ID         uuid.UUID     `json:"id"`
		Role       string        `json:"role"`
		Content    *string       `json:"content"`
		ToolName   *string       `json:"tool_name"`
		ToolCallID *string       `json:"tool_call_id"`
		ToolCalls  []ai.ToolCall `json:"tool_calls,omitempty"`
		CreatedAt  time.Time     `json:"created_at"`
	}
	msgs := make([]msg, 0)
	for rows.Next() {
		var m msg
		var rawCalls []byte
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.ToolName, &m.ToolCallID, &rawCalls, &m.CreatedAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		if len(rawCalls) > 0 {
			_ = json.Unmarshal(rawCalls, &m.ToolCalls)
		}
		msgs = append(msgs, m)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id":         id,
		"created_at": created,
		"messages":   msgs,
	})
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	u := auth.UserFrom(r.Context())
	tag, err := a.pool.Exec(r.Context(), `
		DELETE FROM ai_conversations WHERE id = $1 AND user_id = $2
	`, id, u.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter {
	return &limiter{hits: map[string][]time.Time{}}
}

func (l *limiter) pruneLocked(userID string, now time.Time) []time.Time {
	cutoff := now.Add(-time.Hour)
	cur := l.hits[userID][:0]
	for _, t := range l.hits[userID] {
		if t.After(cutoff) {
			cur = append(cur, t)
		}
	}
	if len(cur) == 0 {
		delete(l.hits, userID)
		return nil
	}
	l.hits[userID] = cur
	return cur
}

func (l *limiter) atLimit(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pruneLocked(userID, time.Now())) >= chatPerHour
}

func (l *limiter) allow(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cur := l.pruneLocked(userID, now)
	if len(cur) >= chatPerHour {
		return false
	}
	l.hits[userID] = append(cur, now)
	return true
}
