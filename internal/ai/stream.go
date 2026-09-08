package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

type chatCompletionsReq struct {
	Model     string         `json:"model"`
	User      string         `json:"user,omitempty"`
	MaxTokens int            `json:"max_tokens"`
	Stream    bool           `json:"stream"`
	Messages  []chatWireMsg  `json:"messages"`
	Tools     []chatWireTool `json:"tools,omitempty"`
}

type chatWireMsg struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatWireCall `json:"tool_calls,omitempty"`
}

type chatWireCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatWireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type chatStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   *string         `json:"content"`
			ToolCalls []chunkToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type chunkToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (c *Client) ChatStream(ctx context.Context, req ChatRequest, emit func(StreamDelta) error) (ChatResult, error) {
	if c == nil || c.APIKey == "" {
		return ChatResult{}, ErrUnavailable
	}
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = ChatMaxTokens
	}

	body := chatCompletionsReq{
		Model:     model,
		User:      req.User,
		MaxTokens: maxTok,
		Stream:    true,
		Messages:  toWireMessages(req.Messages),
		Tools:     toWireTools(req.Tools),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ChatResult{}, err
	}

	base := strings.TrimRight(c.BaseURL, "/")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+CompletionsPath, bytes.NewReader(payload))
	if err != nil {
		return ChatResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	cli := c.chatHTTP()
	res, err := cli.Do(httpReq)
	if err != nil {
		return ChatResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		slog.Error("xai chat", "status", res.StatusCode)
		return ChatResult{}, ErrUnavailable
	}

	acc := newToolAcc()
	var text strings.Builder
	var tokensIn, tokensOut int
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk chatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			tokensIn = chunk.Usage.PromptTokens
			tokensOut = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != nil && *delta.Content != "" {
			text.WriteString(*delta.Content)
			if emit != nil {
				if err := emit(StreamDelta{Text: *delta.Content}); err != nil {
					return ChatResult{}, err
				}
			}
		}
		acc.add(delta.ToolCalls)
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return ChatResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	slog.Info("xai chat", "tokens_in", tokensIn, "tokens_out", tokensOut)
	return ChatResult{
		Text:      text.String(),
		ToolCalls: acc.calls(),
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
	}, nil
}

func (c *Client) chatHTTP() *http.Client {
	if c.ChatHTTP != nil {
		return c.ChatHTTP
	}
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func toWireMessages(in []ChatMessage) []chatWireMsg {
	out := make([]chatWireMsg, 0, len(in))
	for _, m := range in {
		wm := chatWireMsg{
			Role:       m.Role,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		if m.Content != "" {
			wm.Content = m.Content
		}
		if len(m.ToolCalls) > 0 {
			wm.ToolCalls = make([]chatWireCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				c := chatWireCall{ID: tc.ID, Type: "function"}
				c.Function.Name = tc.Name
				c.Function.Arguments = tc.Args
				wm.ToolCalls = append(wm.ToolCalls, c)
			}
		}
		out = append(out, wm)
	}
	return out
}

func toWireTools(in []ToolSpec) []chatWireTool {
	if len(in) == 0 {
		return nil
	}
	out := make([]chatWireTool, 0, len(in))
	for _, t := range in {
		w := chatWireTool{Type: "function"}
		w.Function.Name = t.Name
		w.Function.Description = t.Description
		w.Function.Parameters = t.Parameters
		out = append(out, w)
	}
	return out
}

type toolAcc struct {
	byIdx map[int]*ToolCall
	order []int
}

func newToolAcc() *toolAcc {
	return &toolAcc{byIdx: map[int]*ToolCall{}}
}

func (a *toolAcc) add(calls []chunkToolCall) {
	for _, c := range calls {
		cur, ok := a.byIdx[c.Index]
		if !ok {
			cur = &ToolCall{}
			a.byIdx[c.Index] = cur
			a.order = append(a.order, c.Index)
		}
		if c.ID != "" {
			cur.ID = c.ID
		}
		if c.Function.Name != "" {
			cur.Name = c.Function.Name
		}
		if c.Function.Arguments != "" {
			cur.Args += c.Function.Arguments
		}
	}
}

func (a *toolAcc) calls() []ToolCall {
	if len(a.order) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(a.order))
	for _, i := range a.order {
		if c := a.byIdx[i]; c != nil && c.Name != "" {
			out = append(out, *c)
		}
	}
	return out
}
