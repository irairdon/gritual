package db

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"
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

	pool := TestPool(t)

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
