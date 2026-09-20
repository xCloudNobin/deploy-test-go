package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// Build marker metadata. Injected at build time via -ldflags so every binary
// can be traced back to a source revision (see the Makefile).
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

type config struct {
	addr   string
	dbPath string
}

func loadConfig() config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}
	dbPath := os.Getenv("SQLITE_PATH")
	if dbPath == "" {
		dbPath = os.Getenv("DB_PATH")
	}
	if dbPath == "" {
		dbPath = filepath.Join("data", "board.db")
	}
	if dbPath == ":memory:" {
		// In-memory mode is supported for tests only; production should use
		// a persistent path outside the release checkout.
	} else if abs, err := filepath.Abs(dbPath); err == nil {
		dbPath = abs
	}
	return config{
		addr:   net.JoinHostPort(host, port),
		dbPath: dbPath,
	}
}

func main() {
	cfg := loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := openDB(cfg.dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	if err := initSchema(ctx, db); err != nil {
		log.Fatalf("initialize schema: %v", err)
	}
	seeded, err := seed(ctx, db)
	if err != nil {
		log.Fatalf("seed data: %v", err)
	}
	if seeded > 0 {
		log.Printf("seeded %d sample projects", seeded)
	}

	app := &app{
		db:        db,
		tmpl:      tmpl,
		version:   version,
		commit:    commit,
		buildTime: buildTime,
	}

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("taskboard %s (commit=%s built=%s) listening on %s; database=%s",
			version, commit, buildTime, cfg.addr, cfg.dbPath)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	case <-ctx.Done():
		log.Printf("received shutdown signal, draining connections")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown error: %v", err)
		}
	}
	log.Printf("shutdown complete")
}