package server

import (
	"context"

	"animasola/internal/pubsub"
	"animasola/internal/store"
)

// storeWithEvents wraps the store and publishes pubsub events on mutations.
// This is how other SSH sessions get realtime updates.
type storeWithEvents struct {
	st     *store.Store
	broker *pubsub.Broker[pubsub.Event]
}

func (s *storeWithEvents) CreateCommunity(ctx context.Context, createdByUserID, name, description string) (*store.Community, *store.Room, error) {
	return s.st.CreateCommunity(ctx, createdByUserID, name, description)
}

func (s *storeWithEvents) GetCommunityByName(ctx context.Context, name string) (*store.Community, error) {
	return s.st.GetCommunityByName(ctx, name)
}

func (s *storeWithEvents) GetCommunityByID(ctx context.Context, id string) (*store.Community, error) {
	return s.st.GetCommunityByID(ctx, id)
}

func (s *storeWithEvents) JoinCommunity(ctx context.Context, userID, communityID string) error {
	err := s.st.JoinCommunity(ctx, userID, communityID)
	if err == nil && s.broker != nil {
		s.broker.Publish(pubsub.Event{
			Type:        pubsub.EventUserJoined,
			CommunityID: communityID,
			Username:    userID,
		})
	}
	return err
}

func (s *storeWithEvents) ListExploreCommunities(ctx context.Context, userID string, limit int) ([]store.Community, error) {
	return s.st.ListExploreCommunities(ctx, userID, limit)
}

func (s *storeWithEvents) ListJoinedCommunities(ctx context.Context, userID string) ([]store.Community, error) {
	return s.st.ListJoinedCommunities(ctx, userID)
}

func (s *storeWithEvents) ListHomeFeed(ctx context.Context, userID string, sortMode store.SortMode, topRange store.TopRange, limit, offset int) ([]store.FeedMessage, error) {
	return s.st.ListHomeFeed(ctx, userID, sortMode, topRange, limit, offset)
}

func (s *storeWithEvents) ListRoomsByCommunity(ctx context.Context, communityID string) ([]store.Room, error) {
	return s.st.ListRoomsByCommunity(ctx, communityID)
}

func (s *storeWithEvents) GetRoomByID(ctx context.Context, id string) (*store.Room, error) {
	return s.st.GetRoomByID(ctx, id)
}

func (s *storeWithEvents) UpsertReadPosition(ctx context.Context, userID, roomID, lastReadMessageID string) error {
	return s.st.UpsertReadPosition(ctx, userID, roomID, lastReadMessageID)
}

func (s *storeWithEvents) UnreadCountsByCommunity(ctx context.Context, userID string) ([]store.CommunityUnread, error) {
	return s.st.UnreadCountsByCommunity(ctx, userID)
}

func (s *storeWithEvents) ListRoomTopLevelNew(ctx context.Context, roomID string, limit int, beforeID *string) ([]store.FeedMessage, error) {
	return s.st.ListRoomTopLevelNew(ctx, roomID, limit, beforeID)
}

func (s *storeWithEvents) ListThread(ctx context.Context, rootMessageID string) ([]store.FeedMessage, error) {
	return s.st.ListThread(ctx, rootMessageID)
}

func (s *storeWithEvents) CreateMessage(ctx context.Context, roomID, authorID, content string, parentID *string) (*store.Message, error) {
	m, err := s.st.CreateMessage(ctx, roomID, authorID, content, parentID)
	if err == nil && s.broker != nil {
		s.broker.Publish(pubsub.Event{
			Type:      pubsub.EventNewMessage,
			RoomID:    roomID,
			MessageID: m.ID,
		})
	}
	return m, err
}

func (s *storeWithEvents) ToggleUpvote(ctx context.Context, userID, messageID string) (*store.UpvoteState, error) {
	st, err := s.st.ToggleUpvote(ctx, userID, messageID)
	if err == nil && s.broker != nil {
		msg, err2 := s.st.GetMessageByID(ctx, messageID)
		if err2 == nil && msg != nil {
			s.broker.Publish(pubsub.Event{
				Type:        pubsub.EventUpvoteUpdate,
				RoomID:      msg.RoomID,
				MessageID:   messageID,
				UpvoteCount: st.Count,
			})
		}
	}
	return st, err
}

func (s *storeWithEvents) SoftDeleteMessage(ctx context.Context, messageID, requesterUserID string) error {
	msg, _ := s.st.GetMessageByID(ctx, messageID)
	err := s.st.SoftDeleteMessage(ctx, messageID, requesterUserID)
	if err == nil && s.broker != nil && msg != nil {
		s.broker.Publish(pubsub.Event{
			Type:      pubsub.EventDeleteMessage,
			RoomID:    msg.RoomID,
			MessageID: messageID,
		})
	}
	return err
}
