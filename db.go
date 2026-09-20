package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// schema is deterministic and idempotent: every statement uses IF NOT EXISTS,
// so running it against an existing database is a no-op.
const schema = `
CREATE TABLE IF NOT EXISTS projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'todo'
                CHECK (status IN ('todo','in_progress','done')),
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status  ON tasks(status);
`

func openDB(path string) (*sql.DB, error) {
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create database directory: %w", err)
			}
		}
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows a single writer; serialise access from the pool.
	db.SetMaxOpenConns(1)
	return db, nil
}

func initSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

type seedTask struct {
	title, description, status string
}

type seedProject struct {
	name, description string
	tasks            []seedTask
}

// seed inserts a small set of demo data, but only when the database has no
// projects yet. The procedure is therefore deterministic and idempotent:
// calling it repeatedly never duplicates rows.
func seed(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&count); err != nil {
		return 0, fmt.Errorf("count projects: %w", err)
	}
	if count > 0 {
		return 0, nil
	}

	projects := []seedProject{
		{
			name:        "Website Redesign",
			description: "Refresh the public marketing site and documentation.",
			tasks: []seedTask{
				{"Audit current pages", "Inventory pages and content that need updating.", "todo"},
				{"Design new layout", "Draft the new page structure and visual style.", "in_progress"},
				{"Migrate content", "Move approved copy into the new templates.", "todo"},
			},
		},
		{
			name:        "Mobile App",
			description: "Ship the first public version of the mobile app.",
			tasks: []seedTask{
				{"Set up CI pipeline", "Wire lint, tests and builds for iOS and Android.", "done"},
				{"Implement login flow", "OAuth against the existing account service.", "todo"},
			},
		},
	}

	now := time.Now().UTC().Format(time.RFC3339)
	inserted := 0
	for _, p := range projects {
		res, err := db.ExecContext(ctx,
			"INSERT INTO projects (name, description, created_at) VALUES (?, ?, ?)",
			p.name, p.description, now)
		if err != nil {
			return inserted, fmt.Errorf("seed project %q: %w", p.name, err)
		}
		projectID, err := res.LastInsertId()
		if err != nil {
			return inserted, err
		}
		for _, t := range p.tasks {
			if _, err := db.ExecContext(ctx,
				"INSERT INTO tasks (project_id, title, description, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
				projectID, t.title, t.description, t.status, now, now); err != nil {
				return inserted, fmt.Errorf("seed task %q: %w", t.title, err)
			}
		}
		inserted++
	}
	return inserted, nil
}