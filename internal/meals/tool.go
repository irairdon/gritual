package meals

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/irairdon/gritual/internal/ai"
)

func (a *API) ToolLogMeal(ctx context.Context, userID uuid.UUID, source string, raw json.RawMessage) (uuid.UUID, error) {
	var req struct {
		Items    []itemIn   `json:"items"`
		Notes    *string    `json:"notes"`
		LoggedAt *time.Time `json:"logged_at"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return uuid.Nil, errInvalid("invalid args")
	}
	items, kcal, protein, carbs, fat, err := normalizeItems(req.Items)
	if err != nil {
		return uuid.Nil, errInvalid(err.Error())
	}
	var notes *string
	if req.Notes != nil {
		n := ai.SanitizeNotes(*req.Notes)
		notes = &n
	}
	out, err := a.insertConfirmedSource(ctx, userID, source, items, kcal, protein, carbs, fat, notes, req.LoggedAt)
	if err != nil {
		return uuid.Nil, err
	}
	return out.ID, nil
}

func (a *API) insertConfirmedSource(ctx context.Context, userID uuid.UUID, source string, items []itemIn, kcal, protein, carbs, fat float64, notes *string, loggedAt *time.Time) (*mealOut, error) {
	if source == "" {
		source = "app"
	}
	itemSource := "user"
	if source == "ai_chat" || source == "mcp" {
		itemSource = "ai_text"
	}
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	logged := time.Now().UTC()
	if loggedAt != nil {
		logged = loggedAt.UTC()
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO logs (user_id, type, logged_at, visibility, notes, source)
		VALUES ($1, 'meal', $2, 'private', $3, $4)
		RETURNING id
	`, userID, logged, notes, source).Scan(&id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO meals (log_id, status, kcal, protein_g, carbs_g, fat_g)
		VALUES ($1, 'confirmed', $2, $3, $4, $5)
	`, id, kcal, protein, carbs, fat); err != nil {
		return nil, err
	}
	if err := insertItems(ctx, tx, id, items, itemSource); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a.loadMeal(ctx, id, userID)
}

func (a *API) ToolTodayMacros(ctx context.Context, userID uuid.UUID, tzName string) (map[string]any, error) {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	var kcal, protein, carbs, fat float64
	var n int
	err = a.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(m.kcal), 0), COALESCE(SUM(m.protein_g), 0),
		       COALESCE(SUM(m.carbs_g), 0), COALESCE(SUM(m.fat_g), 0), COUNT(*)::int
		FROM logs l
		JOIN meals m ON m.log_id = l.id
		WHERE l.user_id = $1 AND l.deleted_at IS NULL AND l.type = 'meal'
		  AND m.status = 'confirmed'
		  AND l.logged_at >= $2 AND l.logged_at < $3
	`, userID, start, end).Scan(&kcal, &protein, &carbs, &fat, &n)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"date":      start.Format("2006-01-02"),
		"kcal":      kcal,
		"protein_g": protein,
		"carbs_g":   carbs,
		"fat_g":     fat,
		"meals":     n,
	}, nil
}
