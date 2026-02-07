# Animasola

Terminal-first community for developers, accessed entirely over SSH.

## Dev Quickstart

Prereqs:
- Go 1.22+ (or newer)

Run:
```bash
go mod tidy
go run ./cmd/animasola --config ./config.example.yaml
```

Then connect from another terminal:
```bash
ssh -p 23234 localhost
```

Register (dev mode uses a second port):
```bash
ssh -p 23235 yourname@localhost
```

## Repo Layout

- `cmd/animasola`: entry point
- `internal/server`: SSH server setup (Wish)
- `internal/tui`: Bubble Tea app models
- `internal/store`: SQLite + queries + migrations
- `internal/auth`: SSH key identity and registration helpers
- `internal/pubsub`: in-memory broker for realtime updates
- `migrations`: SQL schema
