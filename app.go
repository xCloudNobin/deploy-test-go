package main

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
)

//go:embed templates/shared/*.html templates/pages/*.html static/*
var embeddedFiles embed.FS

var staticFS = mustSubFS(embeddedFiles, "static")

// templateFuncMap holds shared helpers used by every page.
var templateFuncMap = template.FuncMap{
	"statusLabel": func(s string) string {
		switch s {
		case "todo":
			return "To do"
		case "in_progress":
			return "In progress"
		case "done":
			return "Done"
		}
		return s
	},
	"statusClass": func(s string) string {
		if s == "" {
			s = "todo"
		}
		return "badge-" + s
	},
	"statuses": func() []string {
		return []string{"todo", "in_progress", "done"}
	},
}

// templateCatalog keeps one template set per page. Parsing every page into a
// shared set would let later {{define "content"}} blocks overwrite earlier
// ones, so each page gets its own clone of the shared layout. The clone is
// seeded with the base shell + partial templates, then a single page file
// overrides the "content" block.
type templateCatalog struct {
	pages map[string]*template.Template
}

var sharedPages = []string{"index", "project_detail", "project_form", "task_form", "notfound"}

func parseTemplates() *templateCatalog {
	shared := template.New("").Funcs(templateFuncMap)
	shared = template.Must(shared.ParseFS(embeddedFiles, "templates/shared/*.html"))

	catalog := &templateCatalog{pages: make(map[string]*template.Template, len(sharedPages))}
	for _, name := range sharedPages {
		clone, err := shared.Clone()
		if err != nil {
			log.Fatalf("clone templates for %s: %v", name, err)
		}
		clone = template.Must(clone.ParseFS(embeddedFiles, "templates/pages/"+name+".html"))
		catalog.pages[name] = clone
	}
	return catalog
}

var tmpl = parseTemplates()

// execute renders the shared shell (the "base" template) for a page. The page
// name selects the clone whose "content" block holds that page's body.
func (c *templateCatalog) execute(w io.Writer, name string, data any) error {
	t, ok := c.pages[name]
	if !ok {
		return fmt.Errorf("unknown template page %q", name)
	}
	return t.ExecuteTemplate(w, "base", data)
}

func mustSubFS(src fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(src, dir)
	if err != nil {
		log.Fatalf("embed %s: %v", dir, err)
	}
	return sub
}

type app struct {
	db        *sql.DB
	tmpl      *templateCatalog
	version   string
	commit    string
	buildTime string
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /readiness", a.handleReadiness)

	// Project pages
	mux.HandleFunc("GET /projects/new", a.handleProjectNewForm)
	mux.HandleFunc("POST /projects", a.handleProjectCreate)
	mux.HandleFunc("GET /projects/{id}", a.handleProjectDetail)
	mux.HandleFunc("GET /projects/{id}/edit", a.handleProjectEditForm)
	mux.HandleFunc("POST /projects/{id}/edit", a.handleProjectUpdate)
	mux.HandleFunc("POST /projects/{id}/delete", a.handleProjectDelete)

	// Task pages
	mux.HandleFunc("GET /tasks/new", a.handleTaskNewForm)
	mux.HandleFunc("POST /tasks", a.handleTaskCreate)
	mux.HandleFunc("GET /tasks/{id}/edit", a.handleTaskEditForm)
	mux.HandleFunc("POST /tasks/{id}/edit", a.handleTaskUpdate)
	mux.HandleFunc("POST /tasks/{id}/status", a.handleTaskStatusForm)
	mux.HandleFunc("POST /tasks/{id}/delete", a.handleTaskDelete)

	// JSON API
	mux.HandleFunc("GET /api/projects", a.apiListProjects)
	mux.HandleFunc("POST /api/projects", a.apiCreateProject)
	mux.HandleFunc("GET /api/projects/{id}", a.apiGetProject)
	mux.HandleFunc("PATCH /api/projects/{id}", a.apiUpdateProject)
	mux.HandleFunc("DELETE /api/projects/{id}", a.apiDeleteProject)
	mux.HandleFunc("GET /api/tasks", a.apiListTasks)
	mux.HandleFunc("POST /api/tasks", a.apiCreateTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", a.apiUpdateTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", a.apiDeleteTask)

	// JSON 404 for unknown /api routes
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})

	return mux
}

func (a *app) marker() string {
	return a.version + " (" + a.commit + " built " + a.buildTime + ")"
}