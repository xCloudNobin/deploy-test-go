package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type Project struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	TaskCount   int    `json:"task_count"`
	Tasks       []Task `json:"tasks,omitempty"`
}

type Task struct {
	ID          int64  `json:"id"`
	ProjectID   int64  `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

const taskColumns = "id, project_id, title, description, status, created_at, updated_at"

func scanTask(scanner interface{ Scan(...any) error }) (Task, error) {
	var t Task
	err := scanner.Scan(&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (a *app) listProjects(ctx context.Context) ([]Project, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT p.id, p.name, p.description, p.created_at,
		       (SELECT COUNT(*) FROM tasks t WHERE t.project_id = p.id) AS task_count
		FROM projects p
		ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.TaskCount); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (a *app) getProject(ctx context.Context, id int64) (Project, error) {
	var p Project
	err := a.db.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.description, p.created_at,
		       (SELECT COUNT(*) FROM tasks t WHERE t.project_id = p.id) AS task_count
		FROM projects p WHERE p.id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.TaskCount)
	if err == sql.ErrNoRows {
		return Project{}, errProjectNotFound
	}
	return p, err
}

func (a *app) createProject(ctx context.Context, name, description string) (Project, error) {
	res, err := a.db.ExecContext(ctx,
		"INSERT INTO projects (name, description, created_at) VALUES (?, ?, datetime('now'))",
		name, description)
	if err != nil {
		return Project{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Project{}, err
	}
	return a.getProject(ctx, id)
}

func (a *app) updateProject(ctx context.Context, id int64, name, description string) (Project, error) {
	res, err := a.db.ExecContext(ctx,
		"UPDATE projects SET name = ?, description = ? WHERE id = ?",
		name, description, id)
	if err != nil {
		return Project{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Project{}, errProjectNotFound
	}
	return a.getProject(ctx, id)
}

func (a *app) deleteProject(ctx context.Context, id int64) error {
	res, err := a.db.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errProjectNotFound
	}
	return nil
}

// listTasks returns tasks, optionally filtered by project, status and a
// free-text search term. Everything is parameterised; wildcard characters in
// the search term are escaped so it cannot be used for pattern injection.
func (a *app) listTasks(ctx context.Context, projectID int64, status, q string) ([]Task, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if projectID > 0 {
		where = append(where, "project_id = ?")
		args = append(args, projectID)
	}
	if status != "" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if q != "" {
		where = append(where, "(title LIKE ? ESCAPE '\\' OR description LIKE ? ESCAPE '\\')")
		pattern := "%" + likeEscape(q) + "%"
		args = append(args, pattern, pattern)
	}

	query := fmt.Sprintf(
		"SELECT %s FROM tasks WHERE %s ORDER BY CASE status WHEN 'todo' THEN 0 WHEN 'in_progress' THEN 1 ELSE 2 END, id",
		taskColumns, strings.Join(where, " AND "))

	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (a *app) getTask(ctx context.Context, id int64) (Task, error) {
	row := a.db.QueryRowContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE id = ?", id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return Task{}, errTaskNotFound
	}
	return t, err
}

func (a *app) createTask(ctx context.Context, projectID int64, title, description, status string) (Task, error) {
	var exists int
	if err := a.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM projects WHERE id = ?", projectID).Scan(&exists); err != nil {
		return Task{}, err
	}
	if exists == 0 {
		return Task{}, errProjectNotFound
	}
	res, err := a.db.ExecContext(ctx,
		"INSERT INTO tasks (project_id, title, description, status, created_at, updated_at) VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))",
		projectID, title, description, status)
	if err != nil {
		return Task{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Task{}, err
	}
	return a.getTask(ctx, id)
}

func (a *app) updateTask(ctx context.Context, id int64, title, description, status string) (Task, error) {
	res, err := a.db.ExecContext(ctx,
		"UPDATE tasks SET title = ?, description = ?, status = ?, updated_at = datetime('now') WHERE id = ?",
		title, description, status, id)
	if err != nil {
		return Task{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Task{}, errTaskNotFound
	}
	return a.getTask(ctx, id)
}

func (a *app) setTaskStatus(ctx context.Context, id int64, status string) (Task, error) {
	res, err := a.db.ExecContext(ctx,
		"UPDATE tasks SET status = ?, updated_at = datetime('now') WHERE id = ?",
		status, id)
	if err != nil {
		return Task{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Task{}, errTaskNotFound
	}
	return a.getTask(ctx, id)
}

func (a *app) deleteTask(ctx context.Context, id int64) error {
	res, err := a.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errTaskNotFound
	}
	return nil
}

var (
	errProjectNotFound = fmt.Errorf("project not found")
	errTaskNotFound    = fmt.Errorf("task not found")
)