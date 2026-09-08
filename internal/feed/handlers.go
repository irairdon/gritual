package feed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/httpx"
)

var errNotFound = errors.New("not found")

type createPostReq struct {
	Body string `json:"body"`
}

type createCommentReq struct {
	Body string `json:"body"`
}

type putReactionReq struct {
	Emoji string `json:"emoji"`
}

// visibleLogSQL is the spec feed predicate for log-backed posts.
// Visibility is not ordered: challenge is not a superset of circle, so private
// never matches and challenge requires a participant row (not mere membership).
func visibleLogSQL(viewerParam int) string {
	return fmt.Sprintf(`(
		p.log_id IS NULL
		OR (
			l.id IS NOT NULL AND l.deleted_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM meals meal WHERE meal.log_id = l.id AND meal.status = 'draft')
			AND (
				(l.visibility = 'circle' AND EXISTS (
					SELECT 1 FROM log_circles lc WHERE lc.log_id = l.id AND lc.circle_id = p.circle_id
				))
				OR (
					l.visibility = 'challenge'
					AND EXISTS (
						SELECT 1 FROM challenges ch
						WHERE ch.id = l.challenge_id AND ch.circle_id = p.circle_id
					)
					AND EXISTS (
						SELECT 1 FROM challenge_participants cp
						WHERE cp.challenge_id = l.challenge_id AND cp.user_id = $%d
					)
				)
			)
		)
	)`, viewerParam)
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	circleID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	u := auth.UserFrom(r.Context())
	if _, err := a.circleRole(r.Context(), circleID, u.ID); err != nil {
		writeErr(w, err, "circle not found")
		return
	}

	limit := defaultLimit
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid limit")
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	pred := visibleLogSQL(2)
	var rows pgx.Rows
	var err error
	if cursor == "" {
		rows, err = a.pool.Query(r.Context(), `
			SELECT p.id, p.circle_id, p.user_id, p.log_id, p.challenge_id, p.body, p.created_at,
			       COALESCE(u.display_name, '')
			FROM feed_posts p
			LEFT JOIN users u ON u.id = p.user_id
			LEFT JOIN logs l ON l.id = p.log_id
			WHERE p.circle_id = $1 AND p.deleted_at IS NULL AND `+pred+`
			ORDER BY p.created_at DESC, p.id DESC
			LIMIT $3
		`, circleID, u.ID, limit+1)
	} else {
		cid, parseErr := uuid.Parse(cursor)
		if parseErr != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid cursor")
			return
		}
		rows, err = a.pool.Query(r.Context(), `
			SELECT p.id, p.circle_id, p.user_id, p.log_id, p.challenge_id, p.body, p.created_at,
			       COALESCE(u.display_name, '')
			FROM feed_posts p
			LEFT JOIN users u ON u.id = p.user_id
			LEFT JOIN logs l ON l.id = p.log_id
			WHERE p.circle_id = $1 AND p.deleted_at IS NULL AND `+pred+`
			  AND (p.created_at, p.id) < (SELECT created_at, id FROM feed_posts WHERE id = $3)
			ORDER BY p.created_at DESC, p.id DESC
			LIMIT $4
		`, circleID, u.ID, cid, limit+1)
	}
	if err != nil {
		slog.Error("feed list", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	items, err := scanPosts(rows)
	if err != nil {
		slog.Error("feed list scan", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		n := items[len(items)-1].ID.String()
		next = &n
	}
	if err := a.attachExtras(r.Context(), items, u.ID); err != nil {
		slog.Error("feed extras", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	circleID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "circle not found")
		return
	}
	var req createPostReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	u := auth.UserFrom(r.Context())
	if _, err := a.circleRole(r.Context(), circleID, u.ID); err != nil {
		writeErr(w, err, "circle not found")
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "body required")
		return
	}
	if len(body) > maxPostBody {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "body too long")
		return
	}

	var out postOut
	err := a.pool.QueryRow(r.Context(), `
		INSERT INTO feed_posts (circle_id, user_id, body)
		VALUES ($1, $2, $3)
		RETURNING id, circle_id, user_id, log_id, challenge_id, body, created_at
	`, circleID, u.ID, body).Scan(
		&out.ID, &out.CircleID, &out.UserID, &out.LogID, &out.ChallengeID, &out.Body, &out.CreatedAt,
	)
	if err != nil {
		slog.Error("feed create", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	out.DisplayName = u.DisplayName
	out.Comments = []commentOut{}
	out.Reactions = emptyReactions()
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	postID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "post not found")
		return
	}
	var req createCommentReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "body required")
		return
	}
	if len(body) > maxCommentBody {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "body too long")
		return
	}
	u := auth.UserFrom(r.Context())
	if _, err := a.loadVisiblePost(r.Context(), postID, u.ID); err != nil {
		writeErr(w, err, "post not found")
		return
	}

	var out commentOut
	err := a.pool.QueryRow(r.Context(), `
		INSERT INTO comments (post_id, user_id, body)
		VALUES ($1, $2, $3)
		RETURNING id, post_id, user_id, body, created_at
	`, postID, u.ID, body).Scan(&out.ID, &out.PostID, &out.UserID, &out.Body, &out.CreatedAt)
	if err != nil {
		slog.Error("feed comment", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	out.DisplayName = u.DisplayName
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (a *API) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "comment not found")
		return
	}
	u := auth.UserFrom(r.Context())
	var postID uuid.UUID
	var author *uuid.UUID
	var circleID uuid.UUID
	err := a.pool.QueryRow(r.Context(), `
		SELECT c.post_id, c.user_id, p.circle_id
		FROM comments c
		JOIN feed_posts p ON p.id = c.post_id
		WHERE c.id = $1 AND c.deleted_at IS NULL
	`, id).Scan(&postID, &author, &circleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "comment not found")
			return
		}
		slog.Error("feed comment load", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	if _, err := a.loadVisiblePost(r.Context(), postID, u.ID); err != nil {
		writeErr(w, err, "comment not found")
		return
	}
	allowed := author != nil && *author == u.ID
	if !allowed {
		role, err := a.circleRole(r.Context(), circleID, u.ID)
		if err != nil {
			writeErr(w, err, "comment not found")
			return
		}
		allowed = canManage(role)
	}
	if !allowed {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if _, err := a.pool.Exec(r.Context(), `UPDATE comments SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id); err != nil {
		slog.Error("feed comment delete", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePutReaction(w http.ResponseWriter, r *http.Request) {
	postID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "post not found")
		return
	}
	var req putReactionReq
	if err := httpx.ReadJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid json")
		return
	}
	emoji := strings.TrimSpace(req.Emoji)
	if !validReaction(emoji) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid emoji")
		return
	}
	u := auth.UserFrom(r.Context())
	if _, err := a.loadVisiblePost(r.Context(), postID, u.ID); err != nil {
		writeErr(w, err, "post not found")
		return
	}
	if _, err := a.pool.Exec(r.Context(), `
		INSERT INTO reactions (post_id, user_id, emoji) VALUES ($1, $2, $3)
		ON CONFLICT (post_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji
	`, postID, u.ID, emoji); err != nil {
		if isCheckViolation(err) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid", "invalid emoji")
			return
		}
		slog.Error("feed reaction put", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"emoji": emoji})
}

func (a *API) handleDeleteReaction(w http.ResponseWriter, r *http.Request) {
	postID, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "post not found")
		return
	}
	u := auth.UserFrom(r.Context())
	if _, err := a.loadVisiblePost(r.Context(), postID, u.ID); err != nil {
		writeErr(w, err, "post not found")
		return
	}
	if _, err := a.pool.Exec(r.Context(), `DELETE FROM reactions WHERE post_id = $1 AND user_id = $2`, postID, u.ID); err != nil {
		slog.Error("feed reaction delete", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) loadVisiblePost(ctx context.Context, postID, viewer uuid.UUID) (*postOut, error) {
	pred := visibleLogSQL(2)
	var out postOut
	err := a.pool.QueryRow(ctx, `
		SELECT p.id, p.circle_id, p.user_id, p.log_id, p.challenge_id, p.body, p.created_at,
		       COALESCE(u.display_name, '')
		FROM feed_posts p
		LEFT JOIN users u ON u.id = p.user_id
		LEFT JOIN logs l ON l.id = p.log_id
		WHERE p.id = $1 AND p.deleted_at IS NULL
		  AND EXISTS (
			SELECT 1 FROM circle_members m
			JOIN circles c ON c.id = m.circle_id AND c.deleted_at IS NULL
			WHERE m.circle_id = p.circle_id AND m.user_id = $2
		  )
		  AND `+pred, postID, viewer).Scan(
		&out.ID, &out.CircleID, &out.UserID, &out.LogID, &out.ChallengeID, &out.Body, &out.CreatedAt, &out.DisplayName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (a *API) circleRole(ctx context.Context, circleID, userID uuid.UUID) (string, error) {
	var role string
	err := a.pool.QueryRow(ctx, `
		SELECT m.role
		FROM circle_members m
		JOIN circles c ON c.id = m.circle_id AND c.deleted_at IS NULL
		WHERE m.circle_id = $1 AND m.user_id = $2
	`, circleID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", err
	}
	return role, nil
}

func (a *API) attachExtras(ctx context.Context, items []postOut, viewer uuid.UUID) error {
	if items == nil {
		return nil
	}
	for i := range items {
		if items[i].Comments == nil {
			items[i].Comments = []commentOut{}
		}
		if items[i].Reactions == nil {
			items[i].Reactions = emptyReactions()
		}
	}
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	idx := make(map[uuid.UUID]int, len(items))
	logIDs := make([]uuid.UUID, 0)
	for i, p := range items {
		ids[i] = p.ID
		idx[p.ID] = i
		if p.LogID != nil {
			logIDs = append(logIDs, *p.LogID)
		}
	}

	cRows, err := a.pool.Query(ctx, `
		SELECT c.id, c.post_id, c.user_id, COALESCE(u.display_name, ''), c.body, c.created_at
		FROM comments c
		LEFT JOIN users u ON u.id = c.user_id
		WHERE c.post_id = ANY($1) AND c.deleted_at IS NULL
		ORDER BY c.created_at ASC, c.id ASC
	`, ids)
	if err != nil {
		return err
	}
	defer cRows.Close()
	for cRows.Next() {
		var c commentOut
		if err := cRows.Scan(&c.ID, &c.PostID, &c.UserID, &c.DisplayName, &c.Body, &c.CreatedAt); err != nil {
			return err
		}
		i, ok := idx[c.PostID]
		if !ok {
			continue
		}
		items[i].Comments = append(items[i].Comments, c)
	}
	if err := cRows.Err(); err != nil {
		return err
	}

	rRows, err := a.pool.Query(ctx, `
		SELECT post_id, emoji, COUNT(*)::int
		FROM reactions
		WHERE post_id = ANY($1)
		GROUP BY post_id, emoji
	`, ids)
	if err != nil {
		return err
	}
	defer rRows.Close()
	for rRows.Next() {
		var postID uuid.UUID
		var emoji string
		var n int
		if err := rRows.Scan(&postID, &emoji, &n); err != nil {
			return err
		}
		i, ok := idx[postID]
		if !ok {
			continue
		}
		items[i].Reactions[emoji] = n
	}
	if err := rRows.Err(); err != nil {
		return err
	}

	mRows, err := a.pool.Query(ctx, `
		SELECT post_id, emoji FROM reactions WHERE post_id = ANY($1) AND user_id = $2
	`, ids, viewer)
	if err != nil {
		return err
	}
	defer mRows.Close()
	for mRows.Next() {
		var postID uuid.UUID
		var emoji string
		if err := mRows.Scan(&postID, &emoji); err != nil {
			return err
		}
		i, ok := idx[postID]
		if !ok {
			continue
		}
		e := emoji
		items[i].MyReaction = &e
	}
	if err := mRows.Err(); err != nil {
		return err
	}

	if len(logIDs) == 0 {
		return nil
	}
	lRows, err := a.pool.Query(ctx, `
		SELECT id, type, logged_at, visibility, notes
		FROM logs
		WHERE id = ANY($1)
	`, logIDs)
	if err != nil {
		return err
	}
	defer lRows.Close()
	logs := make(map[uuid.UUID]logSnippet)
	for lRows.Next() {
		var s logSnippet
		if err := lRows.Scan(&s.ID, &s.Type, &s.LoggedAt, &s.Visibility, &s.Notes); err != nil {
			return err
		}
		logs[s.ID] = s
	}
	if err := lRows.Err(); err != nil {
		return err
	}
	for i := range items {
		if items[i].LogID == nil {
			continue
		}
		if s, ok := logs[*items[i].LogID]; ok {
			cp := s
			items[i].Log = &cp
		}
	}
	return nil
}

func scanPosts(rows pgx.Rows) ([]postOut, error) {
	defer rows.Close()
	items := make([]postOut, 0)
	for rows.Next() {
		var p postOut
		if err := rows.Scan(&p.ID, &p.CircleID, &p.UserID, &p.LogID, &p.ChallengeID, &p.Body, &p.CreatedAt, &p.DisplayName); err != nil {
			return nil, err
		}
		p.Comments = []commentOut{}
		p.Reactions = emptyReactions()
		items = append(items, p)
	}
	return items, rows.Err()
}

func writeErr(w http.ResponseWriter, err error, notFoundMsg string) {
	if errors.Is(err, errNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", notFoundMsg)
		return
	}
	slog.Error("feed", "err", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}

func parseUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
