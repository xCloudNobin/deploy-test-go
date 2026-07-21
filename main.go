package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

func Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(response, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Go Deployment Test</title></head><body style="font-family:system-ui;max-width:720px;margin:5rem auto;padding:1rem"><h1>Hello from deploy-test-go</h1><p>Your Go deployment is working.</p><p><a href="/health">Health check</a></p></body></html>`)
	})

	mux.HandleFunc("GET /health", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		json.NewEncoder(response).Encode(map[string]string{
			"status": "ok",
			"app":    "deploy-test-go",
		})
	})

	return mux
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	address := ":" + port
	log.Printf("deploy-test-go listening on %s", address)
	if err := http.ListenAndServe(address, Handler()); err != nil {
		log.Fatal(err)
	}
}
