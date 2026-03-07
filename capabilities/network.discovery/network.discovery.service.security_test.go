package discovery

import (
	"testing"
	"time"
)

func TestShouldRespondToSnapshotRequestRateLimitsGlobalAndPerPeer(t *testing.T) {
	service := NewService(nil, nil, nil, nil)
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)

	if !service.shouldRespondToSnapshotRequest("peer-a", now) {
		t.Fatalf("expected first snapshot request to be allowed")
	}
	if service.shouldRespondToSnapshotRequest("peer-b", now.Add(5*time.Second)) {
		t.Fatalf("expected global snapshot rate limit to reject rapid second response")
	}
	if !service.shouldRespondToSnapshotRequest("peer-b", now.Add(20*time.Second)) {
		t.Fatalf("expected different peer to be allowed after global cooldown")
	}
	if service.shouldRespondToSnapshotRequest("peer-b", now.Add(40*time.Second)) {
		t.Fatalf("expected per-peer cooldown to reject repeated peer response")
	}
	if !service.shouldRespondToSnapshotRequest("peer-a", now.Add(3*time.Minute)) {
		t.Fatalf("expected peer cooldown to expire")
	}
}
