# Architecture

Animasola is a single Go binary:

- Wish: SSH server + public key identity
- Bubble Tea: server-rendered TUI per SSH session
- SQLite: persistence
- In-memory pub/sub: realtime fanout to connected sessions

v1 keeps the data model intentionally small: everything is a `message` with optional `parent_id`.

