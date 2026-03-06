package sqlite

import (
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const derivedRoomKeyPrefix = "argon2:"

// EncodePrivateRoomKey stores a derived room key instead of the raw password.
func EncodePrivateRoomKey(roomID, password string) string {
	if password == "" {
		return ""
	}

	key := argon2.IDKey([]byte(password), roomKeySalt(roomID), 1, 64*1024, 4, 32)
	return derivedRoomKeyPrefix + hex.EncodeToString(key)
}

// DecodeOrDerivePrivateRoomKey accepts either a stored derived key or a raw password.
func DecodeOrDerivePrivateRoomKey(roomID, credential string) ([]byte, error) {
	if credential == "" {
		return nil, nil
	}

	if strings.HasPrefix(credential, derivedRoomKeyPrefix) {
		decoded, err := hex.DecodeString(strings.TrimPrefix(credential, derivedRoomKeyPrefix))
		if err != nil {
			return nil, fmt.Errorf("decode stored room key: %w", err)
		}
		return decoded, nil
	}

	return argon2.IDKey([]byte(credential), roomKeySalt(roomID), 1, 64*1024, 4, 32), nil
}

func roomKeySalt(roomID string) []byte {
	return []byte("animasola-room:" + roomID)
}
