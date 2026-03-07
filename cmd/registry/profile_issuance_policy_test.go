package main

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func ageIssuanceEvents(t *testing.T, db *sql.DB, source string, age time.Duration) {
	t.Helper()
	cutoff := time.Now().UTC().Add(-age).Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE profile_issuance_events SET issued_at = ? WHERE source = ?`, cutoff, source); err != nil {
		t.Fatalf("failed to age issuance events: %v", err)
	}
}

func TestRegisterProfileCooldownBlocksDeleteRecreateChurn(t *testing.T) {
	db := newTestRegistryDB(t)
	srv := &server{db: db}
	ctx := context.Background()
	source := "203.0.113.70"

	if err := srv.registerProfile(ctx, "alpha", testPeerID(1), source); err != nil {
		t.Fatalf("initial register failed: %v", err)
	}
	deleted, err := srv.releaseProfile(ctx, "alpha", testPeerID(1))
	if err != nil {
		t.Fatalf("releaseProfile failed: %v", err)
	}
	if !deleted {
		t.Fatalf("expected profile to be deleted")
	}
	if err := srv.registerProfile(ctx, "beta", testPeerID(2), source); err == nil || err.Error() != "source_profile_cooldown" {
		t.Fatalf("expected source_profile_cooldown after delete/recreate churn, got %v", err)
	}
}

func TestRegisterProfileLongWindowBlocksRepeatedDeleteRecreateChurn(t *testing.T) {
	db := newTestRegistryDB(t)
	srv := &server{db: db}
	ctx := context.Background()
	source := "203.0.113.71"

	for i := 0; i < maxProfileSuccessesPerWindow; i++ {
		username := testUsername(i + 100)
		peerID := testPeerID(i + 100)
		if err := srv.registerProfile(ctx, username, peerID, source); err != nil {
			t.Fatalf("registerProfile #%d failed unexpectedly: %v", i+1, err)
		}
		deleted, err := srv.releaseProfile(ctx, username, peerID)
		if err != nil {
			t.Fatalf("releaseProfile #%d failed: %v", i+1, err)
		}
		if !deleted {
			t.Fatalf("expected profile #%d to be deleted", i+1)
		}
		ageIssuanceEvents(t, db, source, profileSuccessCooldown+time.Minute)
	}

	if err := srv.registerProfile(ctx, "window-overflow", testPeerID(999), source); err == nil || err.Error() != "source_profile_window_limit_reached" {
		t.Fatalf("expected source_profile_window_limit_reached after %d successful issuances, got %v", maxProfileSuccessesPerWindow, err)
	}
}
