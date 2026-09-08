package challenges

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

var errNotParticipant = errors.New("not a participant")

func (a *API) ToolStandings(ctx context.Context, userID uuid.UUID, raw json.RawMessage) (any, error) {
	var req struct {
		ChallengeID uuid.UUID `json:"challenge_id"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, errInvalid("invalid args")
	}
	if req.ChallengeID == uuid.Nil {
		return nil, errInvalid("challenge_id required")
	}
	ch, err := a.loadChallenge(ctx, req.ChallengeID, userID)
	if err != nil {
		return nil, err
	}
	if !ch.Joined {
		return nil, errNotParticipant
	}
	entries, err := a.computeStandings(ctx, ch)
	if err != nil {
		return nil, err
	}
	return map[string]any{"entries": entries}, nil
}

func errInvalid(msg string) error {
	return &apiError{msg: msg}
}

type apiError struct{ msg string }

func (e *apiError) Error() string { return e.msg }

type RecapEntry struct {
	DisplayName string
	Points      float64
}

func (a *API) StandingsSnapshot(ctx context.Context, challengeID uuid.UUID) ([]RecapEntry, error) {
	var c challengeOut
	err := a.pool.QueryRow(ctx, `
		SELECT c.id, c.circle_id, c.ritual_id, c.type, c.scoring_key, c.direction, c.name,
		       c.starts_at, c.ends_at, c.require_photo, c.join_policy, c.created_at,
		       false, (SELECT COUNT(*)::int FROM challenge_participants p WHERE p.challenge_id = c.id)
		FROM challenges c
		WHERE c.id = $1
	`, challengeID).Scan(
		&c.ID, &c.CircleID, &c.RitualID, &c.Type, &c.ScoringKey, &c.Direction, &c.Name,
		&c.StartsAt, &c.EndsAt, &c.RequirePhoto, &c.JoinPolicy, &c.CreatedAt, &c.Joined, &c.ParticipantCount,
	)
	if err != nil {
		return nil, err
	}
	entries, err := a.computeStandings(ctx, &c)
	if err != nil {
		return nil, err
	}
	out := make([]RecapEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, RecapEntry{DisplayName: e.DisplayName, Points: e.Points})
	}
	return out, nil
}
