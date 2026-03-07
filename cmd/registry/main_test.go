package main

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientKeyUsesForwardedForBehindLoopbackProxy(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/challenge", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 127.0.0.1")

	got := clientKey(req)
	if got != "203.0.113.10" {
		t.Fatalf("expected forwarded client ip, got %q", got)
	}
}

func TestClientKeyIgnoresForwardedForWhenNotBehindLocalProxy(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/challenge", nil)
	req.RemoteAddr = "198.51.100.25:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")

	got := clientKey(req)
	if got != "198.51.100.25" {
		t.Fatalf("expected direct remote addr, got %q", got)
	}
}

func TestHandleChallengeRateLimitsPerClientKey(t *testing.T) {
	srv := &server{
		challengeLimiter: newLimiter(),
		challenges:       make(map[string]issuedChallenge),
	}

	for i := 0; i < maxChallengesPerWindow; i++ {
		req := httptest.NewRequest("POST", "/v1/challenge", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", "203.0.113.99")
		req.Body = newJSONBody(`{"peer_id":"12D3KooWA1M5qzv5UMsN6QVLq3g8A7EjRkZxq2FhAX5s8d9oYx9T","action":"register_profile"}`)
		rr := httptest.NewRecorder()
		srv.handleChallenge(rr, req)
		if rr.Code != 200 {
			t.Fatalf("request %d expected 200, got %d body=%s", i+1, rr.Code, rr.Body.String())
		}
	}

	req := httptest.NewRequest("POST", "/v1/challenge", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	req.Body = newJSONBody(`{"peer_id":"12D3KooWA1M5qzv5UMsN6QVLq3g8A7EjRkZxq2FhAX5s8d9oYx9T","action":"register_profile"}`)
	rr := httptest.NewRecorder()
	srv.handleChallenge(rr, req)
	if rr.Code != 429 {
		t.Fatalf("expected 429 after challenge limit, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRegisterProfileLimitsActiveProfilesPerSource(t *testing.T) {
	db := newTestRegistryDB(t)
	srv := &server{db: db}
	source := "203.0.113.9"

	for i := 0; i < maxActiveProfilesPerSource; i++ {
		err := srv.registerProfile(context.Background(), testUsername(i), testPeerID(i), source)
		if err != nil {
			t.Fatalf("registration %d failed unexpectedly: %v", i+1, err)
		}
		ageIssuanceEvents(t, db, source, profileSuccessCooldown+time.Minute)
	}

	err := srv.registerProfile(context.Background(), testUsername(99), testPeerID(99), source)
	if err == nil || err.Error() != "source_profile_limit_reached" {
		t.Fatalf("expected source_profile_limit_reached, got %v", err)
	}
}

func TestRegisterProfileIdempotentRetryDoesNotConsumeExtraSourceSlot(t *testing.T) {
	db := newTestRegistryDB(t)
	srv := &server{db: db}
	source := "203.0.113.11"

	if err := srv.registerProfile(context.Background(), "alice", testPeerID(1), source); err != nil {
		t.Fatalf("initial register failed: %v", err)
	}
	if err := srv.registerProfile(context.Background(), "alice", testPeerID(1), source); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	ageIssuanceEvents(t, db, source, profileSuccessCooldown+time.Minute)
	for i := 0; i < maxActiveProfilesPerSource-1; i++ {
		err := srv.registerProfile(context.Background(), testUsername(i+10), testPeerID(i+10), source)
		if err != nil {
			t.Fatalf("follow-up registration %d failed unexpectedly: %v", i+1, err)
		}
		ageIssuanceEvents(t, db, source, profileSuccessCooldown+time.Minute)
	}

	err := srv.registerProfile(context.Background(), "finalslot", testPeerID(42), source)
	if err == nil || err.Error() != "source_profile_limit_reached" {
		t.Fatalf("expected source_profile_limit_reached after %d real registrations, got %v", maxActiveProfilesPerSource, err)
	}
}

func TestReleaseProfileFreesActiveProfileSlot(t *testing.T) {
	db := newTestRegistryDB(t)
	srv := &server{db: db}
	source := "203.0.113.15"

	for i := 0; i < maxActiveProfilesPerSource; i++ {
		err := srv.registerProfile(context.Background(), testUsername(i), testPeerID(i), source)
		if err != nil {
			t.Fatalf("registration %d failed unexpectedly: %v", i+1, err)
		}
		ageIssuanceEvents(t, db, source, profileSuccessCooldown+time.Minute)
	}

	deleted, err := srv.releaseProfile(context.Background(), testUsername(2), testPeerID(2))
	if err != nil {
		t.Fatalf("releaseProfile failed: %v", err)
	}
	if !deleted {
		t.Fatalf("expected profile to be deleted")
	}

	if err := srv.registerProfile(context.Background(), "replacement", testPeerID(99), source); err != nil {
		t.Fatalf("expected freed slot to allow replacement profile, got %v", err)
	}
}

func TestHandleChallengeRateLimitRecordsSecurityCounter(t *testing.T) {
	db := newTestRegistryDB(t)
	srv := &server{
		db:               db,
		challengeLimiter: newLimiter(),
		challenges:       make(map[string]issuedChallenge),
	}

	source := "203.0.113.44"
	for i := 0; i < maxChallengesPerWindow; i++ {
		req := httptest.NewRequest("POST", "/v1/challenge", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", source)
		req.Body = newJSONBody(`{"peer_id":"12D3KooWA1M5qzv5UMsN6QVLq3g8A7EjRkZxq2FhAX5s8d9oYx9T","action":"register_profile"}`)
		rr := httptest.NewRecorder()
		srv.handleChallenge(rr, req)
	}

	req := httptest.NewRequest("POST", "/v1/challenge", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", source)
	req.Body = newJSONBody(`{"peer_id":"12D3KooWA1M5qzv5UMsN6QVLq3g8A7EjRkZxq2FhAX5s8d9oYx9T","action":"register_profile"}`)
	rr := httptest.NewRecorder()
	srv.handleChallenge(rr, req)
	if rr.Code != 429 {
		t.Fatalf("expected 429, got %d", rr.Code)
	}

	if got := securityCounterCount(t, db, source, "challenge_rate_limited"); got != 1 {
		t.Fatalf("expected one challenge_rate_limited counter, got %d", got)
	}
}
