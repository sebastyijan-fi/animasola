package keys_test

import (
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
)

func TestEd25519IdentityLifecycle(t *testing.T) {
	// 1. Setup isolated agent test environment
	username := "test_agent_alpha_" + time.Now().Format("150405")

	// Ensure cleanup
	defer func() {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			os.RemoveAll(filepath.Join(homeDir, ".config", "animasola", username))
		}
	}()

	// 2. Test HasKey on non-existent profile
	if keys.HasKey(username) {
		t.Fatalf("HasKey returned true for non-existent user %s", username)
	}

	// 3. Test New Key Generation
	k, err := keys.GetOrGenerateKey(username)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	if k == nil {
		t.Fatal("GetOrGenerateKey returned nil key")
	}
	if k.PrivateKey == nil || k.PublicKey == nil {
		t.Fatal("Generated key is missing internal cryptographic material")
	}

	peerID := k.Fingerprint()
	if peerID == "" {
		t.Fatal("Failed to extract PeerID from generated key")
	}

	// 4. Test Key Persistence (HasKey should now be true)
	if !keys.HasKey(username) {
		t.Fatalf("HasKey returned false after generation for user %s", username)
	}

	// 5. Test Key Retrieval (Should return same PeerID)
	k2, err := keys.GetOrGenerateKey(username)
	if err != nil {
		t.Fatalf("Failed to retrieve existing key: %v", err)
	}

	peerID2 := k2.Fingerprint()
	if peerID != peerID2 {
		t.Fatalf("Retrieved key PeerID (%s) does not match original (%s)", peerID2, peerID)
	}

	log.Printf("[SUCCESS] Identity keys correctly generated and matched for %s", peerID)
}
