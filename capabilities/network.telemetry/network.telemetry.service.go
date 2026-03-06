package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	version "github.com/sebastyijan/animasola/capabilities/core.version"
)

const GlobalTelemetryTopic = "animasola/telemetry/v1"

// TelemetryEvent represents an anonymous, untraceable performance metric ping
type TelemetryEvent struct {
	EventName string            `json:"event_name"`
	Duration  int               `json:"duration_ms"` // Resolution/Execution time in ms
	Metadata  map[string]string `json:"metadata"`
	Version   string            `json:"version"`   // App semantic version (e.g. v0.2.11)
	Platform  string            `json:"platform"`  // OS/Arch
	Timestamp int64             `json:"timestamp"` // Unix emission time
}

type Service struct {
	topic        *pubsub.Topic
	sub          *pubsub.Subscription
	localChannel chan TelemetryEvent // Feed for the on-screen HUD
}

// NewService hooks into an existing Libp2p/Tor node and provisions the global telemetry mesh.
func NewService(ps *pubsub.PubSub) (*Service, error) {
	topic, err := ps.Join(GlobalTelemetryTopic)
	if err != nil {
		return nil, fmt.Errorf("failed to join global telemetry topic: %v", err)
	}

	return &Service{
		topic: topic,
	}, nil
}

// WaitForMesh statically blocks until the topic peer list is > 0
func (s *Service) WaitForMesh(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for telemetry mesh sync")
		case <-ticker.C:
			if len(s.topic.ListPeers()) > 0 {
				return nil
			}
		}
	}
}

// RecordEvent queues the anonymous telemetry struct and broadcasts it asynchronously over Tor.
func (s *Service) RecordEvent(ctx context.Context, name string, durationMs int, meta map[string]string, goos, goarch string) {
	if s.topic == nil || name == "" {
		return
	}

	event := TelemetryEvent{
		EventName: name,
		Duration:  durationMs,
		Metadata:  meta,
		Version:   version.Current,
		Platform:  fmt.Sprintf("%s/%s", goos, goarch),
		Timestamp: time.Now().Unix(),
	}

	// Double-wire HUD Interceptor
	if s.localChannel != nil {
		select {
		case s.localChannel <- event:
		default: // Non-blocking: if the HUD buffer is full, drop the visual metric
		}
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return // Silently drop telemetry encoding failures to never crash the user
	}

	// Always emit telemetry asynchronously so we NEVER block the Bubbletea UI or critical paths
	go func() {
		// Try up to 5 times (total 50s wait) to give the Tor/Kademlia mesh time to discover
		// the Headless Telemetry Admin on the network before giving up and dropping the packet.
		for i := 0; i < 5; i++ {
			pubCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := s.topic.Publish(pubCtx, payload)
			cancel()

			if err == nil {
				return // Success! Pushed to Kademlia mesh
			}

			// If publish failed (often "Insufficient Peers"), sleep and wait for the
			// background Tor discovery routing to catch up.
			time.Sleep(10 * time.Second)
		}
	}()
}

// SetLocalInterceptor links the global Kademlia broadcast payload to a local Bubbletea
// channel so the real-time Analytics HUD can render the diagnostics on screen!
func (s *Service) SetLocalInterceptor(ch chan TelemetryEvent) {
	s.localChannel = ch
}

// StartHeadlessListener completely reverses the data flow. It runs on the developer server,
// subscribing to the global telemetry swarm and outputting the received metrics to stdout
// for Logstash/Datadog ingesting.
func (s *Service) StartHeadlessListener(ctx context.Context) error {
	sub, err := s.topic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to headless telemetry feed: %v", err)
	}

	log.Printf("[+] Headless Telemetry Collector Started.")
	log.Printf("[+] Subscribed to global Kademlia Topic: %s", GlobalTelemetryTopic)
	log.Printf("[+] Awaiting untraceable client payloads...")

	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("[-] Telemetry feed read error: %v", err)
			continue
		}

		var event TelemetryEvent
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			log.Printf("[-] Invalid Telemetry JSON ignored from remote node.")
			continue
		}

		// Dump raw JSON directly to STDOUT for external piping (e.g. `animasola telemetry >> metrics.jsonl`)
		fmt.Printf("%s\n", msg.Data)
	}
}
