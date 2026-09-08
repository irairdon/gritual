package rituals

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

func (a *API) ToolCreatePersonal(ctx context.Context, userID uuid.UUID, raw json.RawMessage) (uuid.UUID, error) {
	var req struct {
		Type        string   `json:"type"`
		Title       string   `json:"title"`
		TargetValue *float64 `json:"target_value"`
		Direction   string   `json:"direction"`
		Period      string   `json:"period"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return uuid.Nil, errInvalid("invalid args")
	}
	typ := strings.TrimSpace(req.Type)
	if typ == "strength" {
		return uuid.Nil, errInvalid("type must be workout, not strength")
	}
	if !validType(typ) {
		return uuid.Nil, errInvalid("invalid type")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return uuid.Nil, errInvalid("title required")
	}
	direction := strings.TrimSpace(req.Direction)
	if direction == "" {
		direction = "at_least"
	}
	if !validDirection(direction) {
		return uuid.Nil, errInvalid("invalid direction")
	}
	period := strings.TrimSpace(req.Period)
	if period == "" {
		period = "none"
	}
	if !validPeriod(period) {
		return uuid.Nil, errInvalid("invalid period")
	}
	scoring := defaultScoringKey(typ)
	var id uuid.UUID
	err := a.pool.QueryRow(ctx, `
		INSERT INTO rituals (owner_user_id, type, title, target_value, direction, period, scoring_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, userID, typ, title, req.TargetValue, direction, period, scoring).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

type toolError struct {
	msg string
}

func (e *toolError) Error() string { return e.msg }

func errInvalid(msg string) error { return &toolError{msg: msg} }
