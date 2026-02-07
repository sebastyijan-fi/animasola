package pubsub

type EventType string

const (
	EventNewMessage    EventType = "new_message"
	EventDeleteMessage EventType = "delete_message"
	EventUpvoteUpdate  EventType = "upvote_update"
	EventUserJoined    EventType = "user_joined"
)

type Event struct {
	Type EventType

	RoomID      string
	CommunityID string

	MessageID string

	UpvoteCount int
	Username    string
}
