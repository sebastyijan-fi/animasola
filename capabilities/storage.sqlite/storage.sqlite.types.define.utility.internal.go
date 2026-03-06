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
	ID          string
	Name        string
	Description string
	IsPrivate   bool
	RoomKey     string
	CreatedAt   time.Time
	HasUnread   bool
}

type Message struct {
	ID        string
	RoomID    string
	AuthorID  string
	Content   string
	CreatedAt time.Time
}

type FeedMessage struct {
	Message
	RoomName       string
	AuthorUsername string
}
