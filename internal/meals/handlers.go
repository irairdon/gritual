package meals

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/irairdon/gritual/internal/ai"
	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/media"
)

type itemIn struct {
	Name     string   `json:"name"`
	Grams    *float64 `json:"grams"`
	Kcal     float64  `json:"kcal"`
	ProteinG float64  `json:"protein_g"`
	CarbsG   float64  `json:"carbs_g"`
	FatG     float64  `json:"fat_g"`
}

type confirmReq struct {
	ID       *uuid.UUID `json:"id"`
	DraftID  *uuid.UUID `json:"draft_id"`
	Items    []itemIn   `json:"items"`
	Notes    *string    `json:"notes"`
	LoggedAt *time.Time `json:"logged_at"`
	RitualID *uuid.UUID `json:"ritual_id"`
}

func (r confirmReq) draftLogID() *uuid.UUID {
	if r.DraftID != nil {
		return r.DraftID
	}
	return r.ID
}

func (a *API) handlePhoto(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if !u.EmailVerified {
		httpx.WriteError(w, http.StatusForbidden, "email_unverified", "email not verified")
		return
	}
	if u.AIConsentAt == nil {
		httpx.WriteError(w, http.StatusForbidden, "ai_consent_required", "AI consent required")
		return
	}
	if !a.cfg.AIEnabled || a.cfg.XAIAPIKey == "" || a.vision == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "ai_unavailable", "SpaceXAI unavailable")
		return
	}
	if a.limit.atLimit(u.ID.String()) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many vision requests")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), handlerTimeout)
	defer cancel()

	r.Body = http.MaxBytesReader(w, r.Body, media.MaxUpload)
	if err := r.ParseMultipartForm(media.MaxUpload); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "file too large or invalid multipart")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "file required")
		return
	}
	defer f.Close()

	stored, err := a.media.StoreJPEG(ctx, u.ID, io.LimitReader(f, media.MaxUpload+1))
	if err != nil {
		if errors.Is(err, media.ErrInvalid) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "not an image")
			return
		}
		slog.Error("meal photo store", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	est, err := a.estimate(ctx, u.ID, stored.SHA256, stored.JPEG)
	if err != nil {
		if errors.Is(err, errRateLimited) {
			httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many vision requests")
			return
		}
		slog.Error("meal vision", "err", err)
		httpx.WriteError(w, http.StatusServiceUnavailable, "ai_unavailable", "SpaceXAI error")
		return
	}

	out, err := a.insertDraft(ctx, u.ID, stored.ID, est)
	if err != nil {
		slog.Error("meal draft", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) estimate(ctx context.Context, userID uuid.UUID, sha []byte, jpeg []byte) (ai.Estimate, error) {
	var raw json.RawMessage
	err := a.pool.QueryRow(ctx, `
		SELECT result FROM vision_cache
		WHERE sha256 = $1 AND created_at > now() - interval '30 days'
	`, sha).Scan(&raw)
	if err == nil {
		return ai.ParseEstimate(raw)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ai.Estimate{}, err
	}

	model := a.cfg.XAIVisionModel
	if model == "" {
		model = ai.DefaultModel
	}
	// Count in-flight and failed xAI calls, not only successes.
	if !a.limit.allow(userID.String()) {
		return ai.Estimate{}, errRateLimited
	}
	raw, err = a.vision.CompleteJSON(ctx, ai.MealVisionRequest(ai.HashUser(userID, a.cfg.SessionSecret), model, jpeg))
	if err != nil {
		return ai.Estimate{}, err
	}
	est, err := ai.ParseEstimate(raw)
	if err != nil {
		return ai.Estimate{}, err
	}
	sanitized, err := json.Marshal(est)
	if err != nil {
		return ai.Estimate{}, err
	}
	_, _ = a.pool.Exec(ctx, `
		INSERT INTO vision_cache (sha256, result, model)
		VALUES ($1, $2, $3)
		ON CONFLICT (sha256) DO NOTHING
	`, sha, sanitized, model)
	return est, nil
}

func (a *API) insertDraft(ctx context.Context, userID, photoID uuid.UUID, est ai.Estimate) (*mealOut, error) {
	kcal, protein, carbs, fat := sumFoods(est.Foods)
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var notes *string
	if est.Notes != "" {
		n := est.Notes
		notes = &n
	}
	var logID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO logs (user_id, type, logged_at, visibility, notes, media_id, source)
		VALUES ($1, 'meal', now(), 'private', $2, $3, 'ai_vision')
		RETURNING id
	`, userID, notes, photoID).Scan(&logID)
	if err != nil {
		return nil, err
	}
	conf := est.OverallConfidence
	if _, err := tx.Exec(ctx, `
		INSERT INTO meals (log_id, status, kcal, protein_g, carbs_g, fat_g, confidence, photo_media_id)
		VALUES ($1, 'draft', $2, $3, $4, $5, $6, $7)
	`, logID, kcal, protein, carbs, fat, conf, photoID); err != nil {
		return nil, err
	}
	for _, f := range est.Foods {
		grams := f.Grams
		if _, err := tx.Exec(ctx, `
			INSERT INTO meal_items (meal_id, name, grams, kcal, protein_g, carbs_g, fat_g, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'vision')
		`, logID, f.Name, grams, f.Kcal, f.ProteinG, f.CarbsG, f.FatG); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a.loadMeal(ctx, logID, userID)
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "meal not found")
		return
	}
	out, err := a.loadMeal(r.Context(), id, auth.UserFrom(r.Context()).ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "meal not found")
			return
		}
		slog.Error("meal get", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handleConfirm(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	var req confirmReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	items, kcal, protein, carbs, fat, err := normalizeItems(req.Items)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	var notes *string
	if req.Notes != nil {
		n := ai.SanitizeNotes(*req.Notes)
		notes = &n
	}

	if id := req.draftLogID(); id != nil {
		out, err := a.updateMeal(r.Context(), u.ID, *id, items, kcal, protein, carbs, fat, notes, req.LoggedAt, req.RitualID)
		if err != nil {
			writeMealErr(w, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}

	out, err := a.insertConfirmed(r.Context(), u.ID, items, kcal, protein, carbs, fat, notes, req.LoggedAt, req.RitualID)
	if err != nil {
		writeMealErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleDay(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	loc, err := time.LoadLocation(u.TZ)
	if err != nil {
		loc = time.UTC
	}
	raw := strings.TrimSpace(r.URL.Query().Get("date"))
	var day time.Time
	if raw == "" {
		now := time.Now().In(loc)
		day = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	} else {
		day, err = time.ParseInLocation("2006-01-02", raw, loc)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid date")
			return
		}
	}
	start := day
	end := day.AddDate(0, 0, 1)

	rows, err := a.pool.Query(r.Context(), `
		SELECT l.id
		FROM logs l
		JOIN meals m ON m.log_id = l.id
		WHERE l.user_id = $1 AND l.deleted_at IS NULL AND l.type = 'meal'
		  AND m.status = 'confirmed'
		  AND l.logged_at >= $2 AND l.logged_at < $3
		ORDER BY l.logged_at ASC, l.id ASC
	`, u.ID, start, end)
	if err != nil {
		slog.Error("meals day", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			slog.Error("meals day scan", "err", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	items := make([]mealOut, 0, len(ids))
	var kcal, protein, carbs, fat float64
	for _, id := range ids {
		m, err := a.loadMeal(r.Context(), id, u.ID)
		if err != nil {
			continue
		}
		items = append(items, *m)
		kcal += m.Kcal
		protein += m.ProteinG
		carbs += m.CarbsG
		fat += m.FatG
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"date":      day.Format("2006-01-02"),
		"kcal":      kcal,
		"protein_g": protein,
		"carbs_g":   carbs,
		"fat_g":     fat,
		"items":     items,
	})
}

func (a *API) updateMeal(ctx context.Context, userID, id uuid.UUID, items []itemIn, kcal, protein, carbs, fat float64, notes *string, loggedAt *time.Time, ritualID *uuid.UUID) (*mealOut, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT l.id FROM logs l
		JOIN meals m ON m.log_id = l.id
		WHERE l.id = $1 AND l.user_id = $2 AND l.deleted_at IS NULL
		FOR UPDATE
	`, id, userID).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	if ritualID != nil {
		if err := checkMealRitual(ctx, tx, userID, *ritualID); err != nil {
			return nil, err
		}
	}
	var logged any
	if loggedAt != nil {
		logged = loggedAt.UTC()
	}
	if _, err := tx.Exec(ctx, `
		UPDATE logs SET notes = COALESCE($2, notes), logged_at = COALESCE($3, logged_at), ritual_id = COALESCE($4, ritual_id), updated_at = now()
		WHERE id = $1
	`, id, notes, logged, ritualID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE meals SET status = 'confirmed', kcal = $2, protein_g = $3, carbs_g = $4, fat_g = $5
		WHERE log_id = $1
	`, id, kcal, protein, carbs, fat); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM meal_items WHERE meal_id = $1`, id); err != nil {
		return nil, err
	}
	if err := insertItems(ctx, tx, id, items, "user"); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a.loadMeal(ctx, id, userID)
}

func (a *API) insertConfirmed(ctx context.Context, userID uuid.UUID, items []itemIn, kcal, protein, carbs, fat float64, notes *string, loggedAt *time.Time, ritualID *uuid.UUID) (*mealOut, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if ritualID != nil {
		if err := checkMealRitual(ctx, tx, userID, *ritualID); err != nil {
			return nil, err
		}
	}
	logged := time.Now().UTC()
	if loggedAt != nil {
		logged = loggedAt.UTC()
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO logs (user_id, ritual_id, type, logged_at, visibility, notes, source)
		VALUES ($1, $2, 'meal', $3, 'private', $4, 'app')
		RETURNING id
	`, userID, ritualID, logged, notes).Scan(&id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO meals (log_id, status, kcal, protein_g, carbs_g, fat_g)
		VALUES ($1, 'confirmed', $2, $3, $4, $5)
	`, id, kcal, protein, carbs, fat); err != nil {
		return nil, err
	}
	if err := insertItems(ctx, tx, id, items, "user"); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a.loadMeal(ctx, id, userID)
}

func (a *API) loadMeal(ctx context.Context, id, userID uuid.UUID) (*mealOut, error) {
	var out mealOut
	err := a.pool.QueryRow(ctx, `
		SELECT l.id, m.status, l.logged_at, l.visibility, l.notes,
		       m.kcal, m.protein_g, m.carbs_g, m.fat_g, m.confidence, m.photo_media_id
		FROM logs l
		JOIN meals m ON m.log_id = l.id
		WHERE l.id = $1 AND l.user_id = $2 AND l.deleted_at IS NULL
	`, id, userID).Scan(
		&out.ID, &out.Status, &out.LoggedAt, &out.Visibility, &out.Notes,
		&out.Kcal, &out.ProteinG, &out.CarbsG, &out.FatG, &out.Confidence, &out.PhotoMediaID,
	)
	if err != nil {
		return nil, err
	}
	rows, err := a.pool.Query(ctx, `
		SELECT id, name, grams, kcal, protein_g, carbs_g, fat_g, source
		FROM meal_items WHERE meal_id = $1 ORDER BY id
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out.Items = []mealItemOut{}
	for rows.Next() {
		var it mealItemOut
		if err := rows.Scan(&it.ID, &it.Name, &it.Grams, &it.Kcal, &it.ProteinG, &it.CarbsG, &it.FatG, &it.Source); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, it)
	}
	return &out, rows.Err()
}

func insertItems(ctx context.Context, tx pgx.Tx, mealID uuid.UUID, items []itemIn, source string) error {
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO meal_items (meal_id, name, grams, kcal, protein_g, carbs_g, fat_g, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, mealID, it.Name, it.Grams, it.Kcal, it.ProteinG, it.CarbsG, it.FatG, source); err != nil {
			return err
		}
	}
	return nil
}

func normalizeItems(in []itemIn) ([]itemIn, float64, float64, float64, float64, error) {
	out := make([]itemIn, 0, len(in))
	var kcal, protein, carbs, fat float64
	for _, it := range in {
		name, err := ai.SanitizeFoodName(it.Name)
		if err != nil {
			return nil, 0, 0, 0, 0, err
		}
		grams := 0.0
		if it.Grams != nil {
			grams = *it.Grams
		}
		if !validUserMacros(grams, it.Kcal, it.ProteinG, it.CarbsG, it.FatG) {
			return nil, 0, 0, 0, 0, errors.New("invalid macros")
		}
		it.Name = name
		out = append(out, it)
		kcal += it.Kcal
		protein += it.ProteinG
		carbs += it.CarbsG
		fat += it.FatG
	}
	return out, kcal, protein, carbs, fat, nil
}

func validUserMacros(grams, kcal, protein, carbs, fat float64) bool {
	return grams >= 0 && grams <= 5000 && kcal >= 0 && kcal <= 10000 && protein >= 0 && carbs >= 0 && fat >= 0
}

func sumFoods(foods []ai.Food) (kcal, protein, carbs, fat float64) {
	for _, f := range foods {
		kcal += f.Kcal
		protein += f.ProteinG
		carbs += f.CarbsG
		fat += f.FatG
	}
	return
}

func checkMealRitual(ctx context.Context, tx pgx.Tx, userID, ritualID uuid.UUID) error {
	var typ string
	err := tx.QueryRow(ctx, `
		SELECT type FROM rituals
		WHERE id = $1 AND deleted_at IS NULL
		  AND (owner_user_id = $2 OR circle_id IN (
		        SELECT circle_id FROM circle_members WHERE user_id = $2
		      ))
	`, ritualID, userID).Scan(&typ)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return err
	}
	if typ != "meal" {
		return errInvalid("ritual type mismatch")
	}
	return nil
}

var (
	errNotFound    = errors.New("not found")
	errRateLimited = errors.New("rate limited")
)

type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func errInvalid(msg string) error {
	return &apiError{status: http.StatusBadRequest, code: "invalid", msg: msg}
}

func writeMealErr(w http.ResponseWriter, err error) {
	var api *apiError
	if errors.As(err, &api) {
		httpx.WriteError(w, api.status, api.code, api.msg)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "meal not found")
		return
	}
	if errors.Is(err, ai.ErrBadName) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid food name")
		return
	}
	slog.Error("meals", "err", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}
