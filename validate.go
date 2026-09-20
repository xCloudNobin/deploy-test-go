package main

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var validStatuses = map[string]bool{
	"todo":        true,
	"in_progress": true,
	"done":        true,
}

const (
	maxNameLen  = 120
	maxTitleLen = 200
	maxDescLen  = 5000
)

func validateProjectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("project name is required")
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return "", errors.New("project name must be at most 120 characters")
	}
	return name, nil
}

func validateTaskTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", errors.New("task title is required")
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		return "", errors.New("task title must be at most 200 characters")
	}
	return title, nil
}

func validateDescription(description string) (string, error) {
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > maxDescLen {
		return "", errors.New("description must be at most 5000 characters")
	}
	return description, nil
}

func validateStatus(status string) (string, error) {
	if !validStatuses[status] {
		return "", errors.New("status must be one of: todo, in_progress, done")
	}
	return status, nil
}

func cleanStatus(query string) string {
	query = strings.TrimSpace(query)
	if !validStatuses[query] {
		return ""
	}
	return query
}