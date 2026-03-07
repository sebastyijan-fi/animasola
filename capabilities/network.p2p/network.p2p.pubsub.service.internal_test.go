package p2p

import (
	"testing"
	"time"
)

func TestInboundMessageLimiterBlocksBurstFromSingleAuthor(t *testing.T) {
	limiter := newInboundMessageLimiter()
	now := time.Now().UTC()

	for i := 0; i < inboundMessageBurstLimit; i++ {
		if !limiter.allow("peer-a", now) {
			t.Fatalf("message %d should have been allowed", i+1)
		}
	}
	if limiter.allow("peer-a", now) {
		t.Fatalf("expected burst limit to block next message")
	}
	if !limiter.allow("peer-b", now) {
		t.Fatalf("different author should not be blocked")
	}
}

func TestInboundMessageLimiterSupportsSeparateAggregateLimit(t *testing.T) {
	limiter := newInboundMessageLimiter()
	now := time.Now().UTC()

	for i := 0; i < inboundRoomBurstLimit; i++ {
		if !limiter.allowWithLimit("room-a", now, inboundRoomBurstLimit) {
			t.Fatalf("room burst item %d should have been allowed", i+1)
		}
	}
	if limiter.allowWithLimit("room-a", now, inboundRoomBurstLimit) {
		t.Fatalf("expected room aggregate burst limit to block next message")
	}
}
