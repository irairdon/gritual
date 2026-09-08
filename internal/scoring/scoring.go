package scoring

import (
	"bytes"
	"sort"
	"time"

	_ "time/tzdata"

	"github.com/google/uuid"
)

const (
	HabitCompletion = "habit.completion"
	FishingDays     = "fishing.days"
	FishingCatches  = "fishing.catches"
	WeightProgress  = "weight.progress"
	WorkoutVolume   = "workout.volume"
	CustomSum       = "custom.sum"
	CustomAverage   = "custom.average"
)

type Set struct {
	Reps     *int
	WeightKG *float64
}

type Log struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Type        string
	LoggedAt    time.Time
	Visibility  string
	MediaID     *uuid.UUID
	ChallengeID *uuid.UUID
	RitualID    *uuid.UUID
	CircleIDs   []uuid.UUID
	Deleted     bool
	MealStatus  string // empty when not a meal; drafts are "draft"

	HabitStatus string
	CatchCount  int
	WeightKG    float64
	HasWeight   bool
	Sets        []Set
	CustomValue float64
	HasCustom   bool
}

type Challenge struct {
	ID           uuid.UUID
	CircleID     uuid.UUID
	RitualID     *uuid.UUID
	Type         string
	ScoringKey   string
	Direction    string // at_most | at_least; required for weight.progress
	StartsAt     time.Time
	EndsAt       time.Time
	RequirePhoto bool
	TZ           *time.Location
}

type Entry struct {
	UserID      uuid.UUID
	Points      float64
	LastEventAt *time.Time
	Detail      map[string]any
}

// Eligible is the SPEC leaderboard filter (exact).
func Eligible(ch Challenge, l Log, participants map[uuid.UUID]struct{}) bool {
	if l.Deleted {
		return false
	}
	if l.Type == "meal" && l.MealStatus != "confirmed" {
		return false
	}
	if l.LoggedAt.Before(ch.StartsAt) || !l.LoggedAt.Before(ch.EndsAt) {
		return false
	}
	if _, ok := participants[l.UserID]; !ok {
		return false
	}
	if ch.RitualID == nil {
		if l.Type != ch.Type {
			return false
		}
	} else if l.RitualID == nil || *l.RitualID != *ch.RitualID {
		return false
	}
	if ch.RequirePhoto && l.MediaID == nil {
		return false
	}
	if l.ChallengeID != nil && *l.ChallengeID == ch.ID {
		return true
	}
	if l.Visibility == "challenge" || l.Visibility == "circle" {
		for _, cid := range l.CircleIDs {
			if cid == ch.CircleID {
				return true
			}
		}
	}
	return false
}

func Standings(ch Challenge, participants []uuid.UUID, logs []Log) []Entry {
	partSet := make(map[uuid.UUID]struct{}, len(participants))
	for _, id := range participants {
		partSet[id] = struct{}{}
	}

	window := make([]Log, 0, len(logs))
	byUserAll := make(map[uuid.UUID][]Log, len(participants))
	for _, l := range logs {
		byUserAll[l.UserID] = append(byUserAll[l.UserID], l)
		if Eligible(ch, l, partSet) {
			window = append(window, l)
		}
	}
	byUserWin := make(map[uuid.UUID][]Log, len(participants))
	for _, l := range window {
		byUserWin[l.UserID] = append(byUserWin[l.UserID], l)
	}

	out := make([]Entry, 0, len(participants))
	for _, uid := range participants {
		out = append(out, scoreUser(ch, uid, byUserWin[uid], byUserAll[uid]))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Points != out[j].Points {
			return out[i].Points > out[j].Points
		}
		if out[i].LastEventAt == nil && out[j].LastEventAt == nil {
			return bytes.Compare(out[i].UserID[:], out[j].UserID[:]) < 0
		}
		// Missing events sort after any real last_event_at so "earlier" still wins.
		if out[i].LastEventAt == nil {
			return false
		}
		if out[j].LastEventAt == nil {
			return true
		}
		if !out[i].LastEventAt.Equal(*out[j].LastEventAt) {
			return out[i].LastEventAt.Before(*out[j].LastEventAt)
		}
		return bytes.Compare(out[i].UserID[:], out[j].UserID[:]) < 0
	})
	return out
}

func scoreUser(ch Challenge, uid uuid.UUID, window, all []Log) Entry {
	e := Entry{UserID: uid, Points: 0}
	switch ch.ScoringKey {
	case HabitCompletion:
		n := 0
		for _, l := range window {
			if l.HabitStatus == "done" {
				n++
				touchLast(&e, l.LoggedAt)
			}
		}
		e.Points = float64(n)
	case FishingDays:
		loc := ch.TZ
		if loc == nil {
			loc = time.UTC
		}
		days := map[string]struct{}{}
		for _, l := range window {
			days[l.LoggedAt.In(loc).Format("2006-01-02")] = struct{}{}
			touchLast(&e, l.LoggedAt)
		}
		e.Points = float64(len(days))
	case FishingCatches:
		sum := 0
		for _, l := range window {
			sum += l.CatchCount
			touchLast(&e, l.LoggedAt)
		}
		e.Points = float64(sum)
	case WeightProgress:
		e = scoreWeight(ch, uid, window, all)
	case WorkoutVolume:
		var vol float64
		for _, l := range window {
			for _, s := range l.Sets {
				if s.Reps != nil && s.WeightKG != nil {
					vol += float64(*s.Reps) * *s.WeightKG
				}
			}
			touchLast(&e, l.LoggedAt)
		}
		e.Points = vol
	case CustomSum:
		var sum float64
		for _, l := range window {
			if !l.HasCustom {
				continue
			}
			sum += l.CustomValue
			touchLast(&e, l.LoggedAt)
		}
		e.Points = sum
	case CustomAverage:
		var sum float64
		n := 0
		for _, l := range window {
			if !l.HasCustom {
				continue
			}
			sum += l.CustomValue
			n++
			touchLast(&e, l.LoggedAt)
		}
		if n == 0 {
			e.Points = 0
		} else {
			e.Points = sum / float64(n)
		}
	}
	return e
}

func scoreWeight(ch Challenge, uid uuid.UUID, window, all []Log) Entry {
	e := Entry{UserID: uid}
	current, hasCurrent := latestWeight(window)
	if !hasCurrent {
		return e
	}
	baseline, hasBaseline := latestWeightAtOrBefore(all, ch, uid, ch.StartsAt)
	if !hasBaseline {
		// First eligible window log is the baseline; progress is 0 until a later log.
		baseline, hasBaseline = earliestWeight(window)
	}
	if !hasBaseline {
		return e
	}
	var pts float64
	switch ch.Direction {
	case "at_most":
		pts = baseline.WeightKG - current.WeightKG
	case "at_least":
		pts = current.WeightKG - baseline.WeightKG
	default:
		return e
	}
	if pts < 0 {
		pts = 0
	}
	e.Points = pts
	t := current.LoggedAt
	e.LastEventAt = &t
	e.Detail = map[string]any{
		"baseline_kg": baseline.WeightKG,
		"current_kg":  current.WeightKG,
	}
	return e
}

func matchWeightMeta(ch Challenge, l Log) bool {
	if l.Deleted || !l.HasWeight || l.Type != "weight" {
		return false
	}
	if ch.RitualID != nil {
		return l.RitualID != nil && *l.RitualID == *ch.RitualID
	}
	return true
}

func latestWeight(logs []Log) (Log, bool) {
	var best Log
	ok := false
	for _, l := range logs {
		if !l.HasWeight {
			continue
		}
		if !ok || l.LoggedAt.After(best.LoggedAt) {
			best = l
			ok = true
		}
	}
	return best, ok
}

func earliestWeight(logs []Log) (Log, bool) {
	var best Log
	ok := false
	for _, l := range logs {
		if !l.HasWeight {
			continue
		}
		if !ok || l.LoggedAt.Before(best.LoggedAt) {
			best = l
			ok = true
		}
	}
	return best, ok
}

func latestWeightAtOrBefore(all []Log, ch Challenge, uid uuid.UUID, at time.Time) (Log, bool) {
	var best Log
	ok := false
	for _, l := range all {
		if l.UserID != uid || !matchWeightMeta(ch, l) {
			continue
		}
		if l.LoggedAt.After(at) {
			continue
		}
		if !ok || l.LoggedAt.After(best.LoggedAt) {
			best = l
			ok = true
		}
	}
	return best, ok
}

func touchLast(e *Entry, t time.Time) {
	if e.LastEventAt == nil || t.After(*e.LastEventAt) {
		tt := t
		e.LastEventAt = &tt
	}
}
