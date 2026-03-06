CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS rooms (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	description TEXT,
	is_private BOOLEAN DEFAULT 0,
	room_key TEXT,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS memberships (
	user_id TEXT NOT NULL,
	room_id TEXT NOT NULL,
	role TEXT NOT NULL, -- 'owner', 'member'
	joined_at TEXT NOT NULL,
	last_read_at TEXT,
	PRIMARY KEY (user_id, room_id),
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
	FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS messages (
	id TEXT PRIMARY KEY,
	room_id TEXT NOT NULL,
	author_id TEXT NOT NULL,
	content TEXT NOT NULL,
	created_at TEXT NOT NULL,
	FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
	FOREIGN KEY (author_id) REFERENCES users(id) ON DELETE CASCADE
);
