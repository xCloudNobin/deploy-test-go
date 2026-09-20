package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// newTestServer builds a fully wired app (real SQLite:memory: database,
// schema + seed) and returns its HTTP handler plus the app for inspection.
func newTestServer(t *testing.T) (http.Handler, *app) {
	t.Helper()
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := seed(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := &app{
		db:        db,
		tmpl:      tmpl,
		version:   "test",
		commit:    "deadbeef",
		buildTime: "now",
	}
	return a.routes(), a
}

func doJSON(t *testing.T, h http.Handler, method, url string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, url, reader)
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

// --------------------------------- health / readiness -------------------------------

func TestHealthReportsOkay(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doJSON(t, h, http.MethodGet, "/health", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	payload := decodeBody[map[string]string](t, rec)
	if payload["status"] != "ok" {
		t.Fatalf("unexpected health: %v", payload)
	}
	if payload["app"] != "deploy-test-go" {
		t.Fatalf("unexpected app name: %v", payload)
	}
	if payload["version"] != "test" || payload["commit"] != "deadbeef" {
		t.Fatalf("build marker missing: %v", payload)
	}
}

func TestReadinessReportsDependencies(t *testing.T) {
	h, a := newTestServer(t)

	rec := doJSON(t, h, http.MethodGet, "/readiness", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected readiness 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	payload := decodeBody[map[string]any](t, rec)
	if payload["status"] != "ok" {
		t.Fatalf("unexpected readiness: %v", payload)
	}

	// Simulate dependency failure: close the database, readiness must fail.
	if err := a.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	rec = doJSON(t, h, http.MethodGet, "/readiness", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after db close, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	payload = decodeBody[map[string]any](t, rec)
	if payload["status"] != "error" {
		t.Fatalf("expected error readiness: %v", payload)
	}
}

// --------------------------------- project CRUD ------------------------------------

func TestProjectCRUD(t *testing.T) {
	h, _ := newTestServer(t)

	// create
	created := doJSON(t, h, http.MethodPost, "/api/projects", map[string]string{
		"name": "Acme Launch", "description": "ship it",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	project := decodeBody[Project](t, created)
	if project.ID == 0 || project.Name != "Acme Launch" {
		t.Fatalf("unexpected created project: %+v", project)
	}
	id := fmt.Sprintf("%d", project.ID)

	// read
	got := doJSON(t, h, http.MethodGet, "/api/projects/"+id, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", got.Code)
	}
	if got := decodeBody[Project](t, got); got.Name != "Acme Launch" {
		t.Fatalf("unexpected project: %+v", got)
	}

	// update
	updated := doJSON(t, h, http.MethodPatch, "/api/projects/"+id, map[string]string{"name": "Acme Relaunch"})
	if updated.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", updated.Code, updated.Body.String())
	}
	if got := decodeBody[Project](t, updated); got.Name != "Acme Relaunch" {
		t.Fatalf("unexpected updated project: %+v", got)
	}

	// delete
	deleted := doJSON(t, h, http.MethodDelete, "/api/projects/"+id, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}

	// gone now
	gone := doJSON(t, h, http.MethodGet, "/api/projects/"+id, nil)
	if gone.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", gone.Code)
	}
}

func TestTaskCRUDAndProjectLink(t *testing.T) {
	h, _ := newTestServer(t)

	project := decodeBody[Project](t, doJSON(t, h, http.MethodPost, "/api/projects", map[string]string{
		"name": "Warehouse",
	}))
	pid := fmt.Sprintf("%d", project.ID)

	// create a task in that project
	created := doJSON(t, h, http.MethodPost, "/api/tasks", map[string]any{
		"project_id": project.ID, "title": "Stack boxes", "description": "row A", "status": "todo",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create task: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	task := decodeBody[Task](t, created)
	if task.ProjectID != project.ID || task.Status != "todo" {
		t.Fatalf("unexpected task: %+v", task)
	}
	tid := fmt.Sprintf("%d", task.ID)

	// status change
	moved := doJSON(t, h, http.MethodPatch, "/api/tasks/"+tid, map[string]string{"status": "done"})
	if moved.Code != http.StatusOK {
		t.Fatalf("status change: expected 200, got %d: %s", moved.Code, moved.Body.String())
	}
	if got := decodeBody[Task](t, moved); got.Status != "done" {
		t.Fatalf("unexpected status: %+v", got)
	}

	// project listing now includes the task
	p := decodeBody[Project](t, doJSON(t, h, http.MethodGet, "/api/projects/"+pid, nil))
	if len(p.Tasks) != 1 || p.Tasks[0].Title != "Stack boxes" {
		t.Fatalf("project tasks not linked: %+v", p.Tasks)
	}

	// deleting the project cascades to its task
	if rec := doJSON(t, h, http.MethodDelete, "/api/projects/"+pid, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete project: %d", rec.Code)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/tasks/"+tid, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expected cascaded task 404, got %d", rec.Code)
	}
}

// ------------------------------ search / filter ------------------------------------

func TestSearchAndStatusFilterEscapesWildcards(t *testing.T) {
	h, _ := newTestServer(t)

	// Wildcard characters in the search term must be treated literally, so
	// "%" or "_" cannot be used to broaden the LIKE pattern.
	literal := decodeBody[[]Task](t, doJSON(t, h, http.MethodGet, "/api/tasks?q=%25", nil))
	if len(literal) != 0 {
		t.Fatalf("literal '%%' search should match nothing, got %d", len(literal))
	}
	// Find the known seeded task by title.
	found := decodeBody[[]Task](t, doJSON(t, h, http.MethodGet, "/api/tasks?q=Audit", nil))
	if len(found) != 1 {
		t.Fatalf("expected 1 match for 'Audit', got %d", len(found))
	}

	done := decodeBody[[]Task](t, doJSON(t, h, http.MethodGet, "/api/tasks?status=done", nil))
	for _, task := range done {
		if task.Status != "done" {
			t.Fatalf("status filter leaked task %d (status=%s)", task.ID, task.Status)
		}
	}

	// Invalid status values are validated server-side and treated as "no
	// filter": the request must not fail and must return the unfiltered set.
	all := decodeBody[[]Task](t, doJSON(t, h, http.MethodGet, "/api/tasks", nil))
	bogus := decodeBody[[]Task](t, doJSON(t, h, http.MethodGet, "/api/tasks?status=banana", nil))
	if len(bogus) != len(all) {
		t.Fatalf("bogus status filter should be ignored, got %d tasks, expected %d", len(bogus), len(all))
	}
}

func TestListTasksValidatesProjectID(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doJSON(t, h, http.MethodGet, "/api/tasks?project_id=abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad project_id, got %d", rec.Code)
	}
}

// ---------------------------- invalid input handling ---------------------------------

func TestRejectsMalformedJSON(t *testing.T) {
	h, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := decodeBody[map[string]string](t, rec); err["error"] == "" {
		t.Fatalf("expected error message, got %+v", err)
	}
}

func TestRejectsUnknownFieldsAndOversizedBody(t *testing.T) {
	h, _ := newTestServer(t)

	// Unknown fields are rejected (strict decoding).
	unknown := doJSON(t, h, http.MethodPost, "/api/projects", map[string]string{
		"name": "Ok", "evil": "payload",
	})
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d", unknown.Code)
	}

	// Oversized bodies are cut off.
	big := strings.Repeat("a", 2_000_000)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"name":"`+big+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", rec.Code)
	}
}

func TestValidatesFieldLengthsAndEmptyNames(t *testing.T) {
	h, _ := newTestServer(t)

	empty := doJSON(t, h, http.MethodPost, "/api/projects", map[string]string{"name": "   "})
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for blank name, got %d: %s", empty.Code, empty.Body.String())
	}

	tooLong := doJSON(t, h, http.MethodPost, "/api/projects", map[string]string{"name": strings.Repeat("x", 121)})
	if tooLong.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for over-long name, got %d", tooLong.Code)
	}
}

func TestRejectsInvalidTaskStatus(t *testing.T) {
	h, _ := newTestServer(t)

	project := decodeBody[Project](t, doJSON(t, h, http.MethodPost, "/api/projects", map[string]string{"name": "P"}))
	invalid := doJSON(t, h, http.MethodPost, "/api/tasks", map[string]any{
		"project_id": project.ID, "title": "T", "status": "banana",
	})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status, got %d: %s", invalid.Code, invalid.Body.String())
	}

	// Update path rejects invalid status the same way.
	task := decodeBody[Task](t, doJSON(t, h, http.MethodPost, "/api/tasks", map[string]any{
		"project_id": project.ID, "title": "T", "status": "todo",
	}))
	bad := doJSON(t, h, http.MethodPatch, fmt.Sprintf("/api/tasks/%d", task.ID), map[string]string{"status": "nope"})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status update, got %d: %s", bad.Code, bad.Body.String())
	}
}

func TestNotfoundResponses(t *testing.T) {
	h, _ := newTestServer(t)

	cases := []struct{ method, url, body string }{
		{http.MethodGet, "/api/projects/999999", ""},
		{http.MethodPatch, "/api/projects/999999", `{}`},
		{http.MethodDelete, "/api/projects/999999", ""},
		{http.MethodGet, "/api/tasks/999999", ""},
		{http.MethodGet, "/api/nope", ""},
		{http.MethodGet, "/projects/999999", ""},
		{http.MethodGet, "/tasks/999999/edit", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.url, strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: expected 404, got %d (body=%s)", c.method, c.url, rec.Code, rec.Body.String())
		}
	}
}

// --------------------------------- HTML escaping ------------------------------------

func TestHTMLPageEscapesUserContent(t *testing.T) {
	h, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader([]byte(
		`{"name":"<script>alert(1)</script>","description":"<b>bold</b>"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}

	page := httptest.NewRequest(http.MethodGet, "/", nil)
	prec := httptest.NewRecorder()
	h.ServeHTTP(prec, page)

	body := prec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("page emitted raw <script> payload; expected escaping")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("expected HTML-escaped project name on the page")
	}
}

// ------------------------------- schema / seed --------------------------------------

func TestSchemaIdempotentAndSeedDeterministic(t *testing.T) {
	ctx := context.Background()

	db, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("first schema: %v", err)
	}
	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("schema is not idempotent: %v", err)
	}

	first, err := seed(ctx, db)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if first == 0 {
		t.Fatal("expected seed data on empty database")
	}
	second, err := seed(ctx, db)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if second != 0 {
		t.Fatalf("seed is not idempotent, inserted %d rows again", second)
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != first {
		t.Fatalf("expected %d projects, got %d", first, count)
	}
}

// ------------------------------- persistence ------------------------------------------

// TestPersistenceAcrossRestart uses a real on-disk database file and verifies
// that records written by one server instance survive a full close/reopen,
// exactly like a production redeploy that points at the same SQLite path.
func TestPersistenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	dbPath := filepath.Join(dir, "board.db")
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := initSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := seed(ctx, db); err != nil {
		t.Fatal(err)
	}

	a := &app{db: db, tmpl: tmpl, version: "test"}
	h := a.routes()

	created := decodeBody[Project](t, doJSON(t, h, http.MethodPost, "/api/projects", map[string]string{
		"name": "Survive the redeploy",
	}))
	pid := created.ID

	// Simulate the process stopping completely.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen the same file, as the restarted process would.
	db2, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { db2.Close() })
	if err := initSchema(ctx, db2); err != nil {
		t.Fatal(err)
	}

	a2 := &app{db: db2, tmpl: tmpl, version: "test"}
	h2 := a2.routes()
	rec := doJSON(t, h2, http.MethodGet, fmt.Sprintf("/api/projects/%d", pid), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("record lost after restart: %d", rec.Code)
	}
	got := decodeBody[Project](t, rec)
	if got.Name != "Survive the redeploy" {
		t.Fatalf("persisted project changed: %+v", got)
	}
}

// --------------------------------- concurrency sanity ---------------------------------

func TestConcurrentWritesDoNotCorrupt(t *testing.T) {
	h, _ := newTestServer(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			rec := doJSON(t, h, http.MethodPost, "/api/projects", map[string]string{
				"name": fmt.Sprintf("Concurrent %d", n),
			})
			if rec.Code != http.StatusCreated {
				t.Errorf("concurrent create %d: %d", n, rec.Code)
			}
		}(i)
	}
	wg.Wait()

	rec := doJSON(t, h, http.MethodGet, "/api/projects", nil)
	projects := decodeBody[[]Project](t, rec)
	var found int
	for _, p := range projects {
		if strings.HasPrefix(p.Name, "Concurrent ") {
			found++
		}
	}
	if found != 8 {
		t.Fatalf("expected 8 concurrent projects, found %d", found)
	}
}

// ------------------------------- env / config ----------------------------------------
func TestLoadConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("HOST", "")
	t.Setenv("SQLITE_PATH", "")
	t.Setenv("DB_PATH", "")

	cfg := loadConfig()
	if cfg.addr != "0.0.0.0:8080" {
		t.Fatalf("expected default addr 0.0.0.0:8080, got %q", cfg.addr)
	}
	if !strings.HasSuffix(cfg.dbPath, filepath.Join("data", "board.db")) {
		t.Fatalf("expected default relative db path, got %q", cfg.dbPath)
	}

	t.Setenv("PORT", "9099")
	t.Setenv("HOST", "127.0.0.1")
	t.Setenv("SQLITE_PATH", "/custom/path.db")
	cfg = loadConfig()
	if cfg.addr != "127.0.0.1:9099" {
		t.Fatalf("expected overridden addr, got %q", cfg.addr)
	}
	if cfg.dbPath != "/custom/path.db" {
		t.Fatalf("expected overridden db path, got %q", cfg.dbPath)
	}
}

func TestHTMLLayoutRendersIndex(t *testing.T) {
	h, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Taskboard", "Website Redesign", "Mobile App", "Search", "Status", "vtest"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index page missing %q", want)
		}
	}
}

func TestTaskFormRendersNewAndEdit(t *testing.T) {
	h, _ := newTestServer(t)

	// New task form with no preselect and with an explicit preselect.
	req := httptest.NewRequest(http.MethodGet, "/tasks/new", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("new task form: %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Select a project") {
		t.Fatal("new task form missing project select")
	}

	preselect := httptest.NewRequest(http.MethodGet, "/tasks/new?project_id=1", nil)
	prec := httptest.NewRecorder()
	h.ServeHTTP(prec, preselect)
	if prec.Code != http.StatusOK {
		t.Fatalf("preselected task form: %d", prec.Code)
	}
	if !strings.Contains(prec.Body.String(), `value="1" selected`) {
		t.Fatalf("project not preselected: %s", prec.Body.String())
	}

	// Edit form for seeded task id=1 must set its current status (todo).
	found := decodeBody[[]Task](t, doJSON(t, h, http.MethodGet, "/api/tasks?q=Audit", nil))
	if len(found) != 1 {
		t.Fatalf("expected seeded task, got %d", len(found))
	}
	edit := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/tasks/%d/edit", found[0].ID), nil)
	erec := httptest.NewRecorder()
	h.ServeHTTP(erec, edit)
	if erec.Code != http.StatusOK {
		t.Fatalf("edit task form: %d (body=%s)", erec.Code, erec.Body.String())
	}
	ebody := erec.Body.String()
	if !strings.Contains(ebody, `value="todo" selected`) {
		t.Fatalf("edit form did not mark current status selected: %s", ebody)
	}
	if strings.Contains(ebody, "template render") {
		t.Fatal("edit form rendered a template error")
	}
}