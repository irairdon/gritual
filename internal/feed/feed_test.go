package feed

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
	"github.com/irairdon/gritual/internal/challenges"
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
	chalAPI := challenges.New(cfg, pool)
	feedAPI := New(cfg, pool)
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
			feedAPI.Mount(r)
		}, nil),
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
		"name": "Feed Club",
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

type feedPost struct {
	ID          string         `json:"id"`
	Body        *string        `json:"body"`
	LogID       *string        `json:"log_id"`
	DisplayName string         `json:"display_name"`
	Log         *feedLog       `json:"log"`
	Comments    []feedComment  `json:"comments"`
	Reactions   map[string]int `json:"reactions"`
	MyReaction  *string        `json:"my_reaction"`
}

type feedLog struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Visibility string `json:"visibility"`
}

type feedComment struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

func (h *harness) feed(cookie *http.Cookie, circleID string) []feedPost {
	h.t.Helper()
	res := h.do(http.MethodGet, "/api/v1/circles/"+circleID+"/feed", cookie, nil)
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("feed status = %d code=%s", res.StatusCode, errCode(h.t, res))
	}
	var body struct {
		Items []feedPost `json:"items"`
	}
	readJSON(h.t, res, &body)
	if body.Items == nil {
		return []feedPost{}
	}
	return body.Items
}

func hasBody(items []feedPost, body string) bool {
	for _, p := range items {
		if p.Body != nil && *p.Body == body {
			return true
		}
	}
	return false
}

func hasLog(items []feedPost, logID string) bool {
	for _, p := range items {
		if p.LogID != nil && *p.LogID == logID {
			return true
		}
		if p.Log != nil && p.Log.ID == logID {
			return true
		}
	}
	return false
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

func TestFeedVisibilityPredicate(t *testing.T) {
	h := newHarness(t)
	circleID, cookies, userIDs := h.circleWithMembers(3)
	alice, bob, carol := cookies[0], cookies[1], cookies[2]
	aliceID := userIDs[0]

	res := h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/feed", alice, map[string]any{
		"body": "hello circle",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("freeform status = %d code=%s", res.StatusCode, errCode(t, res))
	}
	res.Body.Close()

	res = h.do(http.MethodPost, "/api/v1/rituals", alice, map[string]any{
		"type":      "workout",
		"title":     "Gym",
		"circle_id": circleID,
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("ritual status = %d", res.StatusCode)
	}
	var rit struct {
		ID string `json:"id"`
	}
	readJSON(t, res, &rit)

	res = h.do(http.MethodPost, "/api/v1/workouts", bob, map[string]any{
		"title":     "Lower",
		"ritual_id": rit.ID,
		"sets":      []map[string]any{{"exercise": "squat", "reps": 5, "weight_kg": 100, "ordinal": 0}},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("circle workout status = %d code=%s", res.StatusCode, errCode(t, res))
	}
	var circleLog struct {
		ID         string `json:"id"`
		Visibility string `json:"visibility"`
	}
	readJSON(t, res, &circleLog)
	if circleLog.Visibility != "circle" {
		t.Fatalf("circle log vis = %q", circleLog.Visibility)
	}

	res = h.do(http.MethodPost, "/api/v1/weights", carol, map[string]any{"kg": 80})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("private weight status = %d", res.StatusCode)
	}
	var privateLog struct {
		ID         string `json:"id"`
		Visibility string `json:"visibility"`
	}
	readJSON(t, res, &privateLog)
	if privateLog.Visibility != "private" {
		t.Fatalf("private vis = %q", privateLog.Visibility)
	}
	// Row exists so GET, not insert-path, is the gate. Private is never listed.
	if _, err := h.pool.Exec(context.Background(), `
		INSERT INTO feed_posts (circle_id, user_id, log_id, body)
		VALUES ($1, $2, $3, 'private leak')
	`, circleID, userIDs[2], privateLog.ID); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	res = h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/challenges", alice, map[string]any{
		"name":                "Volume",
		"type":                "workout",
		"scoring_key":         "workout.volume",
		"starts_at":           now.Add(-time.Hour).Format(time.RFC3339),
		"ends_at":             now.Add(24 * time.Hour).Format(time.RFC3339),
		"share_matching_logs": true,
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("challenge status = %d code=%s", res.StatusCode, errCode(t, res))
	}
	res.Body.Close()

	res = h.do(http.MethodPost, "/api/v1/workouts", alice, map[string]any{
		"title": "Challenge lift",
		"sets":  []map[string]any{{"exercise": "bench", "reps": 5, "weight_kg": 80, "ordinal": 0}},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("challenge workout status = %d code=%s", res.StatusCode, errCode(t, res))
	}
	var chalLog struct {
		ID          string  `json:"id"`
		Visibility  string  `json:"visibility"`
		ChallengeID *string `json:"challenge_id"`
	}
	readJSON(t, res, &chalLog)
	if chalLog.Visibility != "challenge" || chalLog.ChallengeID == nil {
		t.Fatalf("expected challenge auto-tag, got vis=%q cid=%v", chalLog.Visibility, chalLog.ChallengeID)
	}

	var draftID string
	if err := h.pool.QueryRow(context.Background(), `
		INSERT INTO logs (user_id, type, logged_at, visibility, source)
		VALUES ($1, 'meal', now(), 'circle', 'app')
		RETURNING id::text
	`, aliceID).Scan(&draftID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(context.Background(), `INSERT INTO meals (log_id, status) VALUES ($1, 'draft')`, draftID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(context.Background(), `INSERT INTO log_circles (log_id, circle_id) VALUES ($1, $2)`, draftID, circleID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(context.Background(), `
		INSERT INTO feed_posts (circle_id, user_id, log_id, body) VALUES ($1, $2, $3, 'draft meal')
	`, circleID, aliceID, draftID); err != nil {
		t.Fatal(err)
	}

	dave, _ := h.login("dave-" + uuid.NewString()[:8] + "@example.com")

	tests := []struct {
		name   string
		cookie *http.Cookie
		want   map[string]bool
	}{
		{
			name:   "participant sees freeform circle and own challenge, never private or draft",
			cookie: alice,
			want: map[string]bool{
				"hello circle": true,
				"circleLog":    true,
				"chalLog":      true,
				"private":      false,
				"draft":        false,
			},
		},
		{
			name:   "non-participant hides challenge-only even with log_circles",
			cookie: bob,
			want: map[string]bool{
				"hello circle": true,
				"circleLog":    true,
				"chalLog":      false,
				"private":      false,
				"draft":        false,
			},
		},
		{
			name:   "author of private log still does not see it in feed",
			cookie: carol,
			want: map[string]bool{
				"hello circle": true,
				"circleLog":    true,
				"chalLog":      false,
				"private":      false,
				"draft":        false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := h.feed(tt.cookie, circleID)
			got := map[string]bool{
				"hello circle": hasBody(items, "hello circle"),
				"circleLog":    hasLog(items, circleLog.ID),
				"chalLog":      hasLog(items, chalLog.ID),
				"private":      hasLog(items, privateLog.ID) || hasBody(items, "private leak"),
				"draft":        hasLog(items, draftID) || hasBody(items, "draft meal"),
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Errorf("%s: got %v want %v items=%+v", k, got[k], want, items)
				}
			}
		})
	}

	t.Run("non-member 404", func(t *testing.T) {
		res := h.do(http.MethodGet, "/api/v1/circles/"+circleID+"/feed", dave, nil)
		if res.StatusCode != http.StatusNotFound || errCode(t, res) != "not_found" {
			t.Fatalf("outsider status=%d", res.StatusCode)
		}
	})

	t.Run("deleted log never listed", func(t *testing.T) {
		res := h.do(http.MethodDelete, "/api/v1/logs/"+circleLog.ID, bob, nil)
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("delete log status=%d", res.StatusCode)
		}
		res.Body.Close()
		items := h.feed(alice, circleID)
		if hasLog(items, circleLog.ID) {
			t.Fatal("deleted log still listed")
		}
	})
}

func TestFeedCommentsAndReactions(t *testing.T) {
	h := newHarness(t)
	circleID, cookies, _ := h.circleWithMembers(2)
	alice, bob := cookies[0], cookies[1]

	res := h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/feed", alice, map[string]any{
		"body": "good morning",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("post status = %d", res.StatusCode)
	}
	var post feedPost
	readJSON(t, res, &post)

	res = h.do(http.MethodPost, "/api/v1/posts/"+post.ID+"/comments", bob, map[string]any{
		"body": "nice",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("comment status = %d code=%s", res.StatusCode, errCode(t, res))
	}
	var cmt feedComment
	readJSON(t, res, &cmt)

	res = h.do(http.MethodPut, "/api/v1/posts/"+post.ID+"/reactions", alice, map[string]any{"emoji": "fire"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put fire status = %d", res.StatusCode)
	}
	res.Body.Close()
	res = h.do(http.MethodPut, "/api/v1/posts/"+post.ID+"/reactions", alice, map[string]any{"emoji": "heart"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put heart status = %d", res.StatusCode)
	}
	res.Body.Close()
	res = h.do(http.MethodPut, "/api/v1/posts/"+post.ID+"/reactions", bob, map[string]any{"emoji": "fire"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bob fire status = %d", res.StatusCode)
	}
	res.Body.Close()

	res = h.do(http.MethodPut, "/api/v1/posts/"+post.ID+"/reactions", bob, map[string]any{"emoji": "sparkle"})
	if res.StatusCode != http.StatusBadRequest || errCode(t, res) != "invalid" {
		t.Fatalf("invalid emoji status=%d", res.StatusCode)
	}

	items := h.feed(alice, circleID)
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	got := items[0]
	if len(got.Comments) != 1 || got.Comments[0].Body != "nice" {
		t.Fatalf("comments = %+v", got.Comments)
	}
	if got.Reactions["heart"] != 1 || got.Reactions["fire"] != 1 {
		t.Fatalf("reactions = %+v", got.Reactions)
	}
	if got.MyReaction == nil || *got.MyReaction != "heart" {
		t.Fatalf("my_reaction = %v", got.MyReaction)
	}

	res = h.do(http.MethodDelete, "/api/v1/posts/"+post.ID+"/reactions", alice, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete reaction status=%d", res.StatusCode)
	}
	res.Body.Close()
	items = h.feed(alice, circleID)
	if items[0].Reactions["heart"] != 0 || items[0].MyReaction != nil {
		t.Fatalf("after delete reactions=%+v my=%v", items[0].Reactions, items[0].MyReaction)
	}

	res = h.do(http.MethodDelete, "/api/v1/comments/"+cmt.ID, bob, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("author delete comment status=%d", res.StatusCode)
	}
	res.Body.Close()
	items = h.feed(alice, circleID)
	if len(items[0].Comments) != 0 {
		t.Fatalf("deleted comment still listed: %+v", items[0].Comments)
	}

	res = h.do(http.MethodPost, "/api/v1/posts/"+post.ID+"/comments", bob, map[string]any{"body": "again"})
	readJSON(t, res, &cmt)
	res = h.do(http.MethodDelete, "/api/v1/comments/"+cmt.ID, alice, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("owner delete comment status=%d", res.StatusCode)
	}
	res.Body.Close()
}

func TestFeedCreateValidation(t *testing.T) {
	h := newHarness(t)
	circleID, cookies, _ := h.circleWithMembers(1)
	res := h.do(http.MethodPost, "/api/v1/circles/"+circleID+"/feed", cookies[0], map[string]any{"body": "   "})
	if res.StatusCode != http.StatusBadRequest || errCode(t, res) != "invalid" {
		t.Fatalf("blank body status=%d", res.StatusCode)
	}
}
