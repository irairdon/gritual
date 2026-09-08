package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationFilenamesSort(t *testing.T) {
	names, err := listMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("embedded migrations is empty")
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	if strings.Join(names, ",") != strings.Join(sorted, ",") {
		t.Fatalf("migrations not in lexical order: %v", names)
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".sql") {
			t.Fatalf("%s is not a .sql file", name)
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
}

func TestRunMigrationsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := integrationPool(t, ctx)

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	for _, table := range []string{"users", "custom_logs", "challenges", "jobs"} {
		var exists bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.tables
				WHERE table_schema = current_schema()
				  AND table_name = $1
			)
		`, table).Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("missing table %s", table)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("schema_migrations has no rows")
	}

	var oauth int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name LIKE 'oauth%'
	`).Scan(&oauth); err != nil {
		t.Fatal(err)
	}
	if oauth != 0 {
		t.Fatalf("oauth_* tables present: %d", oauth)
	}
}

func integrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://gritual:gritual@127.0.0.1:5432/gritual?sslmode=disable"
	}

	pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
	admin, err := Open(pingCtx, dsn)
	pingCancel()
	if err != nil {
		if !startComposePostgres(t) {
			t.Skipf("postgres unavailable: %v", err)
		}
		waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
		defer waitCancel()
		admin, err = waitOpen(waitCtx, dsn)
		if err != nil {
			t.Skipf("postgres unavailable: %v", err)
		}
	}

	name, err := uniqueDBName()
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Logf("CREATE DATABASE %s: %v; using shared database", name, err)
		t.Cleanup(admin.Close)
		return admin
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close()
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close()
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(dropCtx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close()
	})
	return pool
}

func uniqueDBName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "gritual_migtest_" + hex.EncodeToString(b[:]), nil
}

func waitOpen(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	var last error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		pool, err := Open(attemptCtx, dsn)
		cancel()
		if err == nil {
			return pool, nil
		}
		last = err
		select {
		case <-ctx.Done():
			if last == nil {
				last = ctx.Err()
			}
			return nil, last
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func startComposePostgres(t *testing.T) bool {
	t.Helper()
	dir, err := findComposeDir()
	if err != nil {
		t.Log(err)
		return false
	}
	cmd := exec.Command("docker", "compose", "up", "-d", "postgres")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("docker compose up: %v\n%s", err, out)
		return false
	}
	ps := exec.Command("docker", "compose", "ps", "--status", "running", "--services")
	ps.Dir = dir
	psOut, err := ps.CombinedOutput()
	if err != nil {
		t.Logf("docker compose ps: %v\n%s", err, psOut)
		return false
	}
	for _, line := range strings.Split(string(psOut), "\n") {
		if strings.TrimSpace(line) == "postgres" {
			return true
		}
	}
	t.Logf("compose postgres is not running\n%s", psOut)
	return false
}

func findComposeDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("docker-compose.yml not found")
		}
		dir = parent
	}
}
