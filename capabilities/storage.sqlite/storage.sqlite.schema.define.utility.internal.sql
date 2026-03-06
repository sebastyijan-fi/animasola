CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS rooms (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT,
	creator_id TEXT,
	signature TEXT,
	is_private BOOLEAN DEFAULT 0,
	room_key TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT,
	last_seen_at TEXT,
	version INTEGER DEFAULT 1,
	announce_count INTEGER DEFAULT 0
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

CREATE TABLE IF NOT EXISTS public_room_index (
	room_id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT,
	creator_id TEXT,
	signature TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT,
	last_seen_at TEXT,
	version INTEGER DEFAULT 1,
	announce_count INTEGER DEFAULT 0
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

CREATE INDEX IF NOT EXISTS idx_messages_room_created_at ON messages(room_id, created_at);
CREATE INDEX IF NOT EXISTS idx_memberships_user_room ON memberships(user_id, room_id);
CREATE INDEX IF NOT EXISTS idx_memberships_room_user ON memberships(room_id, user_id);
CREATE INDEX IF NOT EXISTS idx_rooms_public_last_seen ON rooms(is_private, last_seen_at, name);
CREATE INDEX IF NOT EXISTS idx_public_room_index_last_seen ON public_room_index(last_seen_at, name);
