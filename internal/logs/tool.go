package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func decodeStrict(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return invalid("invalid args")
	}
	return nil
}

func (a *API) ToolLogWeight(ctx context.Context, userID uuid.UUID, source string, raw json.RawMessage) (uuid.UUID, error) {
	var req struct {
		KG       *float64   `json:"kg"`
		LB       *float64   `json:"lb"`
		LoggedAt *time.Time `json:"logged_at"`
		Notes    *string    `json:"notes"`
	}
	if err := decodeStrict(raw, &req); err != nil {
		return uuid.Nil, err
	}
	kg, err := resolveKg(req.KG, req.LB)
	if err != nil {
		return uuid.Nil, invalid(err.Error())
	}
	out, err := a.insertLog(ctx, userID, "weight", logFields{
		LoggedAt: req.LoggedAt, Notes: req.Notes, Source: source,
	}, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
		_, err := tx.Exec(ctx, `INSERT INTO weight_logs (log_id, kg) VALUES ($1, $2)`, logID, kg)
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	return out.ID, nil
}

func (a *API) ToolLogActivity(ctx context.Context, userID uuid.UUID, source string, raw json.RawMessage) (uuid.UUID, error) {
	var req struct {
		Type      string     `json:"type"`
		Title     string     `json:"title"`
		Sets      []setIn    `json:"sets"`
		Status    string     `json:"status"`
		WaterBody *string    `json:"water_body"`
		Lat       *float64   `json:"lat"`
		Lng       *float64   `json:"lng"`
		Catches   []catchIn  `json:"catches"`
		Value     *float64   `json:"value"`
		Unit      string     `json:"unit"`
		LoggedAt  *time.Time `json:"logged_at"`
		Notes     *string    `json:"notes"`
	}
	if err := decodeStrict(raw, &req); err != nil {
		return uuid.Nil, err
	}
	typ := strings.TrimSpace(req.Type)
	fields := logFields{LoggedAt: req.LoggedAt, Notes: req.Notes, Source: source}
	var out *logOut
	var err error
	switch typ {
	case "workout":
		title := strings.TrimSpace(req.Title)
		if title == "" {
			return uuid.Nil, invalid("title required")
		}
		for i, s := range req.Sets {
			if strings.TrimSpace(s.Exercise) == "" {
				return uuid.Nil, invalid("set exercise required")
			}
			req.Sets[i].Exercise = strings.TrimSpace(s.Exercise)
		}
		out, err = a.insertLog(ctx, userID, "workout", fields, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
			if _, err := tx.Exec(ctx, `INSERT INTO workout_logs (log_id, title) VALUES ($1, $2)`, logID, title); err != nil {
				return err
			}
			return insertSets(ctx, tx, logID, req.Sets)
		})
	case "habit":
		status := strings.TrimSpace(req.Status)
		if status != "done" && status != "skip" {
			return uuid.Nil, invalid("status must be done or skip")
		}
		out, err = a.insertLog(ctx, userID, "habit", fields, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
			_, err := tx.Exec(ctx, `INSERT INTO habit_logs (log_id, status) VALUES ($1, $2)`, logID, status)
			return err
		})
	case "fishing":
		if err := validateLatLng(req.Lat, req.Lng); err != nil {
			return uuid.Nil, invalid(err.Error())
		}
		out, err = a.insertLog(ctx, userID, "fishing", fields, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
			if _, err := tx.Exec(ctx, `
				INSERT INTO fishing_logs (log_id, water_body, lat, lng)
				VALUES ($1, $2, $3, $4)
			`, logID, trimPtr(req.WaterBody), req.Lat, req.Lng); err != nil {
				return err
			}
			return insertCatches(ctx, tx, logID, req.Catches)
		})
	case "custom":
		if req.Value == nil {
			return uuid.Nil, invalid("value required")
		}
		out, err = a.insertLog(ctx, userID, "custom", fields, func(ctx context.Context, tx pgx.Tx, logID uuid.UUID) error {
			_, err := tx.Exec(ctx, `INSERT INTO custom_logs (log_id, value, unit) VALUES ($1, $2, $3)`, logID, *req.Value, strings.TrimSpace(req.Unit))
			return err
		})
	default:
		return uuid.Nil, invalid("type must be workout, habit, fishing, or custom")
	}
	if err != nil {
		return uuid.Nil, err
	}
	return out.ID, nil
}
