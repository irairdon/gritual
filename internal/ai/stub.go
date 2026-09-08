package ai

import (
	"context"
	"encoding/json"
)

type Stub struct {
	JSON           json.RawMessage
	Err            error
	Calls          int
	Last           JSONRequest
	CompleteJSONFn func(ctx context.Context, req JSONRequest) (json.RawMessage, error)
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
