PRAGMA foreign_keys = ON;

-- Users
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,         -- ULID
    username TEXT NOT NULL,
    key_fingerprint TEXT NOT NULL,
    public_key TEXT NOT NULL,
    created_at TEXT NOT NULL      -- ISO 8601 UTC
);

CREATE UNIQUE INDEX IF NOT EXISTS users_username_nocase_uq ON users(username COLLATE NOCASE);
CREATE UNIQUE INDEX IF NOT EXISTS users_key_fingerprint_uq ON users(key_fingerprint);

-- Communities
CREATE TABLE IF NOT EXISTS communities (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    member_count INTEGER NOT NULL DEFAULT 0
);

-- Rooms
CREATE TABLE IF NOT EXISTS rooms (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    community_id TEXT NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    pinned_message_id TEXT REFERENCES messages(id),
    created_at TEXT NOT NULL,
    UNIQUE(name, community_id)
);

-- Messages
CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,         -- ULID
    room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    author_id TEXT NOT NULL REFERENCES users(id),
    content TEXT NOT NULL,
    parent_id TEXT REFERENCES messages(id),
    upvote_count INTEGER NOT NULL DEFAULT 0,
    reply_count INTEGER NOT NULL DEFAULT 0,
    is_deleted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_room_id ON messages(room_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_parent_id ON messages(parent_id);
CREATE INDEX IF NOT EXISTS idx_messages_author_id ON messages(author_id);

-- Full-text search (FTS5)
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content,
    content='messages',
    content_rowid='rowid'
);

-- Upvotes
CREATE TABLE IF NOT EXISTS upvotes (
    user_id TEXT NOT NULL REFERENCES users(id),
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, message_id)
);

-- Community memberships
CREATE TABLE IF NOT EXISTS memberships (
    user_id TEXT NOT NULL REFERENCES users(id),
    community_id TEXT NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'member',
    joined_at TEXT NOT NULL,
    PRIMARY KEY (user_id, community_id)
);

CREATE INDEX IF NOT EXISTS idx_memberships_user_id ON memberships(user_id);
CREATE INDEX IF NOT EXISTS idx_memberships_community_id ON memberships(community_id);

-- Read tracking
CREATE TABLE IF NOT EXISTS read_positions (
    user_id TEXT NOT NULL REFERENCES users(id),
    room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    last_read_message_id TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, room_id)
);

-- Denormalized counters
CREATE TRIGGER IF NOT EXISTS trg_memberships_ai
AFTER INSERT ON memberships
BEGIN
  UPDATE communities
  SET member_count = member_count + 1
  WHERE id = NEW.community_id;
END;

CREATE TRIGGER IF NOT EXISTS trg_memberships_ad
AFTER DELETE ON memberships
BEGIN
  UPDATE communities
  SET member_count = CASE WHEN member_count > 0 THEN member_count - 1 ELSE 0 END
  WHERE id = OLD.community_id;
END;

CREATE TRIGGER IF NOT EXISTS trg_upvotes_ai
AFTER INSERT ON upvotes
BEGIN
  UPDATE messages
  SET upvote_count = upvote_count + 1
  WHERE id = NEW.message_id;
END;

CREATE TRIGGER IF NOT EXISTS trg_upvotes_ad
AFTER DELETE ON upvotes
BEGIN
  UPDATE messages
  SET upvote_count = CASE WHEN upvote_count > 0 THEN upvote_count - 1 ELSE 0 END
  WHERE id = OLD.message_id;
END;

-- reply_count increments/decrements on all ancestors (parent, grandparent, etc).
CREATE TRIGGER IF NOT EXISTS trg_messages_reply_ai
AFTER INSERT ON messages
WHEN NEW.parent_id IS NOT NULL
BEGIN
  UPDATE messages
  SET reply_count = reply_count + 1
  WHERE id IN (
    WITH RECURSIVE ancestors(id) AS (
      SELECT NEW.parent_id
      UNION ALL
      SELECT m.parent_id
      FROM messages m
      JOIN ancestors a ON m.id = a.id
      WHERE m.parent_id IS NOT NULL
    )
    SELECT id FROM ancestors
  );
END;

CREATE TRIGGER IF NOT EXISTS trg_messages_reply_ad
AFTER DELETE ON messages
WHEN OLD.parent_id IS NOT NULL
BEGIN
  UPDATE messages
  SET reply_count = CASE WHEN reply_count > 0 THEN reply_count - 1 ELSE 0 END
  WHERE id IN (
    WITH RECURSIVE ancestors(id) AS (
      SELECT OLD.parent_id
      UNION ALL
      SELECT m.parent_id
      FROM messages m
      JOIN ancestors a ON m.id = a.id
      WHERE m.parent_id IS NOT NULL
    )
    SELECT id FROM ancestors
  );
END;

-- Full-text search sync
CREATE TRIGGER IF NOT EXISTS trg_messages_fts_ai
AFTER INSERT ON messages
BEGIN
  INSERT INTO messages_fts(rowid, content) VALUES (NEW.rowid, NEW.content);
END;

CREATE TRIGGER IF NOT EXISTS trg_messages_fts_ad
AFTER DELETE ON messages
BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', OLD.rowid, OLD.content);
END;

CREATE TRIGGER IF NOT EXISTS trg_messages_fts_au
AFTER UPDATE OF content ON messages
BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', OLD.rowid, OLD.content);
  INSERT INTO messages_fts(rowid, content) VALUES (NEW.rowid, NEW.content);
END;
