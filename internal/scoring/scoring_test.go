package scoring

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	alice = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	bob   = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	carol = uuid.MustParse("00000000-0000-0000-0000-00000000000c")
	chID  = uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
	circ  = uuid.MustParse("00000000-0000-0000-0000-0000000000ce")
)

func denver() *time.Location {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		panic(err)
	}
	return loc
}

func ts(loc *time.Location, s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		panic(err)
	}
	return t
}

func tagged(user uuid.UUID, typ string, at time.Time) Log {
	cid := chID
	return Log{
		UserID:      user,
		Type:        typ,
		LoggedAt:    at,
		Visibility:  "challenge",
		ChallengeID: &cid,
	}
}

func iPtr(v int) *int         { return &v }
func fPtr(v float64) *float64 { return &v }

func TestStandingsExamples(t *testing.T) {
	loc := denver()
	weekStart := ts(loc, "2026-05-26 00:00")
	weekEnd := ts(loc, "2026-06-02 00:00")
	people := []uuid.UUID{carol, bob, alice} // input order must not affect rank

	tests := []struct {
		name   string
		ch     Challenge
		logs   []Log
		wantID []uuid.UUID
		wantPt []float64
	}{
		{
			name: "habit.completion",
			ch: Challenge{
				ID: chID, CircleID: circ, Type: "habit", ScoringKey: HabitCompletion,
				StartsAt: weekStart, EndsAt: weekEnd, TZ: loc,
			},
			logs: func() []Log {
				// Last-done Monday 09:00 Alice / 10:00 Bob; Carol skip-heavy (2 done).
				var logs []Log
				for _, day := range []string{
					"2026-05-26 08:00", "2026-05-27 08:00", "2026-05-28 08:00",
					"2026-05-29 08:00", "2026-05-30 08:00", "2026-06-01 09:00",
				} {
					l := tagged(alice, "habit", ts(loc, day))
					l.HabitStatus = "done"
					logs = append(logs, l)
				}
				for _, day := range []string{
					"2026-05-26 08:00", "2026-05-27 08:00", "2026-05-28 08:00",
					"2026-05-29 08:00", "2026-05-30 08:00", "2026-06-01 10:00",
				} {
					l := tagged(bob, "habit", ts(loc, day))
					l.HabitStatus = "done"
					logs = append(logs, l)
				}
				for _, day := range []string{"2026-05-26 08:00", "2026-05-27 08:00"} {
					l := tagged(carol, "habit", ts(loc, day))
					l.HabitStatus = "done"
					logs = append(logs, l)
				}
				for _, day := range []string{"2026-05-28 08:00", "2026-05-29 08:00", "2026-05-30 08:00"} {
					l := tagged(carol, "habit", ts(loc, day))
					l.HabitStatus = "skip"
					logs = append(logs, l)
				}
				return logs
			}(),
			wantID: []uuid.UUID{alice, bob, carol},
			wantPt: []float64{6, 6, 2},
		},
		{
			name: "fishing.days",
			ch: Challenge{
				ID: chID, CircleID: circ, Type: "fishing", ScoringKey: FishingDays,
				StartsAt: ts(loc, "2026-06-06 00:00"), EndsAt: ts(loc, "2026-06-13 00:00"), TZ: loc,
			},
			logs: []Log{
				tagged(alice, "fishing", ts(loc, "2026-06-06 18:00")), // Sat
				tagged(alice, "fishing", ts(loc, "2026-06-07 18:00")), // Sun
				tagged(alice, "fishing", ts(loc, "2026-06-08 18:00")), // Mon
				tagged(alice, "fishing", ts(loc, "2026-06-10 18:00")), // Wed
				tagged(bob, "fishing", ts(loc, "2026-06-06 09:00")),   // Sat
				tagged(bob, "fishing", ts(loc, "2026-06-06 16:00")),   // Sat again
			},
			wantID: []uuid.UUID{alice, bob, carol},
			wantPt: []float64{4, 1, 0},
		},
		{
			name: "fishing.catches",
			ch: Challenge{
				ID: chID, CircleID: circ, Type: "fishing", ScoringKey: FishingCatches,
				StartsAt: ts(loc, "2026-06-06 00:00"), EndsAt: ts(loc, "2026-06-13 00:00"), TZ: loc,
			},
			logs: []Log{
				func() Log { l := tagged(alice, "fishing", ts(loc, "2026-06-06 18:00")); l.CatchCount = 7; return l }(),
				func() Log { l := tagged(alice, "fishing", ts(loc, "2026-06-07 18:00")); l.CatchCount = 5; return l }(),
				func() Log { l := tagged(bob, "fishing", ts(loc, "2026-06-06 18:00")); l.CatchCount = 3; return l }(),
			},
			wantID: []uuid.UUID{alice, bob, carol},
			wantPt: []float64{12, 3, 0},
		},
		{
			name: "weight.progress at_most loss",
			ch: Challenge{
				ID: chID, CircleID: circ, Type: "weight", ScoringKey: WeightProgress,
				Direction: "at_most",
				StartsAt:  ts(loc, "2026-06-01 00:00"), EndsAt: ts(loc, "2026-09-01 00:00"), TZ: loc,
			},
			logs: []Log{
				weightLog(alice, ts(loc, "2026-05-31 08:00"), 80, true),
				weightLog(alice, ts(loc, "2026-06-15 08:00"), 76, true),
				weightLog(bob, ts(loc, "2026-05-31 08:00"), 90, true),
				weightLog(bob, ts(loc, "2026-06-15 08:00"), 89, true),
				weightLog(carol, ts(loc, "2026-05-31 08:00"), 70, true),
				weightLog(carol, ts(loc, "2026-06-15 08:00"), 71, true),
			},
			wantID: []uuid.UUID{alice, bob, carol},
			wantPt: []float64{4, 1, 0},
		},
		{
			name: "workout.volume",
			ch: Challenge{
				ID: chID, CircleID: circ, Type: "workout", ScoringKey: WorkoutVolume,
				StartsAt: weekStart, EndsAt: weekEnd, TZ: loc,
			},
			logs: []Log{
				func() Log {
					l := tagged(alice, "workout", ts(loc, "2026-05-27 18:00"))
					sets := make([]Set, 5)
					for i := range sets {
						sets[i] = Set{Reps: iPtr(5), WeightKG: fPtr(100)}
					}
					l.Sets = sets
					return l
				}(),
				func() Log {
					l := tagged(bob, "workout", ts(loc, "2026-05-27 18:00"))
					sets := make([]Set, 3)
					for i := range sets {
						sets[i] = Set{Reps: iPtr(8), WeightKG: fPtr(60)}
					}
					l.Sets = sets
					return l
				}(),
				func() Log {
					l := tagged(carol, "workout", ts(loc, "2026-05-27 18:00"))
					l.Sets = []Set{{Reps: iPtr(5), WeightKG: nil}}
					return l
				}(),
			},
			wantID: []uuid.UUID{alice, bob, carol},
			wantPt: []float64{2500, 1440, 0},
		},
		{
			name: "custom.sum",
			ch: Challenge{
				ID: chID, CircleID: circ, Type: "custom", ScoringKey: CustomSum,
				StartsAt: weekStart, EndsAt: weekEnd, TZ: loc,
			},
			logs: []Log{
				customLog(alice, ts(loc, "2026-05-26 08:00"), 10),
				customLog(alice, ts(loc, "2026-05-27 08:00"), 10),
				customLog(bob, ts(loc, "2026-05-26 08:00"), 7),
			},
			wantID: []uuid.UUID{alice, bob, carol},
			wantPt: []float64{20, 7, 0},
		},
		{
			name: "custom.average",
			ch: Challenge{
				ID: chID, CircleID: circ, Type: "custom", ScoringKey: CustomAverage,
				StartsAt: weekStart, EndsAt: weekEnd, TZ: loc,
			},
			logs: []Log{
				customLog(alice, ts(loc, "2026-05-26 08:00"), 10),
				customLog(alice, ts(loc, "2026-05-27 08:00"), 10),
				customLog(bob, ts(loc, "2026-05-26 08:00"), 7),
			},
			wantID: []uuid.UUID{alice, bob, carol},
			wantPt: []float64{10, 7, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Standings(tt.ch, people, tt.logs)
			if len(got) != 3 {
				t.Fatalf("entries = %d", len(got))
			}
			for i := range tt.wantID {
				if got[i].UserID != tt.wantID[i] {
					t.Errorf("rank %d user = %s want %s", i, got[i].UserID, tt.wantID[i])
				}
				if got[i].Points != tt.wantPt[i] {
					t.Errorf("rank %d points = %v want %v", i, got[i].Points, tt.wantPt[i])
				}
			}
			if tt.name == "weight.progress at_most loss" {
				if got[0].Detail["baseline_kg"] != 80.0 || got[0].Detail["current_kg"] != 76.0 {
					t.Errorf("alice detail = %v", got[0].Detail)
				}
			}
		})
	}
}

func TestWeightProgressGainAndBaselineFallback(t *testing.T) {
	loc := denver()
	ch := Challenge{
		ID: chID, CircleID: circ, Type: "weight", ScoringKey: WeightProgress,
		Direction: "at_least",
		StartsAt:  ts(loc, "2026-06-01 00:00"), EndsAt: ts(loc, "2026-09-01 00:00"), TZ: loc,
	}
	logs := []Log{
		weightLog(alice, ts(loc, "2026-05-31 08:00"), 80, true),
		weightLog(alice, ts(loc, "2026-06-15 08:00"), 76, true), // loss → 0 on gain
		weightLog(carol, ts(loc, "2026-05-31 08:00"), 70, true),
		weightLog(carol, ts(loc, "2026-06-15 08:00"), 71, true),
	}
	got := Standings(ch, []uuid.UUID{alice, bob, carol}, logs)
	if got[0].UserID != carol || got[0].Points != 1 {
		t.Fatalf("gain rank0 = %s pts %v", got[0].UserID, got[0].Points)
	}
	if got[1].Points != 0 || got[2].Points != 0 {
		t.Fatalf("others pts = %v %v", got[1].Points, got[2].Points)
	}

	// No pre-window log: first in-window is baseline, second is current.
	ch2 := ch
	ch2.Direction = "at_most"
	fallback := []Log{
		weightLog(alice, ts(loc, "2026-06-02 08:00"), 80, true),
		weightLog(alice, ts(loc, "2026-06-15 08:00"), 76, true),
	}
	got = Standings(ch2, []uuid.UUID{alice}, fallback)
	if got[0].Points != 4 {
		t.Fatalf("fallback points = %v", got[0].Points)
	}

	// Single in-window log → progress 0.
	got = Standings(ch2, []uuid.UUID{alice}, []Log{
		weightLog(alice, ts(loc, "2026-06-02 08:00"), 80, true),
	})
	if got[0].Points != 0 {
		t.Fatalf("single log points = %v", got[0].Points)
	}
}

func TestPrivateLogsNeverStand(t *testing.T) {
	loc := denver()
	ch := Challenge{
		ID: chID, CircleID: circ, Type: "habit", ScoringKey: HabitCompletion,
		StartsAt: ts(loc, "2026-05-26 00:00"), EndsAt: ts(loc, "2026-06-02 00:00"), TZ: loc,
	}
	priv := tagged(alice, "habit", ts(loc, "2026-05-27 08:00"))
	priv.ChallengeID = nil
	priv.Visibility = "private"
	priv.HabitStatus = "done"
	taggedDone := tagged(alice, "habit", ts(loc, "2026-05-28 08:00"))
	taggedDone.HabitStatus = "done"
	got := Standings(ch, []uuid.UUID{alice}, []Log{priv, taggedDone})
	if got[0].Points != 1 {
		t.Fatalf("points = %v (private should not score)", got[0].Points)
	}
}

func TestTieBreakUserID(t *testing.T) {
	loc := denver()
	at := ts(loc, "2026-05-27 08:00")
	ch := Challenge{
		ID: chID, CircleID: circ, Type: "habit", ScoringKey: HabitCompletion,
		StartsAt: ts(loc, "2026-05-26 00:00"), EndsAt: ts(loc, "2026-06-02 00:00"), TZ: loc,
	}
	a := tagged(alice, "habit", at)
	a.HabitStatus = "done"
	b := tagged(bob, "habit", at)
	b.HabitStatus = "done"
	got := Standings(ch, []uuid.UUID{bob, alice}, []Log{b, a})
	if got[0].UserID != alice || got[1].UserID != bob {
		t.Fatalf("order = %s %s", got[0].UserID, got[1].UserID)
	}
}

func weightLog(user uuid.UUID, at time.Time, kg float64, inChallenge bool) Log {
	l := Log{
		UserID:     user,
		Type:       "weight",
		LoggedAt:   at,
		HasWeight:  true,
		WeightKG:   kg,
		Visibility: "private",
	}
	if inChallenge && !at.Before(ts(denver(), "2026-06-01 00:00")) {
		cid := chID
		l.ChallengeID = &cid
		l.Visibility = "challenge"
	}
	return l
}

func customLog(user uuid.UUID, at time.Time, v float64) Log {
	l := tagged(user, "custom", at)
	l.HasCustom = true
	l.CustomValue = v
	return l
}
