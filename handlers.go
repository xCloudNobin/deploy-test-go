package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type pageData struct {
	Title    string
	Projects []Project
	Tasks    []Task
	Project  *Project
	Task     *Task
	Q        string
	Status   string
	Msg      string
	Err      string
	Marker   string
	Statuses []string
}

func (a *app) render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data.Statuses = []string{"todo", "in_progress", "done"}
	if data.Marker == "" {
		data.Marker = a.marker()
	}
	if err := a.tmpl.execute(w, name, data); err != nil {
		log.Printf("template render %s: %v", name, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode json response: %v", err)
	}
}

const maxRequestBody = 1 << 20 // 1 MiB

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request body"})
		return false
	}
	return true
}

func parsePathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

func (a *app) renderNotfoundPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = a.tmpl.execute(w, "notfound", pageData{Title: "Not found", Marker: a.marker()})
}

func filterParams(r *http.Request) (q, status string) {
	q = strings.TrimSpace(r.URL.Query().Get("q"))
	status = cleanStatus(r.URL.Query().Get("status"))
	return q, status
}

// ---------------------------------- pages ----------------------------------

func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	q, status := filterParams(r)

	projects, err := a.listProjects(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		log.Printf("list projects: %v", err)
		return
	}

	tasksByProject := make(map[int64][]Task, len(projects))
	for _, p := range projects {
		tasks, err := a.listTasks(r.Context(), p.ID, status, q)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			log.Printf("list tasks: %v", err)
			return
		}
		tasksByProject[p.ID] = tasks
	}
	for i := range projects {
		projects[i].Tasks = tasksByProject[projects[i].ID]
	}

	a.render(w, "index", pageData{
		Title:    "Taskboard",
		Projects: projects,
		Q:        q,
		Status:   status,
		Msg:      r.URL.Query().Get("msg"),
		Err:      r.URL.Query().Get("err"),
	})
}

func (a *app) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		a.renderNotfoundPage(w)
		return
	}
	project, err := a.getProject(r.Context(), id)
	if errors.Is(err, errProjectNotFound) {
		a.renderNotfoundPage(w)
		return
	}
	if err != nil {
		log.Printf("get project: %v", err)
		a.renderNotfoundPage(w)
		return
	}

	q, status := filterParams(r)
	tasks, err := a.listTasks(r.Context(), id, status, q)
	if err != nil {
		log.Printf("list tasks: %v", err)
		a.renderNotfoundPage(w)
		return
	}
	project.Tasks = tasks

	a.render(w, "project_detail", pageData{
		Title:    project.Name,
		Project:  &project,
		Tasks:    tasks,
		Q:        q,
		Status:   status,
		Msg:      r.URL.Query().Get("msg"),
		Err:      r.URL.Query().Get("err"),
	})
}

func (a *app) handleProjectNewForm(w http.ResponseWriter, r *http.Request) {
	a.render(w, "project_form", pageData{Title: "New project"})
}

func (a *app) handleProjectEditForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		a.renderNotfoundPage(w)
		return
	}
	project, err := a.getProject(r.Context(), id)
	if errors.Is(err, errProjectNotFound) {
		a.renderNotfoundPage(w)
		return
	}
	if err != nil {
		log.Printf("get project: %v", err)
		a.renderNotfoundPage(w)
		return
	}
	a.render(w, "project_form", pageData{Title: "Edit project", Project: &project})
}

func (a *app) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/?err="+urlQueryEscape("invalid form data"), http.StatusSeeOther)
		return
	}
	name, err := validateProjectName(r.FormValue("name"))
	if err != nil {
		http.Redirect(w, r, "/projects/new?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	description, err := validateDescription(r.FormValue("description"))
	if err != nil {
		http.Redirect(w, r, "/projects/new?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	project, err := a.createProject(r.Context(), name, description)
	if err != nil {
		log.Printf("create project: %v", err)
		http.Redirect(w, r, "/projects/new?err="+urlQueryEscape("could not create project"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(project.ID, 10)+"?msg="+urlQueryEscape("Project created"), http.StatusSeeOther)
}

func (a *app) handleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		a.renderNotfoundPage(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/projects/"+strconv.FormatInt(id, 10)+"?err="+urlQueryEscape("invalid form data"), http.StatusSeeOther)
		return
	}
	name, err := validateProjectName(r.FormValue("name"))
	if err != nil {
		http.Redirect(w, r, "/projects/"+strconv.FormatInt(id, 10)+"/edit?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	description, err := validateDescription(r.FormValue("description"))
	if err != nil {
		http.Redirect(w, r, "/projects/"+strconv.FormatInt(id, 10)+"/edit?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	_, err = a.updateProject(r.Context(), id, name, description)
	if errors.Is(err, errProjectNotFound) {
		a.renderNotfoundPage(w)
		return
	}
	if err != nil {
		log.Printf("update project: %v", err)
		http.Redirect(w, r, "/projects/"+strconv.FormatInt(id, 10)+"/edit?err="+urlQueryEscape("could not update project"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(id, 10)+"?msg="+urlQueryEscape("Project updated"), http.StatusSeeOther)
}

func (a *app) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		a.renderNotfoundPage(w)
		return
	}
	if err := a.deleteProject(r.Context(), id); err != nil {
		if errors.Is(err, errProjectNotFound) {
			a.renderNotfoundPage(w)
			return
		}
		log.Printf("delete project: %v", err)
		http.Redirect(w, r, "/?err="+urlQueryEscape("could not delete project"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?msg="+urlQueryEscape("Project deleted"), http.StatusSeeOther)
}

func (a *app) loadProjectsForSelect(ctx context.Context) []Project {
	projects, err := a.listProjects(ctx)
	if err != nil {
		log.Printf("list projects for select: %v", err)
		return []Project{}
	}
	return projects
}

func (a *app) handleTaskNewForm(w http.ResponseWriter, r *http.Request) {
	data := pageData{Title: "New task", Projects: a.loadProjectsForSelect(r.Context())}
	if raw := r.URL.Query().Get("project_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || !projectExists(data.Projects, id) {
			http.Redirect(w, r, "/tasks/new?err="+urlQueryEscape("unknown project"), http.StatusSeeOther)
			return
		}
		data.Task = &Task{ProjectID: id}
	}
	a.render(w, "task_form", data)
}

func projectExists(projects []Project, id int64) bool {
	for _, p := range projects {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (a *app) handleTaskEditForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		a.renderNotfoundPage(w)
		return
	}
	task, err := a.getTask(r.Context(), id)
	if errors.Is(err, errTaskNotFound) {
		a.renderNotfoundPage(w)
		return
	}
	if err != nil {
		log.Printf("get task: %v", err)
		a.renderNotfoundPage(w)
		return
	}
	a.render(w, "task_form", pageData{
		Title:    "Edit task",
		Task:     &task,
		Projects: a.loadProjectsForSelect(r.Context()),
	})
}

func (a *app) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/tasks/new?err="+urlQueryEscape("invalid form data"), http.StatusSeeOther)
		return
	}
	projectID, err := strconv.ParseInt(r.FormValue("project_id"), 10, 64)
	if err != nil || projectID < 1 {
		http.Redirect(w, r, "/tasks/new?err="+urlQueryEscape("choose a project"), http.StatusSeeOther)
		return
	}
	title, err := validateTaskTitle(r.FormValue("title"))
	if err != nil {
		http.Redirect(w, r, "/tasks/new?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	description, err := validateDescription(r.FormValue("description"))
	if err != nil {
		http.Redirect(w, r, "/tasks/new?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	status, err := validateStatus(r.FormValue("status"))
	if err != nil {
		status = "todo"
	}

	task, err := a.createTask(r.Context(), projectID, title, description, status)
	if errors.Is(err, errProjectNotFound) {
		http.Redirect(w, r, "/tasks/new?err="+urlQueryEscape("unknown project"), http.StatusSeeOther)
		return
	}
	if err != nil {
		log.Printf("create task: %v", err)
		http.Redirect(w, r, "/tasks/new?err="+urlQueryEscape("could not create task"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(task.ProjectID, 10)+"?msg="+urlQueryEscape("Task created"), http.StatusSeeOther)
}

func (a *app) handleTaskUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		a.renderNotfoundPage(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/tasks/"+strconv.FormatInt(id, 10)+"/edit?err="+urlQueryEscape("invalid form data"), http.StatusSeeOther)
		return
	}
	title, err := validateTaskTitle(r.FormValue("title"))
	if err != nil {
		http.Redirect(w, r, "/tasks/"+strconv.FormatInt(id, 10)+"/edit?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	description, err := validateDescription(r.FormValue("description"))
	if err != nil {
		http.Redirect(w, r, "/tasks/"+strconv.FormatInt(id, 10)+"/edit?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	status, err := validateStatus(r.FormValue("status"))
	if err != nil {
		status = "todo"
	}

	task, err := a.updateTask(r.Context(), id, title, description, status)
	if errors.Is(err, errTaskNotFound) {
		a.renderNotfoundPage(w)
		return
	}
	if err != nil {
		log.Printf("update task: %v", err)
		http.Redirect(w, r, "/tasks/"+strconv.FormatInt(id, 10)+"/edit?err="+urlQueryEscape("could not update task"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(task.ProjectID, 10)+"?msg="+urlQueryEscape("Task updated"), http.StatusSeeOther)
}

func (a *app) handleTaskStatusForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		a.renderNotfoundPage(w)
		return
	}
	back := "/"
	if ref := r.Header.Get("Referer"); ref != "" {
		back = ref
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	status, err := validateStatus(r.FormValue("status"))
	if err != nil {
		http.Redirect(w, r, back+"?err="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	task, err := a.setTaskStatus(r.Context(), id, status)
	if errors.Is(err, errTaskNotFound) {
		a.renderNotfoundPage(w)
		return
	}
	if err != nil {
		log.Printf("set task status: %v", err)
		http.Redirect(w, r, back+"?err="+urlQueryEscape("could not update task"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(task.ProjectID, 10)+"?msg="+urlQueryEscape("Task status updated"), http.StatusSeeOther)
}

func (a *app) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		a.renderNotfoundPage(w)
		return
	}
	task, err := a.getTask(r.Context(), id)
	if err == nil {
		if derr := a.deleteTask(r.Context(), id); derr != nil {
			log.Printf("delete task: %v", derr)
			http.Redirect(w, r, "/?err="+urlQueryEscape("could not delete task"), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/projects/"+strconv.FormatInt(task.ProjectID, 10)+"?msg="+urlQueryEscape("Task deleted"), http.StatusSeeOther)
		return
	}
	if errors.Is(err, errTaskNotFound) {
		a.renderNotfoundPage(w)
		return
	}
	log.Printf("get task for delete: %v", err)
	a.renderNotfoundPage(w)
}

// -------------------------------- health API -------------------------------

func (a *app) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"app":     "deploy-test-go",
		"version": a.version,
		"commit":  a.commit,
	})
}

func (a *app) handleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := a.db.PingContext(ctx); err != nil {
		log.Printf("readiness: database unreachable: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error",
			"checks": map[string]string{"database": "unavailable"},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"checks": map[string]string{"database": "ok"},
	})
}

// --------------------------------- JSON API --------------------------------

type projectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type taskInput struct {
	ProjectID   int64  `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type projectPatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

type taskPatch struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
}

func (a *app) apiListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := a.listProjects(r.Context())
	if err != nil {
		log.Printf("api list projects: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (a *app) apiCreateProject(w http.ResponseWriter, r *http.Request) {
	var input projectInput
	if !decodeJSON(w, r, &input) {
		return
	}
	name, err := validateProjectName(input.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	description, err := validateDescription(input.Description)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	project, err := a.createProject(r.Context(), name, description)
	if err != nil {
		log.Printf("api create project: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (a *app) apiGetProject(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	project, err := a.getProject(r.Context(), id)
	if errors.Is(err, errProjectNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if err != nil {
		log.Printf("api get project: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	q, status := filterParams(r)
	tasks, err := a.listTasks(r.Context(), id, status, q)
	if err != nil {
		log.Printf("api list tasks: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	project.Tasks = tasks
	writeJSON(w, http.StatusOK, project)
}

func (a *app) apiUpdateProject(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var patch projectPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	current, err := a.getProject(r.Context(), id)
	if errors.Is(err, errProjectNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if err != nil {
		log.Printf("api get project: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	name := current.Name
	if patch.Name != nil {
		name, err = validateProjectName(*patch.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	description := current.Description
	if patch.Description != nil {
		description, err = validateDescription(*patch.Description)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	project, err := a.updateProject(r.Context(), id, name, description)
	if err != nil {
		log.Printf("api update project: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (a *app) apiDeleteProject(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := a.deleteProject(r.Context(), id); err != nil {
		if errors.Is(err, errProjectNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		log.Printf("api delete project: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) apiListTasks(w http.ResponseWriter, r *http.Request) {
	var projectID int64
	if raw := r.URL.Query().Get("project_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid project_id"})
			return
		}
		projectID = id
	}
	q, status := filterParams(r)
	tasks, err := a.listTasks(r.Context(), projectID, status, q)
	if err != nil {
		log.Printf("api list tasks: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (a *app) apiCreateTask(w http.ResponseWriter, r *http.Request) {
	var input taskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	title, err := validateTaskTitle(input.Title)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	description, err := validateDescription(input.Description)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	status, err := validateStatus(input.Status)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	task, err := a.createTask(r.Context(), input.ProjectID, title, description, status)
	if errors.Is(err, errProjectNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown project_id"})
		return
	}
	if err != nil {
		log.Printf("api create task: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (a *app) apiUpdateTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var patch taskPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	current, err := a.getTask(r.Context(), id)
	if errors.Is(err, errTaskNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	if err != nil {
		log.Printf("api get task: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	title := current.Title
	if patch.Title != nil {
		title, err = validateTaskTitle(*patch.Title)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	description := current.Description
	if patch.Description != nil {
		description, err = validateDescription(*patch.Description)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	status := current.Status
	if patch.Status != nil {
		status, err = validateStatus(*patch.Status)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	task, err := a.updateTask(r.Context(), id, title, description, status)
	if err != nil {
		log.Printf("api update task: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (a *app) apiDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := a.deleteTask(r.Context(), id); err != nil {
		if errors.Is(err, errTaskNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
			return
		}
		log.Printf("api delete task: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// dbGuard is a tiny helper so the sql.DB nil check reads clearly in tests.
func pingDB(db *sql.DB, ctx context.Context) error { return db.PingContext(ctx) }

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}