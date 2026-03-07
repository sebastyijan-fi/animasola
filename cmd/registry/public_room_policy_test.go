package main

import (
	"context"
	"testing"

	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
)

func TestRegisterPublicRoomAllowsThreePerProfileThenBlocksFourth(t *testing.T) {
	db := newTestRegistryDB(t)
	srv := &server{db: db}
	ctx := context.Background()

	username := "roomowner"
	peerID := testPeerID(1)
	if err := srv.registerProfile(ctx, username, peerID, "203.0.113.55"); err != nil {
		t.Fatalf("registerProfile failed: %v", err)
	}

	for i := 0; i < maxActivePublicRooms; i++ {
		roomName := testUsername(i + 20)
		roomID := registry.DerivePublicRoomID(roomName)
		if err := srv.registerPublicRoom(ctx, username, peerID, roomID, roomName); err != nil {
			t.Fatalf("registerPublicRoom #%d failed unexpectedly: %v", i+1, err)
		}
	}

	if err := srv.registerPublicRoom(ctx, username, peerID, registry.DerivePublicRoomID("overflow-room"), "overflow-room"); err == nil || err.Error() != "public_room_limit_reached" {
		t.Fatalf("expected public_room_limit_reached on fourth room, got %v", err)
	}
}
