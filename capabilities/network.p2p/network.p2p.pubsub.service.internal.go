package p2p

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

const maxNetworkPayloadBytes = 8 * 1024
const inboundMessageBurstLimit = 20
const inboundRoomBurstLimit = 30
const inboundMessageBurstWindow = 10 * time.Second

type inboundMessageLimiter struct {
	entries map[string][]time.Time
}

func newInboundMessageLimiter() *inboundMessageLimiter {
	return &inboundMessageLimiter{entries: make(map[string][]time.Time)}
}

func (l *inboundMessageLimiter) allow(authorID string, now time.Time) bool {
	return l.allowWithLimit(authorID, now, inboundMessageBurstLimit)
}

func (l *inboundMessageLimiter) allowWithLimit(key string, now time.Time, limit int) bool {
	cutoff := now.Add(-inboundMessageBurstWindow)
	items := l.entries[key]
	filtered := items[:0]
	for _, ts := range items {
		if ts.After(cutoff) {
			filtered = append(filtered, ts)
		}
	}
	if len(filtered) >= limit {
		l.entries[key] = filtered
		return false
	}
	l.entries[key] = append(filtered, now)
	return true
}

func logDebug(format string, a ...interface{}) {
	if os.Getenv("ANIMASOLA_DEBUG_NETWORK") != "1" {
		return
	}

	logPath, err := networkDebugLogPath()
	if err != nil {
		return
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		defer f.Close()
		msg := fmt.Sprintf(format, a...)
		f.WriteString(time.Now().Format(time.RFC3339) + " " + msg + "\n")
	}
}

func networkDebugLogPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	logDir := filepath.Join(configDir, "animasola")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return "", err
	}

	return filepath.Join(logDir, "network_debug.log"), nil
}

// NetworkMessage is the JSON payload we send over GossipSub
type NetworkMessage struct {
	ID             string `json:"id"`
	RoomID         string `json:"room_id"`
	RoomName       string `json:"room_name"`
	AuthorID       string `json:"author_id"`
	AuthorUsername string `json:"author_username"`
	Content        string `json:"content"`
	CreatedAt      string `json:"created_at"`
}

// Room represents an active PubSub subscription to a specific chat room
type Room struct {
	ID        string
	Topic     *pubsub.Topic
	Sub       *pubsub.Subscription
	ctx       context.Context
	cancel    context.CancelFunc
	IsPrivate bool
	RoomKey   []byte
}

// JoinRoom subscribes to a GossipSub topic for the given room ID
func (n *Node) JoinRoom(roomID string, isPrivate bool, password string, requireDHT bool) (*Room, error) {
	topicName := fmt.Sprintf("animasola/room/%s", roomID)
	topic, err := n.PubSub.Join(topicName)
	if err != nil {
		return nil, fmt.Errorf("failed to join topic: %w", err)
	}

	sub, err := topic.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to topic: %w", err)
	}

	var rKey []byte
	if isPrivate && password != "" {
		// Accept either the raw password or a stored derived room key.
		rKey, err = sqlite.DecodeOrDerivePrivateRoomKey(roomID, password)
		if err != nil {
			_ = topic.Close()
			return nil, fmt.Errorf("failed to prepare room key: %w", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &Room{
		ID:        roomID,
		Topic:     topic,
		Sub:       sub,
		ctx:       ctx,
		cancel:    cancel,
		IsPrivate: isPrivate,
		RoomKey:   rKey,
	}

	// UX HARDENING: If this is a public room search join, we must verify the DHT actually resolved a peer.
	// If the user tries to join a ghost room that everyone has left, waiting 5 seconds and gracefully rejecting
	// is infinitely better than dumping them into a broken empty UI.
	// REQUIREDHT: Only execute this block if explicitly told to via a Remote Search action.
	if !isPrivate && requireDHT {
		timeout := time.After(5 * time.Second)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		peerFound := false
	CheckLoop:
		for {
			select {
			case <-timeout:
				break CheckLoop
			case <-ticker.C:
				if len(topic.ListPeers()) > 0 {
					peerFound = true
					break CheckLoop
				}
			}
		}

		if !peerFound {
			r.LeaveRoom()
			return nil, fmt.Errorf("Room does not exist or all hosts are offline. Press 'c' to create it locally.")
		}
	}

	return r, nil
}

// WaitForRoomPeers blocks until at least 1 peer is found on the topic, or the timeout is reached.
// We use this to hold the user in a loading screen while the Tor DHT resolves.
func (n *Node) WaitForRoomPeers(ctx context.Context, roomID string, timeout time.Duration) error {
	startTime := time.Now()
	topicName := fmt.Sprintf("animasola/room/%s", roomID)

	// Join the topic to actively probe the GossipSub mesh
	topic, err := n.PubSub.Join(topicName)
	if err != nil {
		return fmt.Errorf("failed to join topic for peer resolution: %w", err)
	}
	// We deliberately leave the topic "Join" open here! When the user officially drops
	// into the RoomModel and calls JoinRoom natively, pubsub handles the de-duplication
	// and they instantiate instantly without dropping the mesh connection!

	timeoutCh := time.After(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCh:
			topic.Close()
			return fmt.Errorf("Room does not exist or all hosts are offline. Press 'c' to create it locally.")
		case <-ticker.C:
			if len(topic.ListPeers()) > 0 {
				if n.Telemetry != nil {
					duration := int(time.Since(startTime).Milliseconds())
					n.Telemetry.RecordEvent(ctx, "DHT_Resolution_Latency", duration, map[string]string{
						"action": "room_join",
					}, runtime.GOOS, runtime.GOARCH)
				}
				return nil
			}
		case <-ctx.Done():
			topic.Close()
			return ctx.Err()
		}
	}
}

// LeaveRoom unsubscribes from the topic
func (r *Room) LeaveRoom() {
	r.cancel()
	r.Sub.Cancel()
	_ = r.Topic.Close() // #nosec G104 -- Errors on closing a topic during teardown are non-fatal.
}

// Publish broadcasts a message to all peers in the room
func (r *Room) Publish(ctx context.Context, msg *sqlite.Message, roomName string, authorUsername string) error {
	netMsg := NetworkMessage{
		ID:             msg.ID,
		RoomID:         msg.RoomID,
		RoomName:       roomName,
		AuthorID:       msg.AuthorID,
		AuthorUsername: authorUsername,
		Content:        msg.Content,
		CreatedAt:      msg.CreatedAt.Format(sqlite.SortableTimeFormat),
	}

	payload, err := json.Marshal(netMsg)
	if err != nil {
		logDebug("Publish: failed to marshal message: %v", err)
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if r.IsPrivate && len(r.RoomKey) > 0 {
		block, err := aes.NewCipher(r.RoomKey)
		if err != nil {
			return err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}
		nonce := make([]byte, gcm.NonceSize())
		if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
			return err
		}
		payload = gcm.Seal(nonce, nonce, payload, nil)
	}

	logDebug("Publish: broadcasting payload length %d to topic", len(payload))
	return r.Topic.Publish(ctx, payload)
}

// Listen blocks and waits for new messages, writing them to the SQLite store
func (r *Room) Listen(db *sqlite.Store, hostID string, onNewMessage func(sqlite.FeedMessage)) {
	authorLimiter := newInboundMessageLimiter()
	roomLimiter := newInboundMessageLimiter()
	registryClient := registry.NewClient()
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
			msg, err := r.Sub.Next(r.ctx)
			if err != nil {
				// Context canceled or subscription closed
				return
			}

			// Don't process our own messages, we already saved them locally when sending
			if msg.ReceivedFrom.String() == hostID {
				continue
			}

			if len(msg.Data) > maxNetworkPayloadBytes {
				logDebug("Listen: dropping oversized inbound payload length %d", len(msg.Data))
				continue
			}

			payload := msg.Data

			if r.IsPrivate && len(r.RoomKey) > 0 {
				block, err := aes.NewCipher(r.RoomKey)
				if err != nil {
					fmt.Printf("Warning: failed to create cipher: %s\n", err)
					continue
				}
				gcm, err := cipher.NewGCM(block)
				if err != nil {
					fmt.Printf("Warning: failed to create gcm: %s\n", err)
					continue
				}
				nonceSize := gcm.NonceSize()
				if len(payload) < nonceSize {
					fmt.Println("Warning: incoming encrypted payload too short")
					continue
				}
				nonce, ciphertext := payload[:nonceSize], payload[nonceSize:]
				decrypted, err := gcm.Open(nil, nonce, ciphertext, nil)
				if err != nil {
					// Wrong password or tampering. Simply drop the message.
					continue
				}
				payload = decrypted
			}

			logDebug("Listen: processing payload length %d", len(payload))

			var netMsg NetworkMessage
			if err := json.Unmarshal(payload, &netMsg); err != nil {
				logDebug("Listen: failed to unmarshal p2p message: %v", err)
				fmt.Printf("Warning: failed to unmarshal p2p message: %s\n", err)
				continue
			}

			logDebug("Listen: unmarshaled message from author %s", netMsg.AuthorID)

			// 1. Cryptographic Envelope Binding (Anti-Forgery)
			// The outer Libp2p envelope `msg.ReceivedFrom` is mathematically signed by the sender's private key.
			// The inner JSON `netMsg.AuthorID` is just an untrusted string. We MUST assert they match.
			if netMsg.AuthorID != msg.ReceivedFrom.String() {
				fmt.Printf("SECURITY ALERT: Dropping forged payload! Inner Author %s does not match Outer Envelope Signature %s\n", netMsg.AuthorID, msg.ReceivedFrom.String())
				continue
			}
			if netMsg.RoomID != r.ID {
				logDebug("Listen: dropping cross-room payload room=%s expected=%s", netMsg.RoomID, r.ID)
				continue
			}
			now := time.Now().UTC()
			if !authorLimiter.allow(netMsg.AuthorID, now) {
				logDebug("Listen: dropping burst payload from author %s", netMsg.AuthorID)
				continue
			}
			if !roomLimiter.allowWithLimit(r.ID, now, inboundRoomBurstLimit) {
				logDebug("Listen: dropping aggregate room burst payload room=%s", r.ID)
				continue
			}

			username := netMsg.AuthorUsername
			if username == "" || strings.TrimSpace(username) != username {
				logDebug("Listen: dropping message with missing or malformed username from %s", netMsg.AuthorID)
				continue
			}
			usernameRunes := []rune(username)
			if len(usernameRunes) > 32 {
				logDebug("Listen: dropping message with oversized username from %s", netMsg.AuthorID)
				continue
			}
			allowed, err := registryClient.ValidateProfile(r.ctx, username, netMsg.AuthorID)
			if err != nil {
				logDebug("Listen: dropping message because registry profile validation failed for %s/%s: %v", username, netMsg.AuthorID, err)
				continue
			}
			if !allowed {
				logDebug("Listen: dropping message because username %s is not bound to %s", username, netMsg.AuthorID)
				continue
			}

			err = db.EnsureRemoteUserExists(r.ctx, netMsg.AuthorID, username)
			if err != nil {
				logDebug("Listen: failed to ensure remote user: %v", err)
				fmt.Printf("Warning: failed to ensure remote user: %s\n", err)
				continue
			}

			logDebug("Listen: ensured user %s exists. Syncing message %s", username, netMsg.ID)

			t, err := time.Parse(sqlite.SortableTimeFormat, netMsg.CreatedAt)
			if err != nil {
				fmt.Printf("Warning: dropping payload with malformed timestamp: %s\n", err)
				continue
			}

			// 2. Time-Jack Validation (Anti-API Pinning)
			// Calculate delta between the sender's asserted timestamp and our local system clock
			delta := time.Since(t)

			// Protect against extreme historical replay attacks (older than 24 hours)
			if delta > 24*time.Hour {
				fmt.Printf("SECURITY ALERT: Dropping Time-Jack payload! Timestamp %s is >24 hours in the past.\n", netMsg.CreatedAt)
				continue
			}

			// Protect against future UI pinning attacks (more than 15 minutes in the future)
			// Note: `time.Since` returns a negative duration for future timestamps.
			if delta < -15*time.Minute {
				fmt.Printf("SECURITY ALERT: Dropping Time-Jack payload! Timestamp %s is >15 minutes in the future.\n", netMsg.CreatedAt)
				continue
			}

			// Enforce max chat length to prevent malicious UI freezing
			contentRunes := []rune(netMsg.Content)
			if len(contentRunes) > sqlite.MaxMessageContentRunes() {
				netMsg.Content = string(contentRunes[:sqlite.MaxMessageContentRunes()])
			}

			dbMsg := sqlite.Message{
				ID:             netMsg.ID,
				RoomID:         netMsg.RoomID,
				AuthorID:       netMsg.AuthorID,
				AuthorUsername: username,
				Content:        netMsg.Content,
				CreatedAt:      t,
			}

			// If the message brings a RoomName, we should update our local stub room if it's currently generic
			if netMsg.RoomName != "" && netMsg.RoomName != "Remote Room" {
				roomNameRunes := []rune(netMsg.RoomName)
				if len(roomNameRunes) > 64 {
					netMsg.RoomName = string(roomNameRunes[:64])
				}
				db.UpdateRoomNameIfDefault(r.ctx, netMsg.RoomID, netMsg.RoomName)
			}

			// Insert into DB. If err is "UNIQUE constraint failed", it means it's our own
			// echoed message or we already synced it. We can ignore that safely.
			err = db.SyncMessage(r.ctx, &dbMsg)
			if err != nil {
				logDebug("Listen: sync message failed (likely self/duplicate): %v", err)
			} else {
				logDebug("Listen: message synced successfully! Triggering UI callback.")
			}

			// Trigger the UI callback so the Feed updates in real time
			feedMsg := sqlite.FeedMessage{
				Message: sqlite.Message{
					ID:        dbMsg.ID,
					RoomID:    dbMsg.RoomID,
					AuthorID:  dbMsg.AuthorID,
					AuthorUsername: dbMsg.AuthorUsername,
					Content:   dbMsg.Content,
					CreatedAt: dbMsg.CreatedAt,
				},
				RoomName:       netMsg.RoomName,
				AuthorUsername: username,
			}

			if onNewMessage != nil {
				onNewMessage(feedMsg)
			}
		}
	}
}
