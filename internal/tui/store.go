package tui

import (
	"context"
	"time"

	"animasola/internal/store"
)

// Store is the minimal persistence surface the TUI needs.
// Keeping this as an interface makes the TUI testable without spinning up SSH.
type Store interface {
	CreateCommunity(ctx context.Context, createdByUserID, name, description string) (*store.Community, *store.Room, error)
	GetCommunityByName(ctx context.Context, name string) (*store.Community, error)
	GetCommunityByID(ctx context.Context, id string) (*store.Community, error)
	JoinCommunity(ctx context.Context, userID, communityID string) error
	LeaveCommunity(ctx context.Context, userID, communityID string) error
	ListExploreCommunities(ctx context.Context, userID string, limit int) ([]store.Community, error)
	ListJoinedCommunities(ctx context.Context, userID string) ([]store.Community, error)
	ListHomeFeed(ctx context.Context, userID string, sortMode store.SortMode, topRange store.TopRange, limit, offset int) ([]store.FeedMessage, error)
	ListHomeNewPage(ctx context.Context, userID string, limit int, beforeID *string) ([]store.FeedMessage, *string, error)
	ListHomeTopPage(ctx context.Context, userID string, topRange store.TopRange, limit int, before *store.HomeTopCursor) ([]store.FeedMessage, *store.HomeTopCursor, error)
	ListHomeHotPage(ctx context.Context, userID string, now time.Time, limit int, before *store.HomeHotCursor) ([]store.FeedMessage, *store.HomeHotCursor, error)
	SearchMessages(ctx context.Context, userID string, query string, limit int) ([]store.SearchResult, error)
	ResolveThreadRootID(ctx context.Context, messageID string) (string, error)
	CreateRoom(ctx context.Context, communityID, name string) (*store.Room, error)
	ListRoomsByCommunity(ctx context.Context, communityID string) ([]store.Room, error)
	CountRoomsByCommunity(ctx context.Context, communityID string) (int, error)
	GetRoomByID(ctx context.Context, id string) (*store.Room, error)
	UpsertReadPosition(ctx context.Context, userID, roomID, lastReadMessageID string) error
	UnreadCountsByCommunity(ctx context.Context, userID string) ([]store.CommunityUnread, error)
	ListRoomTopLevelNew(ctx context.Context, roomID string, limit int, beforeID *string) ([]store.FeedMessage, error)
	ListRoomTopLevelNewPage(ctx context.Context, roomID string, limit int, beforeID *string) ([]store.FeedMessage, *string, error)
	ListRoomTopLevelTopPage(ctx context.Context, roomID string, topRange store.TopRange, limit int, before *store.RoomTopCursor) ([]store.FeedMessage, *store.RoomTopCursor, error)
	ListRoomTopLevelHotPage(ctx context.Context, roomID string, now time.Time, limit int, before *store.RoomHotCursor) ([]store.FeedMessage, *store.RoomHotCursor, error)
	ListThread(ctx context.Context, rootMessageID string) ([]store.FeedMessage, error)
	CreateMessage(ctx context.Context, roomID, authorID, content string, parentID *string) (*store.Message, error)
	ToggleUpvote(ctx context.Context, userID, messageID string) (*store.UpvoteState, error)
	SoftDeleteMessage(ctx context.Context, messageID, requesterUserID string) error
}
