# Deployment (v1)

v1 is a single binary with a single SQLite file.

Inputs:
- YAML config (SSH bind address/port, host key path, DB path)

Recommended:
- Run under systemd
- Persist `host_key` and `animasola.db`

