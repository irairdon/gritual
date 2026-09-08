package challenges

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/circles"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
	"github.com/irairdon/gritual/internal/httpx"
	"github.com/irairdon/gritual/internal/logs"
	"github.com/irairdon/gritual/internal/rituals"
)

const origin = "http://localhost:8080"

type harness struct {
	t    *testing.T
	pool *pgxpool.Pool
	h    http.Handler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := db.TestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		AppBaseURL:    origin,
		ViteDevOrigin: "http://localhost:5173",
		AuthDevLogin:  true,
		SessionSecret: bytes.Repeat([]byte("s"), 32),
	}
	authAPI := auth.New(cfg, pool)
	circAPI := circles.New(cfg, pool)
	ritAPI := rituals.New(cfg, pool)
	logAPI := logs.New(cfg, pool)
	chalAPI := New(cfg, pool)
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}
	return &harness{
		t:    t,
		pool: pool,
		h: httpx.NewRouter(ui, pool, func(r chi.Router) {
			authAPI.Mount(r)
			circAPI.Mount(r)
			ritAPI.Mount(r)
			logAPI.Mount(r)
			chalAPI.Mount(r)
		}),
	}
}

func (h *harness) do(method, path string, cookie *http.Cookie, body any) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", origin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec.Result()
}

func (h *harness) login(email string) (*http.Cookie, string) {
	h.t.Helper()
	res := h.do(http.MethodPost, "/api/v1/auth/dev-login", nil, map[string]any{"email": email})
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("dev-login status = %d", res.StatusCode)
	}
	var payload struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	readJSON(h.t, res, &payload)
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			return c, payload.User.ID
		}
	}
	h.t.Fatal("missing session cookie")
	return nil, ""
}

func (h *harness) circleWithMembers(n int) (circleID string, cookies []*http.Cookie, userIDs []string) {
	h.t.Helper()
	owner, oid := h.login("own-" + uuid.NewString()[:8] + "@example.com")
	res := h.do(http.MethodPost, "/api/v1/circles", owner, map[string]any{
		"name": "Cut Club",
		"tz":   "America/Denver",
	})
	if res.StatusCode != http.StatusCreated {
		h.t.Fatalf("circle status = %d", res.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	readJSON(h.t, res, &created)
	cookies = []*http.Cookie{owner}
	userIDs = []string{oid}
	if n <= 1 {
		return created.ID, cookies, userIDs
	}
	res = h.do(http.MethodPost, "/api/v1/circles/"+created.ID+"/invites", owner, map[string]any{})
	var inv struct {
		Token string `json:"token"`
	}
	readJSON(h.t, res, &inv)
	for i := 1; i < n; i++ {
		c, uid := h.login("m" + uuid.NewString()[:8] + "@example.com")
		res = h.do(http.MethodPost, "/api/v1/invites/"+inv.Token+"/accept", c, map[string]any{})
		if res.StatusCode != http.StatusOK {
			h.t.Fatalf("accept status = %d", res.StatusCode)
		}
		res.Body.Close()
		cookies = append(cookies, c)
		userIDs = append(userIDs, uid)
	}
	return created.ID, cookies, userIDs
}

func errCode(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	b, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatalf("decode error envelope: %v body=%s", err, b)
	}
	return payload.Error.Code
}

func readJSON(t *testing.T, res *http.Response, dst any) {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("json: %v body=%s", err, b)
	}
}

func TestCreateNeverSilentEnrolls(t *testing.T) {
	h := newHarness(t)
	circleID, cookies, _ := h.circleWithMembers(3)

	res := h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", cookies[0], map[string]any{
		"name":        "Summer cut",
		"type":        "weight",
		"scoring_key": "weight.progress",
		"direction":   "at_most",
		"starts_at":   "2026-06-01T00:00:00-06:00",
		"ends_at":     "2026-09-01T00:00:00-06:00",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d code=%s", res.StatusCode, errCode(t, res))
	}
	var created challengeOut
	readJSON(t, res, &created)
	if created.JoinPolicy != "opt_in" {
		t.Fatalf("join_policy = %q", created.JoinPolicy)
	}
	if created.Joined || created.ParticipantCount != 0 {
		t.Fatalf("silent enroll: %+v", created)
	}
	var n int
	if err := h.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM challenge_participants WHERE challenge_id = $1`, created.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("participants = %d", n)
	}

	res = h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", cookies[0], map[string]any{
		"name":                "Enroll me",
		"type":                "habit",
		"starts_at":           "2026-06-01T00:00:00Z",
		"ends_at":             "2026-06-08T00:00:00Z",
		"share_matching_logs": true,
	})
	readJSON(t, res, &created)
	if !created.Joined || created.ParticipantCount != 1 {
		t.Fatalf("creator grant: %+v", created)
	}
	if err := h.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM challenge_participants WHERE challenge_id = $1`, created.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("participants after grant = %d", n)
	}
}

func TestJoinRequiresGrant(t *testing.T) {
	h := newHarness(t)
	circleID, cookies, _ := h.circleWithMembers(2)
	res := h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", cookies[0], map[string]any{
		"name":      "Habits",
		"type":      "habit",
		"starts_at": "2026-06-01T00:00:00Z",
		"ends_at":   "2026-06-08T00:00:00Z",
	})
	var created challengeOut
	readJSON(t, res, &created)

	res = h.do(http.MethodPost, "/api/v1/challenges/"+created.ID.String()+"/join", cookies[1], map[string]any{})
	if res.StatusCode != http.StatusBadRequest || errCode(t, res) != "invalid" {
		t.Fatalf("omitted grant status=%d", res.StatusCode)
	}
	res = h.do(http.MethodPost, "/api/v1/challenges/"+created.ID.String()+"/join", cookies[1], map[string]any{
		"share_matching_logs": false,
	})
	if res.StatusCode != http.StatusBadRequest || errCode(t, res) != "invalid" {
		t.Fatalf("false grant status=%d", res.StatusCode)
	}
	res = h.do(http.MethodPost, "/api/v1/challenges/"+created.ID.String()+"/join", cookies[1], map[string]any{
		"share_matching_logs": true,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("join status = %d code=%s", res.StatusCode, errCode(t, res))
	}
	var joined challengeOut
	readJSON(t, res, &joined)
	if !joined.Joined {
		t.Fatal("expected joined")
	}
}

func TestWeightDirectionRules(t *testing.T) {
	h := newHarness(t)
	circleID, cookies, _ := h.circleWithMembers(1)
	cookie := cookies[0]

	res := h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", cookie, map[string]any{
		"name":        "no dir",
		"type":        "weight",
		"scoring_key": "weight.progress",
		"starts_at":   "2026-06-01T00:00:00Z",
		"ends_at":     "2026-09-01T00:00:00Z",
	})
	if res.StatusCode != http.StatusBadRequest || errCode(t, res) != "invalid" {
		t.Fatalf("missing direction status=%d", res.StatusCode)
	}

	res = h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", cookie, map[string]any{
		"name":        "hit",
		"type":        "weight",
		"scoring_key": "weight.progress",
		"direction":   "hit",
		"starts_at":   "2026-06-01T00:00:00Z",
		"ends_at":     "2026-09-01T00:00:00Z",
	})
	if res.StatusCode != http.StatusBadRequest || errCode(t, res) != "invalid" {
		t.Fatalf("hit direction status=%d", res.StatusCode)
	}

	res = h.do(http.MethodPost, "/api/v1/rituals", cookie, map[string]any{
		"type":      "weight",
		"title":     "Cut",
		"direction": "at_most",
		"circle_id": circleID,
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("ritual status = %d", res.StatusCode)
	}
	var rit struct {
		ID string `json:"id"`
	}
	readJSON(t, res, &rit)

	res = h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", cookie, map[string]any{
		"name":        "mismatch",
		"type":        "weight",
		"scoring_key": "weight.progress",
		"direction":   "at_least",
		"ritual_id":   rit.ID,
		"starts_at":   "2026-06-01T00:00:00Z",
		"ends_at":     "2026-09-01T00:00:00Z",
	})
	if res.StatusCode != http.StatusBadRequest || errCode(t, res) != "invalid" {
		t.Fatalf("ritual mismatch status=%d", res.StatusCode)
	}

	res = h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", cookie, map[string]any{
		"name":        "match",
		"type":        "weight",
		"scoring_key": "weight.progress",
		"direction":   "at_most",
		"ritual_id":   rit.ID,
		"starts_at":   "2026-06-01T00:00:00Z",
		"ends_at":     "2026-09-01T00:00:00Z",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("match status = %d code=%s", res.StatusCode, errCode(t, res))
	}
	res.Body.Close()
}

func TestAutoTagAndStandings(t *testing.T) {
	h := newHarness(t)
	circleID, cookies, userIDs := h.circleWithMembers(3)
	alice, bob, carol := cookies[0], cookies[1], cookies[2]

	res := h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", alice, map[string]any{
		"name":                "Summer cut",
		"type":                "weight",
		"scoring_key":         "weight.progress",
		"direction":           "at_most",
		"starts_at":           "2026-06-01T06:00:00Z",
		"ends_at":             "2026-09-01T06:00:00Z",
		"share_matching_logs": true,
	})
	var ch challengeOut
	readJSON(t, res, &ch)

	// Existing private log in-window is not retroactively shared.
	res = h.do(http.MethodPost, "/api/v1/weights", bob, map[string]any{
		"kg":        90,
		"logged_at": "2026-06-15T12:00:00Z",
	})
	var prior struct {
		ID          string  `json:"id"`
		Visibility  string  `json:"visibility"`
		ChallengeID *string `json:"challenge_id"`
	}
	readJSON(t, res, &prior)
	if prior.Visibility != "private" || prior.ChallengeID != nil {
		t.Fatalf("prior = %+v", prior)
	}

	join := func(c *http.Cookie) {
		r := h.do(http.MethodPost, "/api/v1/challenges/"+ch.ID.String()+"/join", c, map[string]any{
			"share_matching_logs": true,
		})
		if r.StatusCode != http.StatusOK {
			t.Fatalf("join status = %d", r.StatusCode)
		}
		r.Body.Close()
	}
	join(bob)
	join(carol)

	res = h.do(http.MethodGet, "/api/v1/logs/"+prior.ID, bob, nil)
	readJSON(t, res, &prior)
	if prior.Visibility != "private" || prior.ChallengeID != nil {
		t.Fatalf("retroactive share: %+v", prior)
	}

	type wlog struct {
		Visibility  string  `json:"visibility"`
		ChallengeID *string `json:"challenge_id"`
	}
	logW := func(c *http.Cookie, kg float64, at string) wlog {
		r := h.do(http.MethodPost, "/api/v1/weights", c, map[string]any{"kg": kg, "logged_at": at})
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("weight status = %d code=%s", r.StatusCode, errCode(t, r))
		}
		var out wlog
		readJSON(t, r, &out)
		return out
	}

	// Baselines (at or before start) stay private; window logs auto-tag.
	aBase := logW(alice, 80, "2026-05-31T12:00:00Z")
	if aBase.Visibility != "private" {
		t.Fatalf("alice baseline vis = %s", aBase.Visibility)
	}
	aNow := logW(alice, 76, "2026-06-15T12:00:00Z")
	if aNow.Visibility != "challenge" || aNow.ChallengeID == nil || *aNow.ChallengeID != ch.ID.String() {
		t.Fatalf("alice window = %+v", aNow)
	}
	logW(bob, 90, "2026-05-31T12:00:00Z")
	bNow := logW(bob, 89, "2026-06-20T12:00:00Z")
	if bNow.Visibility != "challenge" {
		t.Fatalf("bob window vis = %s", bNow.Visibility)
	}
	logW(carol, 70, "2026-05-31T12:00:00Z")
	logW(carol, 71, "2026-06-15T12:00:00Z")

	var lc int
	if err := h.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM log_circles lc JOIN logs l ON l.id = lc.log_id WHERE l.challenge_id = $1`, ch.ID).Scan(&lc); err != nil {
		t.Fatal(err)
	}
	if lc < 3 {
		t.Fatalf("log_circles = %d", lc)
	}

	res = h.do(http.MethodGet, "/api/v1/challenges/"+ch.ID.String()+"/standings", alice, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("standings status = %d code=%s", res.StatusCode, errCode(t, res))
	}
	var body struct {
		Entries []standingEntry `json:"entries"`
	}
	readJSON(t, res, &body)
	if len(body.Entries) != 3 {
		t.Fatalf("entries = %d", len(body.Entries))
	}
	if body.Entries[0].UserID.String() != userIDs[0] || body.Entries[0].Points != 4 {
		t.Fatalf("rank0 = %+v want alice 4", body.Entries[0])
	}
	if body.Entries[1].UserID.String() != userIDs[1] || body.Entries[1].Points != 1 {
		t.Fatalf("rank1 = %+v want bob 1", body.Entries[1])
	}
	if body.Entries[2].UserID.String() != userIDs[2] || body.Entries[2].Points != 0 {
		t.Fatalf("rank2 = %+v want carol 0", body.Entries[2])
	}
}

func TestJoinPolicyAllMembersRejected(t *testing.T) {
	h := newHarness(t)
	circleID, cookies, _ := h.circleWithMembers(1)
	res := h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", cookies[0], map[string]any{
		"name":        "all",
		"type":        "habit",
		"join_policy": "all_members",
		"starts_at":   "2026-06-01T00:00:00Z",
		"ends_at":     "2026-06-08T00:00:00Z",
	})
	if res.StatusCode != http.StatusBadRequest || errCode(t, res) != "invalid" {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestLeave(t *testing.T) {
	h := newHarness(t)
	circleID, cookies, _ := h.circleWithMembers(1)
	res := h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", cookies[0], map[string]any{
		"name":                "h",
		"type":                "habit",
		"starts_at":           "2026-06-01T00:00:00Z",
		"ends_at":             "2026-06-08T00:00:00Z",
		"share_matching_logs": true,
	})
	var ch challengeOut
	readJSON(t, res, &ch)
	res = h.do(http.MethodPost, "/api/v1/challenges/"+ch.ID.String()+"/leave", cookies[0], map[string]any{})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("leave status = %d", res.StatusCode)
	}
	res.Body.Close()
}
