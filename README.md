# deploy-test-go

Minimal Go HTTP application for testing Git deployments. It uses only the standard library.

## Routes

- `/` — HTML home page
- `/health` — JSON health check

## Build and run

```bash
go test ./...
go build -o bin/server .
PORT=8080 ./bin/server
```

The server binds to all interfaces and reads the `PORT` environment variable.
