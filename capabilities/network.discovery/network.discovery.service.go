package discovery

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	peer "github.com/libp2p/go-libp2p/core/peer"
	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
	p2p "github.com/sebastyijan/animasola/capabilities/network.p2p"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

const GlobalDiscoveryTopic = "animasola/global/room-discovery"

const (
	messageTypeAnnounce        = "announce"
	messageTypeSnapshotRequest = "snapshot_request"
	snapshotResponseMinInterval     = 15 * time.Second
	snapshotResponsePerPeerCooldown = 2 * time.Minute
)

type roomDiscoveryMessage struct {
	MessageType string    `json:"message_type"`
	RoomID      string    `json:"room_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatorID   string    `json:"creator_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int       `json:"version"`
	Signature   string    `json:"signature,omitempty"`
}

type Service struct {
	node   *p2p.Node
	store  *sqlite.Store
	user   *sqlite.User
	registry *registry.Client
	topic  *pubsub.Topic
	cancel context.CancelFunc
	mu     sync.RWMutex
	state  Status
	snapshotResponses map[string]time.Time
}

type Status struct {
	StartedAt              time.Time
	FirstDiscoveryPeerAt   time.Time
	LastSnapshotRequestAt  time.Time
	LastSnapshotResponseAt time.Time
	LastAnnouncementAt     time.Time
	LastRemoteAnnouncement time.Time
	KnownPeerCount         int
	ReceivedAnnouncements  int
	SnapshotRequestCount   int
}

func NewService(node *p2p.Node, store *sqlite.Store, user *sqlite.User, registryClient *registry.Client) *Service {
	return &Service{
		node:     node,
		store:    store,
		user:     user,
		registry: registryClient,
		state:    Status{StartedAt: time.Now().UTC()},
		snapshotResponses: make(map[string]time.Time),
	}
}

func (s *Service) Start(discoveryCh chan sqlite.Room) error {
	if s == nil || s.node == nil || s.store == nil || s.user == nil {
		return nil
	}
	if s.topic != nil {
		return nil
	}

	topic, err := s.node.PubSub.Join(GlobalDiscoveryTopic)
	if err != nil {
		return fmt.Errorf("failed to join global discovery topic: %w", err)
	}
	sub, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to discovery topic: %w", err)
	}
	s.topic = topic

	ctx, cancel := context.WithCancel(s.node.Context())
	s.cancel = cancel
	go s.listen(ctx, sub, discoveryCh)
	go s.startPublisher(ctx, 45*time.Second)
	go s.watchPeerState(ctx)
	return nil
}

func (s *Service) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.topic != nil {
		return s.topic.Close()
	}
	return nil
}

func (s *Service) BroadcastRoom(ctx context.Context, room *sqlite.Room) error {
	if s == nil || s.topic == nil {
		return nil
	}
	go func() { _ = s.PublishRoomSync(ctx, room) }()
	return nil
}

func (s *Service) PublishRoomSync(ctx context.Context, room *sqlite.Room) error {
	if s == nil || s.topic == nil || room == nil {
		return nil
	}
	if !room.IsPrivate && s.registry != nil {
		allowed, err := s.validatePublicRoom(ctx, s.node.Host.ID().String(), room.ID)
		if err != nil {
			return fmt.Errorf("failed to validate public room before publish: %w", err)
		}
		if !allowed {
			return fmt.Errorf("public room is not registry-approved for publish")
		}
	}
	if room.UpdatedAt.IsZero() {
		room.UpdatedAt = room.CreatedAt
	}
	if room.Version <= 0 {
		room.Version = 1
	}

	msg := roomDiscoveryMessage{
		MessageType: messageTypeAnnounce,
		RoomID:      room.ID,
		Name:        room.Name,
		Description: room.Description,
		CreatorID:   s.node.Host.ID().String(),
		CreatedAt:   room.CreatedAt,
		UpdatedAt:   room.UpdatedAt,
		Version:     room.Version,
	}
	msg.Signature = s.signMessage(msg)

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal discovery message: %w", err)
	}
	s.markAnnouncement()

	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return s.topic.Publish(ctx, payload)
		case <-ticker.C:
			if len(s.topic.ListPeers()) > 0 {
				return s.topic.Publish(ctx, payload)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Service) RequestSnapshot(ctx context.Context) error {
	if s == nil || s.topic == nil {
		return nil
	}
	s.markSnapshotRequest()
	payload, err := json.Marshal(roomDiscoveryMessage{
		MessageType: messageTypeSnapshotRequest,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		Version:     1,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal discovery snapshot request: %w", err)
	}
	return s.topic.Publish(ctx, payload)
}

func (s *Service) listen(ctx context.Context, sub *pubsub.Subscription, discoveryCh chan sqlite.Room) {
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if msg.ReceivedFrom.String() == s.node.Host.ID().String() {
			continue
		}

		var discoveryMsg roomDiscoveryMessage
		if err := json.Unmarshal(msg.Data, &discoveryMsg); err != nil {
			continue
		}

		switch discoveryMsg.MessageType {
		case "", messageTypeAnnounce:
			s.markRemoteAnnouncement()
		case messageTypeSnapshotRequest:
			if s.shouldRespondToSnapshotRequest(msg.ReceivedFrom.String(), time.Now().UTC()) {
				s.markSnapshotObserved()
				go s.publishKnownPublicRooms(ctx)
			}
			continue
		default:
			continue
		}

		if discoveryMsg.CreatorID == "" || discoveryMsg.CreatorID != msg.ReceivedFrom.String() {
			continue
		}
		if !verifyMessage(discoveryMsg, msg.ReceivedFrom.String()) {
			continue
		}

		allowed, err := s.validatePublicRoom(ctx, discoveryMsg.CreatorID, discoveryMsg.RoomID)
		if err != nil || !allowed {
			continue
		}

		if err := s.store.EnsurePublicRoomExists(ctx, discoveryMsg.RoomID, discoveryMsg.Name, discoveryMsg.Description, discoveryMsg.CreatorID, discoveryMsg.Signature, discoveryMsg.CreatedAt, discoveryMsg.UpdatedAt, discoveryMsg.Version); err != nil {
			continue
		}
		if discoveryCh != nil {
			select {
			case discoveryCh <- sqlite.Room{
				ID:          discoveryMsg.RoomID,
				Name:        discoveryMsg.Name,
				Description: discoveryMsg.Description,
				CreatorID:   discoveryMsg.CreatorID,
				Signature:   discoveryMsg.Signature,
				IsPrivate:   false,
				CreatedAt:   discoveryMsg.CreatedAt,
				UpdatedAt:   discoveryMsg.UpdatedAt,
				Version:     discoveryMsg.Version,
			}:
			default:
			}
		}
	}
}

func (s *Service) startPublisher(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.publishKnownPublicRooms(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.publishKnownPublicRooms(ctx)
		}
	}
}

func (s *Service) publishKnownPublicRooms(ctx context.Context) {
	if s.topic == nil {
		return
	}
	rooms, err := s.store.ListOwnedPublicRooms(ctx, s.user.ID)
	if err != nil {
		return
	}
	for _, room := range rooms {
		room := room
		pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = s.PublishRoomSync(pubCtx, &room)
		cancel()
	}
}

func (s *Service) validatePublicRoom(ctx context.Context, creatorID, roomID string) (bool, error) {
	if s.registry == nil {
		return true, nil
	}
	validateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.registry.ValidatePublicRoom(validateCtx, creatorID, roomID)
}

func (s *Service) UpdatePublicRoomMetadata(ctx context.Context, roomID, ownerID, name, description string) (*sqlite.Room, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("discovery service unavailable")
	}

	current, err := s.store.GetRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}

	updatedAt := time.Now().UTC()
	nextVersion := current.Version + 1
	signature := s.signMessage(roomDiscoveryMessage{
		MessageType: messageTypeAnnounce,
		RoomID:      current.ID,
		Name:        name,
		Description: description,
		CreatorID:   s.node.Host.ID().String(),
		CreatedAt:   current.CreatedAt,
		UpdatedAt:   updatedAt,
		Version:     nextVersion,
	})

	updated, err := s.store.UpdatePublicRoomMetadata(ctx, roomID, ownerID, name, description, signature)
	if err != nil {
		return nil, err
	}
	updated.CreatorID = s.node.Host.ID().String()
	if err := s.PublishRoomSync(ctx, updated); err != nil {
		return updated, err
	}
	return updated, nil
}

func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Service) watchPeerState(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.updatePeerCount()
		}
	}
}

func (s *Service) signMessage(msg roomDiscoveryMessage) string {
	if s.node.Identity == nil {
		return ""
	}
	return s.node.Identity.Sign(signingPayload(msg))
}

func verifyMessage(msg roomDiscoveryMessage, receivedFrom string) bool {
	peerID, err := peer.Decode(receivedFrom)
	if err != nil {
		return false
	}
	pubKey, err := peerID.ExtractPublicKey()
	if err != nil {
		return false
	}
	rawPub, err := pubKey.Raw()
	if err != nil {
		return false
	}
	return keys.VerifySignature(ed25519.PublicKey(rawPub), signingPayload(msg), msg.Signature)
}

func signingPayload(msg roomDiscoveryMessage) []byte {
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d",
		msg.MessageType,
		msg.RoomID,
		msg.Name,
		msg.Description,
		msg.CreatorID,
		msg.CreatedAt.UTC().Format(time.RFC3339Nano),
		msg.UpdatedAt.UTC().Format(time.RFC3339Nano),
		msg.Version,
	))
}

func (s *Service) markSnapshotRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastSnapshotRequestAt = time.Now().UTC()
	s.state.SnapshotRequestCount++
}

func (s *Service) markAnnouncement() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastAnnouncementAt = time.Now().UTC()
}

func (s *Service) markSnapshotObserved() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.SnapshotRequestCount++
}

func (s *Service) shouldRespondToSnapshotRequest(peerID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.snapshotResponses == nil {
		s.snapshotResponses = make(map[string]time.Time)
	}

	if last := s.state.LastSnapshotResponseAt; !last.IsZero() && now.Sub(last) < snapshotResponseMinInterval {
		return false
	}

	if last := s.snapshotResponses[peerID]; !last.IsZero() && now.Sub(last) < snapshotResponsePerPeerCooldown {
		return false
	}

	s.snapshotResponses[peerID] = now
	s.state.LastSnapshotResponseAt = now
	return true
}

func (s *Service) markRemoteAnnouncement() {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastRemoteAnnouncement = now
	s.state.ReceivedAnnouncements++
}

func (s *Service) updatePeerCount() {
	if s.topic == nil {
		return
	}
	now := time.Now().UTC()
	peerCount := len(s.topic.ListPeers())

	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.KnownPeerCount = peerCount
	if peerCount > 0 && s.state.FirstDiscoveryPeerAt.IsZero() {
		s.state.FirstDiscoveryPeerAt = now
		if s.node != nil && s.node.Telemetry != nil {
			go s.node.Telemetry.RecordEvent(context.Background(), "Discovery_First_Peer_Latency", int(now.Sub(s.state.StartedAt).Milliseconds()), map[string]string{
				"action": "discovery_start",
			}, runtime.GOOS, runtime.GOARCH)
		}
	}
}
