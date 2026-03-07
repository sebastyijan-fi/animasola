package sqlite

import (
	"time"
)

type User struct {
	ID        string
	Username  string
	CreatedAt time.Time
}

type Room struct {
	ID            string
	Name          string
	Description   string
	CreatorID     string
	Signature     string
	IsPrivate     bool
	RoomKey       string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastSeenAt    time.Time
	Version       int
	AnnounceCount int
	HasUnread     bool
	IsHidden      bool
	IsTrusted     bool
	CreatorMuted  bool
	CreatorBlocked bool
}

type Message struct {
	ID        string
	RoomID    string
	AuthorID  string
	AuthorUsername string
	Content   string
	CreatedAt time.Time
}

type FeedMessage struct {
	Message
	RoomName       string
	AuthorUsername string
}
