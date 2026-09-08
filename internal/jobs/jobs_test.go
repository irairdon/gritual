package jobs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/irairdon/gritual/internal/challenges"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/db"
)

func TestJobsClaimReclaimAndGC(t *testing.T) {
	pool := db.TestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := config.Config{
		SessionSecret: bytes.Repeat([]byte("s"), 32),
		MediaDir:      dir,
		AppBaseURL:    "http://localhost:8080",
	}
	p := New(cfg, pool, challenges.New(cfg, pool))
	p.id = "tester:1"

	t.Run("reclaim stale lock", func(t *testing.T) {
		var id uuid.UUID
		err := pool.QueryRow(ctx, `
			INSERT INTO jobs (kind, payload, run_at, locked_at, locked_by)
			VALUES ('vision_cache_gc', '{}', now(), now() - interval '3 minutes', 'dead:9')
			RETURNING id
		`).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		job, err := p.claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if job.ID != id {
			t.Fatalf("claimed %s want %s", job.ID, id)
		}
		if job.Kind != "vision_cache_gc" {
			t.Fatalf("kind = %s", job.Kind)
		}
		var lockedBy string
		if err := pool.QueryRow(ctx, `SELECT locked_by FROM jobs WHERE id = $1`, id).Scan(&lockedBy); err != nil {
			t.Fatal(err)
		}
		if lockedBy != p.id {
			t.Fatalf("locked_by = %q", lockedBy)
		}
		_, _ = pool.Exec(ctx, `UPDATE jobs SET done_at = now() WHERE id = $1`, id)
	})

	t.Run("vision_cache_gc deletes old rows", func(t *testing.T) {
		sha := bytes.Repeat([]byte{1}, 32)
		if _, err := pool.Exec(ctx, `
			INSERT INTO vision_cache (sha256, result, model, created_at)
			VALUES ($1, '{}'::jsonb, 'grok-4.5', now() - interval '31 days')
		`, sha); err != nil {
			t.Fatal(err)
		}
		fresh := bytes.Repeat([]byte{2}, 32)
		if _, err := pool.Exec(ctx, `
			INSERT INTO vision_cache (sha256, result, model, created_at)
			VALUES ($1, '{}'::jsonb, 'grok-4.5', now())
		`, fresh); err != nil {
			t.Fatal(err)
		}
		if err := p.runJob(ctx, &job{Kind: "vision_cache_gc"}); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM vision_cache WHERE sha256 = $1`, sha).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("old cache row remained")
		}
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM vision_cache WHERE sha256 = $1`, fresh).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatal("fresh cache row deleted")
		}
	})

	t.Run("media_gc unreferenced after 24h", func(t *testing.T) {
		userID := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, email, display_name, dob, password_hash)
			VALUES ($1, $2, 'G', '1990-01-01', 'x')
		`, userID, "gc-"+uuid.NewString()[:8]+"@example.com"); err != nil {
			t.Fatal(err)
		}
		rel := filepath.Join(userID.String(), "dead.jpeg")
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_objects (user_id, sha256, content_type, bytes, path, created_at)
			VALUES ($1, $2, 'image/jpeg', 1, $3, now() - interval '25 hours')
		`, userID, bytes.Repeat([]byte{9}, 32), rel); err != nil {
			t.Fatal(err)
		}
		if err := p.runJob(ctx, &job{Kind: "media_gc"}); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM media_objects WHERE path = $1`, rel).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("media row remained")
		}
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			t.Fatalf("file still exists: %v", err)
		}
	})
}
