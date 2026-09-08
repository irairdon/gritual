package challenges

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/scoring"
)

func (a *API) handleStandings(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "challenge not found")
		return
	}
	u := auth.UserFrom(r.Context())
	out, err := a.loadChallenge(r.Context(), id, u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	entries, err := a.computeStandings(r.Context(), out)
	if err != nil {
		slog.Error("standings", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (a *API) computeStandings(ctx context.Context, ch *challengeOut) ([]standingEntry, error) {
	var tzName string
	if err := a.pool.QueryRow(ctx, `SELECT tz FROM circles WHERE id = $1`, ch.CircleID).Scan(&tzName); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}

	dir := ""
	if ch.Direction != nil {
		dir = *ch.Direction
	}
	sc := scoring.Challenge{
		ID:           ch.ID,
		CircleID:     ch.CircleID,
		RitualID:     ch.RitualID,
		Type:         ch.Type,
		ScoringKey:   ch.ScoringKey,
		Direction:    dir,
		StartsAt:     ch.StartsAt,
		EndsAt:       ch.EndsAt,
		RequirePhoto: ch.RequirePhoto,
		TZ:           loc,
	}

	prows, err := a.pool.Query(ctx, `
		SELECT p.user_id, u.display_name
		FROM challenge_participants p
		JOIN users u ON u.id = p.user_id
		WHERE p.challenge_id = $1
	`, ch.ID)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	var participants []uuid.UUID
	names := map[uuid.UUID]string{}
	for prows.Next() {
		var uid uuid.UUID
		var name string
		if err := prows.Scan(&uid, &name); err != nil {
			return nil, err
		}
		participants = append(participants, uid)
		names[uid] = name
	}
	if err := prows.Err(); err != nil {
		return nil, err
	}
	if participants == nil {
		participants = []uuid.UUID{}
	}

	logs, err := a.loadScoreLogs(ctx, sc, participants)
	if err != nil {
		return nil, err
	}
	ranked := scoring.Standings(sc, participants, logs)
	out := make([]standingEntry, 0, len(ranked))
	for _, e := range ranked {
		out = append(out, standingEntry{
			UserID:      e.UserID,
			DisplayName: names[e.UserID],
			Points:      e.Points,
			LastEventAt: e.LastEventAt,
			Detail:      e.Detail,
		})
	}
	return out, nil
}

func (a *API) loadScoreLogs(ctx context.Context, ch scoring.Challenge, participants []uuid.UUID) ([]scoring.Log, error) {
	if len(participants) == 0 {
		return nil, nil
	}
	rows, err := a.pool.Query(ctx, `
		SELECT l.id, l.user_id, l.ritual_id, l.challenge_id, l.type, l.logged_at,
		       l.visibility, l.media_id, l.deleted_at,
		       hl.status, wl.kg, cl.value, m.status
		FROM logs l
		LEFT JOIN habit_logs hl ON hl.log_id = l.id
		LEFT JOIN weight_logs wl ON wl.log_id = l.id
		LEFT JOIN custom_logs cl ON cl.log_id = l.id
		LEFT JOIN meals m ON m.log_id = l.id
		WHERE l.user_id = ANY($1)
		  AND (
		    ($2::uuid IS NULL AND l.type = $3)
		    OR (l.ritual_id = $2)
		  )
		  AND (
		    (l.logged_at >= $4 AND l.logged_at < $5)
		    OR ($6 = 'weight.progress' AND l.type = 'weight' AND l.logged_at <= $4)
		  )
	`, participants, ch.RitualID, ch.Type, ch.StartsAt, ch.EndsAt, ch.ScoringKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []scoring.Log
	idx := map[uuid.UUID]int{}
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var l scoring.Log
		var deleted *time.Time
		var habit *string
		var kg *float64
		var custom *float64
		var meal *string
		if err := rows.Scan(&l.ID, &l.UserID, &l.RitualID, &l.ChallengeID, &l.Type, &l.LoggedAt,
			&l.Visibility, &l.MediaID, &deleted, &habit, &kg, &custom, &meal); err != nil {
			return nil, err
		}
		l.Deleted = deleted != nil
		if habit != nil {
			l.HabitStatus = *habit
		}
		if kg != nil {
			l.HasWeight = true
			l.WeightKG = *kg
		}
		if custom != nil {
			l.HasCustom = true
			l.CustomValue = *custom
		}
		if meal != nil {
			l.MealStatus = *meal
		}
		idx[l.ID] = len(logs)
		logs = append(logs, l)
		ids = append(ids, l.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return logs, nil
	}

	if err := a.hydrateCircles(ctx, logs, idx, ids); err != nil {
		return nil, err
	}
	if err := a.hydrateSets(ctx, logs, idx, ids); err != nil {
		return nil, err
	}
	if err := a.hydrateCatches(ctx, logs, idx, ids); err != nil {
		return nil, err
	}
	return logs, nil
}

func (a *API) hydrateCircles(ctx context.Context, logs []scoring.Log, idx map[uuid.UUID]int, ids []uuid.UUID) error {
	rows, err := a.pool.Query(ctx, `SELECT log_id, circle_id FROM log_circles WHERE log_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var lid, cid uuid.UUID
		if err := rows.Scan(&lid, &cid); err != nil {
			return err
		}
		i, ok := idx[lid]
		if !ok {
			continue
		}
		logs[i].CircleIDs = append(logs[i].CircleIDs, cid)
	}
	return rows.Err()
}

func (a *API) hydrateSets(ctx context.Context, logs []scoring.Log, idx map[uuid.UUID]int, ids []uuid.UUID) error {
	rows, err := a.pool.Query(ctx, `
		SELECT workout_log_id, reps, weight_kg FROM workout_sets WHERE workout_log_id = ANY($1)
	`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var wid uuid.UUID
		var s scoring.Set
		if err := rows.Scan(&wid, &s.Reps, &s.WeightKG); err != nil {
			return err
		}
		i, ok := idx[wid]
		if !ok {
			continue
		}
		logs[i].Sets = append(logs[i].Sets, s)
	}
	return rows.Err()
}

func (a *API) hydrateCatches(ctx context.Context, logs []scoring.Log, idx map[uuid.UUID]int, ids []uuid.UUID) error {
	rows, err := a.pool.Query(ctx, `
		SELECT fishing_log_id, COALESCE(SUM(count), 0)::int
		FROM fishing_catches WHERE fishing_log_id = ANY($1)
		GROUP BY fishing_log_id
	`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var fid uuid.UUID
		var n int
		if err := rows.Scan(&fid, &n); err != nil {
			return err
		}
		i, ok := idx[fid]
		if !ok {
			continue
		}
		logs[i].CatchCount = n
	}
	return rows.Err()
}
