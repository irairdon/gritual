package ai

import (
	"context"
	"encoding/json"
)

type ChatRound struct {
	Text      string
	ToolCalls []ToolCall
	Err       error
}

type Stub struct {
	JSON           json.RawMessage
	Err            error
	Calls          int
	Last           JSONRequest
	CompleteJSONFn func(ctx context.Context, req JSONRequest) (json.RawMessage, error)
	ChatCalls      int
	LastChat       ChatRequest
	ChatRounds     []ChatRound
	ChatStreamFn   func(ctx context.Context, req ChatRequest, emit func(StreamDelta) error) (ChatResult, error)
}

func (s *Stub) CompleteJSON(ctx context.Context, req JSONRequest) (json.RawMessage, error) {
	s.Calls++
	s.Last = req
	if s.CompleteJSONFn != nil {
		return s.CompleteJSONFn(ctx, req)
	}
	if s.Err != nil {
		return nil, s.Err
	}
	if len(s.JSON) == 0 {
		return json.RawMessage(`{"foods":[],"overall_confidence":0,"notes":""}`), nil
	}
	return s.JSON, nil
}

func (s *Stub) ChatStream(ctx context.Context, req ChatRequest, emit func(StreamDelta) error) (ChatResult, error) {
	s.ChatCalls++
	s.LastChat = req
	if s.ChatStreamFn != nil {
		return s.ChatStreamFn(ctx, req, emit)
	}
	if s.Err != nil {
		return ChatResult{}, s.Err
	}
	idx := s.ChatCalls - 1
	if idx < len(s.ChatRounds) {
		r := s.ChatRounds[idx]
		if r.Err != nil {
			return ChatResult{}, r.Err
		}
		if r.Text != "" && emit != nil {
			if err := emit(StreamDelta{Text: r.Text}); err != nil {
				return ChatResult{}, err
			}
		}
		return ChatResult{Text: r.Text, ToolCalls: r.ToolCalls}, nil
	}
	text := "ok"
	if emit != nil {
		if err := emit(StreamDelta{Text: text}); err != nil {
			return ChatResult{}, err
		}
	}
	return ChatResult{Text: text}, nil
}
