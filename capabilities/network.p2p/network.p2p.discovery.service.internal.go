package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

const GlobalDiscoveryTopic = "animasola/global/room-discovery"

// RoomDiscoveryMessage is the payload sent when a new public room is created
type RoomDiscoveryMessage struct {
	RoomID      string    `json:"room_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatorID   string    `json:"creator_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// StartGlobalDiscovery joins the global discovery topic and listens for announcements
// of new public rooms, adding them to the local SQLite database.
func (n *Node) StartGlobalDiscovery(db *sqlite.Store, discoveryCh chan sqlite.Room) error {
	topic, err := n.PubSub.Join(GlobalDiscoveryTopic)
	if err != nil {
		return fmt.Errorf("failed to join global discovery topic: %w", err)
	}

	sub, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to discovery topic: %w", err)
	}

	n.DiscoveryTopic = topic

	// Start background listener
	go func() {
		for {
			select {
			case <-n.ctx.Done():
				return
			default:
				msg, err := sub.Next(n.ctx)
				if err != nil {
					return
				}

				// Don't process our own broadcast
				if msg.ReceivedFrom.String() == n.Host.ID().String() {
					continue
				}

				var discoveryMsg RoomDiscoveryMessage
				if err := json.Unmarshal(msg.Data, &discoveryMsg); err != nil {
					fmt.Printf("Warning: failed to unmarshal discovery message: %v\n", err)
					continue
				}

				err = db.EnsurePublicRoomExists(
					n.ctx,
					discoveryMsg.RoomID,
					discoveryMsg.Name,
					discoveryMsg.Description,
					discoveryMsg.CreatorID,
					discoveryMsg.CreatedAt,
				)
				if err != nil {
					fmt.Printf("Warning: failed to insert discovered room %s: %v\n", discoveryMsg.Name, err)
				} else if discoveryCh != nil {
					// Notify the UI so it can refresh if the user is in search mode
					discoveryCh <- sqlite.Room{
						ID:          discoveryMsg.RoomID,
						Name:        discoveryMsg.Name,
						Description: discoveryMsg.Description,
						IsPrivate:   false,
						CreatedAt:   discoveryMsg.CreatedAt,
					}
				}
			}
		}
	}()

	return nil
}

// BroadcastRoomDiscovery announces to all connected peers that a new public room exists.
func (n *Node) BroadcastRoomDiscovery(ctx context.Context, room *sqlite.Room) error {
	if n.DiscoveryTopic == nil {
		return fmt.Errorf("discovery topic is not initialized")
	}

	msg := RoomDiscoveryMessage{
		RoomID:      room.ID,
		Name:        room.Name,
		Description: room.Description,
		CreatedAt:   room.CreatedAt,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal discovery message: %w", err)
	}

	return n.DiscoveryTopic.Publish(ctx, payload)
}
