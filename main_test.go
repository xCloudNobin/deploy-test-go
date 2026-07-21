package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHomeIdentifiesDeployment(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "Hello from deploy-test-go") {
		t.Fatalf("home page did not identify the application")
	}
}

func TestHealthReportsOkay(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	Handler().ServeHTTP(response, request)

	var payload map[string]string
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || payload["status"] != "ok" || payload["app"] != "deploy-test-go" {
		t.Fatalf("unexpected health response: status=%d payload=%v", response.Code, payload)
	}
}
