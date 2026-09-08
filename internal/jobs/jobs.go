package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "time/tzdata"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/challenges"
	"github.com/irairdon/gritual/internal/config"
)

type Poller struct {
	cfg  config.Config
	pool *pgxpool.Pool
	chal *challenges.API
	id   string
}

func New(cfg config.Config, pool *pgxpool.Pool, chal *challenges.API) *Poller {
	host, _ := os.Hostname()
	return &Poller{
		cfg:  cfg,
		pool: pool,
		chal: chal,
		id:   fmt.Sprintf("%s:%d", host, os.Getpid()),
	}
}

func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	_ = p.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = p.Tick(ctx)
		}
	}
}

func (p *Poller) Tick(ctx context.Context) error {
	if err := p.ensure(ctx); err != nil {
		slog.Error("jobs ensure", "err", err)
	}
	for i := 0; i < 8; i++ {
		job, err := p.claim(ctx)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil
			}
			slog.Error("jobs claim", "err", err)
			return err
		}
		if err := p.runJob(ctx, job); err != nil {
			slog.Error("jobs run", "kind", job.Kind, "err", err)
			_, _ = p.pool.Exec(ctx, `
				UPDATE jobs SET last_error = $2, locked_at = NULL, locked_by = NULL, run_at = now() + interval '1 minute'
				WHERE id = $1
			`, job.ID, err.Error())
			continue
		}
		_, _ = p.pool.Exec(ctx, `
			UPDATE jobs SET done_at = now(), last_error = NULL WHERE id = $1
		`, job.ID)
	}
	return nil
}

type job struct {
	ID      uuid.UUID
	Kind    string
	Payload json.RawMessage
}

func (p *Poller) claim(ctx context.Context) (*job, error) {
	var j job
	err := p.pool.QueryRow(ctx, `
		WITH next AS (
			SELECT id
			FROM jobs
			WHERE done_at IS NULL
			  AND run_at <= now()
			  AND (locked_at IS NULL OR locked_at < now() - interval '2 minutes')
			ORDER BY run_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE jobs j
		SET locked_at = now(), locked_by = $1, attempts = attempts + 1
		FROM next
		WHERE j.id = next.id
		RETURNING j.id, j.kind, j.payload
	`, p.id).Scan(&j.ID, &j.Kind, &j.Payload)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (p *Poller) ensure(ctx context.Context) error {
	kinds := []string{"media_gc", "vision_cache_gc", "recap"}
	for _, k := range kinds {
		_, err := p.pool.Exec(ctx, `
			INSERT INTO jobs (kind, payload, run_at)
			SELECT $1, '{}'::jsonb, now()
			WHERE NOT EXISTS (
				SELECT 1 FROM jobs WHERE kind = $1 AND done_at IS NULL
			)
		`, k)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Poller) runJob(ctx context.Context, j *job) error {
	switch j.Kind {
	case "vision_cache_gc":
		_, err := p.pool.Exec(ctx, `DELETE FROM vision_cache WHERE created_at < now() - interval '30 days'`)
		if err != nil {
			return err
		}
		return p.requeue(ctx, "vision_cache_gc", time.Hour)
	case "media_gc":
		if err := p.mediaGC(ctx); err != nil {
			return err
		}
		return p.requeue(ctx, "media_gc", time.Hour)
	case "recap":
		if err := p.recap(ctx); err != nil {
			return err
		}
		return p.requeue(ctx, "recap", time.Hour)
	default:
		return fmt.Errorf("unknown kind %s", j.Kind)
	}
}

func (p *Poller) requeue(ctx context.Context, kind string, d time.Duration) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO jobs (kind, payload, run_at) VALUES ($1, '{}'::jsonb, now() + $2)
	`, kind, d)
	return err
}

func (p *Poller) mediaGC(ctx context.Context) error {
	rows, err := p.pool.Query(ctx, `
		SELECT id, path FROM media_objects m
		WHERE m.created_at < now() - interval '24 hours'
		  AND NOT EXISTS (SELECT 1 FROM logs l WHERE l.media_id = m.id)
		  AND NOT EXISTS (SELECT 1 FROM profiles p WHERE p.avatar_media_id = m.id)
		  AND NOT EXISTS (SELECT 1 FROM meals meal WHERE meal.photo_media_id = m.id)
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		id   uuid.UUID
		path string
	}
	var doomed []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.path); err != nil {
			return err
		}
		doomed = append(doomed, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range doomed {
		if _, err := p.pool.Exec(ctx, `DELETE FROM media_objects WHERE id = $1`, r.id); err != nil {
			return err
		}
		full := r.path
		if !filepath.IsAbs(full) {
			full = filepath.Join(p.cfg.MediaDir, r.path)
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			slog.Error("media_gc unlink", "path", full, "err", err)
		}
	}
	return nil
}

func (p *Poller) recap(ctx context.Context) error {
	rows, err := p.pool.Query(ctx, `
		SELECT id, tz FROM circles WHERE deleted_at IS NULL
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type circ struct {
		id uuid.UUID
		tz string
	}
	var circles []circ
	for rows.Next() {
		var c circ
		if err := rows.Scan(&c.id, &c.tz); err != nil {
			return err
		}
		circles = append(circles, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	now := time.Now()
	for _, c := range circles {
		loc, err := time.LoadLocation(c.tz)
		if err != nil {
			loc = time.UTC
		}
		local := now.In(loc)
		if local.Weekday() != time.Sunday || local.Hour() < 18 {
			continue
		}
		weekStart := time.Date(local.Year(), local.Month(), local.Day(), 18, 0, 0, 0, loc)
		var exists bool
		if err := p.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM feed_posts
				WHERE circle_id = $1 AND log_id IS NULL AND user_id IS NULL
				  AND body LIKE 'Weekly recap%'
				  AND created_at >= $2 AND deleted_at IS NULL
			)
		`, c.id, weekStart).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := p.recapBody(ctx, c.id)
		if err != nil {
			return err
		}
		if _, err := p.pool.Exec(ctx, `
			INSERT INTO feed_posts (circle_id, user_id, body)
			VALUES ($1, NULL, $2)
		`, c.id, body); err != nil {
			return err
		}
	}
	return nil
}

func (p *Poller) recapBody(ctx context.Context, circleID uuid.UUID) (string, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, name FROM challenges WHERE circle_id = $1 ORDER BY starts_at DESC, id
	`, circleID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	b.WriteString("Weekly recap")
	n := 0
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return "", err
		}
		n++
		b.WriteString("\n\n")
		b.WriteString(name)
		if p.chal == nil {
			continue
		}
		entries, err := p.chal.StandingsSnapshot(ctx, id)
		if err != nil {
			slog.Error("recap standings", "challenge", id, "err", err)
			continue
		}
		for i, e := range entries {
			fmt.Fprintf(&b, "\n%d. %s — %.1f", i+1, e.DisplayName, e.Points)
		}
	}
	if n == 0 {
		b.WriteString("\nNo active challenges.")
	}
	return b.String(), rows.Err()
}
