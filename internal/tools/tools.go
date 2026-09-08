package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/irairdon/gritual/internal/ai"
	"github.com/irairdon/gritual/internal/challenges"
	"github.com/irairdon/gritual/internal/logs"
	"github.com/irairdon/gritual/internal/meals"
	"github.com/irairdon/gritual/internal/rituals"
)

type Runner struct {
	logs       *logs.API
	meals      *meals.API
	rituals    *rituals.API
	challenges *challenges.API
	tzForUser  func(ctx context.Context, userID uuid.UUID) (string, error)
}

func New(logsAPI *logs.API, mealsAPI *meals.API, ritualsAPI *rituals.API, chalAPI *challenges.API, tzForUser func(ctx context.Context, userID uuid.UUID) (string, error)) *Runner {
	return &Runner{logs: logsAPI, meals: mealsAPI, rituals: ritualsAPI, challenges: chalAPI, tzForUser: tzForUser}
}

type Result struct {
	OK    bool           `json:"ok"`
	LogID *uuid.UUID     `json:"log_id,omitempty"`
	ID    *uuid.UUID     `json:"id,omitempty"`
	Error string         `json:"error,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

func (r *Runner) Call(ctx context.Context, userID uuid.UUID, source, name string, args json.RawMessage) Result {
	if !ai.KnownTool(name) {
		return Result{OK: false, Error: "unknown tool"}
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	switch name {
	case ai.ToolLogWeight:
		id, err := r.logs.ToolLogWeight(ctx, userID, source, args)
		return logResult(id, err)
	case ai.ToolLogActivity:
		id, err := r.logs.ToolLogActivity(ctx, userID, source, args)
		return logResult(id, err)
	case ai.ToolLogMeal:
		id, err := r.meals.ToolLogMeal(ctx, userID, source, args)
		return logResult(id, err)
	case ai.ToolCreateRitual:
		id, err := r.rituals.ToolCreatePersonal(ctx, userID, args)
		if err != nil {
			return Result{OK: false, Error: err.Error()}
		}
		return Result{OK: true, ID: &id}
	case ai.ToolGetTodayMacros:
		tz := "America/Denver"
		if r.tzForUser != nil {
			if got, err := r.tzForUser(ctx, userID); err == nil && got != "" {
				tz = got
			}
		}
		data, err := r.meals.ToolTodayMacros(ctx, userID, tz)
		if err != nil {
			return Result{OK: false, Error: err.Error()}
		}
		return Result{OK: true, Data: data}
	case ai.ToolGetChallengeStandings:
		data, err := r.challenges.ToolStandings(ctx, userID, args)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Result{OK: false, Error: "challenge not found"}
			}
			return Result{OK: false, Error: err.Error()}
		}
		b, _ := json.Marshal(data)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return Result{OK: true, Data: m}
	default:
		return Result{OK: false, Error: "unknown tool"}
	}
}

func logResult(id uuid.UUID, err error) Result {
	if err != nil {
		return Result{OK: false, Error: err.Error()}
	}
	return Result{OK: true, LogID: &id}
}
