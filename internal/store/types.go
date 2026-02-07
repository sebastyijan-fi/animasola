package store

import "time"

type Community struct {
	ID          string
	Name        string
	Description string
	CreatedBy   string
	CreatedAt   time.Time
	MemberCount int
}

type Room struct {
	ID              string
	Name            string
	CommunityID     string
	PinnedMessageID *string
	CreatedAt       time.Time
}

type Message struct {
	ID        string
	RoomID    string
	AuthorID  string
	Content   string
	ParentID  *string
	Upvotes   int
	Replies   int
	IsDeleted bool
	CreatedAt time.Time
}

type FeedMessage struct {
	Message
	AuthorUsername string
	CommunityID    string
	CommunityName  string
	RoomName       string
}

type SortMode string

const (
	SortNew SortMode = "new"
	SortTop SortMode = "top"
	SortHot SortMode = "hot"
)

type TopRange string

const (
	TopToday TopRange = "today"
	TopWeek  TopRange = "week"
	TopMonth TopRange = "month"
	TopAll   TopRange = "all"
)
