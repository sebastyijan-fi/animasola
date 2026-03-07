package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
)

func TestHandleSearchPublicRoomsReturnsMatchingRooms(t *testing.T) {
	db := newTestRegistryDB(t)
	srv := &server{db: db}
	ctx := context.Background()

	if err := srv.registerProfile(ctx, "alice", testPeerID(1), "203.0.113.10"); err != nil {
		t.Fatalf("registerProfile alice failed: %v", err)
	}
	if err := srv.registerProfile(ctx, "bob", testPeerID(2), "203.0.113.11"); err != nil {
		t.Fatalf("registerProfile bob failed: %v", err)
	}
	if err := srv.registerPublicRoom(ctx, "alice", testPeerID(1), registry.DerivePublicRoomID("vibecoders"), "vibecoders"); err != nil {
		t.Fatalf("registerPublicRoom vibecoders failed: %v", err)
	}
	if err := srv.registerPublicRoom(ctx, "bob", testPeerID(2), registry.DerivePublicRoomID("gardening"), "gardening"); err != nil {
		t.Fatalf("registerPublicRoom gardening failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/public-rooms/search?query=vibe", nil)
	rr := httptest.NewRecorder()
	srv.handleSearchPublicRooms(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var results []publicRoomSearchResult
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "vibecoders" {
		t.Fatalf("expected vibecoders, got %q", results[0].Name)
	}
}
