package registry

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
)

const (
	ActionRegisterProfile    = "register_profile"
	ActionReleaseProfile     = "release_profile"
	ActionRegisterPublicRoom = "register_public_room"
	ActionReleasePublicRoom  = "release_public_room"
)

type challengeRequest struct {
	PeerID string `json:"peer_id"`
	Action string `json:"action"`
}

type challengeResponse struct {
	Nonce string `json:"nonce"`
}

func DerivePublicRoomID(name string) string {
	hasher := sha256.New()
	hasher.Write([]byte(strings.ToLower(strings.TrimSpace(name))))
	return "public-" + hex.EncodeToString(hasher.Sum(nil))[:24]
}

func SignProfileRegistration(identity *keys.Keys, username, peerID, nonce string) string {
	return identity.Sign([]byte(fmt.Sprintf("%s\n%s\n%s\n%s", ActionRegisterProfile, username, peerID, nonce)))
}

func SignProfileRelease(identity *keys.Keys, username, peerID, nonce string) string {
	return identity.Sign([]byte(fmt.Sprintf("%s\n%s\n%s\n%s", ActionReleaseProfile, username, peerID, nonce)))
}

func SignPublicRoomRegistration(identity *keys.Keys, username, peerID, roomID, roomName, nonce string) string {
	return identity.Sign([]byte(fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s", ActionRegisterPublicRoom, username, peerID, roomID, roomName, nonce)))
}

func SignPublicRoomRelease(identity *keys.Keys, peerID, roomID, nonce string) string {
	return identity.Sign([]byte(fmt.Sprintf("%s\n%s\n%s\n%s", ActionReleasePublicRoom, peerID, roomID, nonce)))
}

func DecodeSignature(signature string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(signature)
}
