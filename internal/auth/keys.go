package auth

import (
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

var (
	// 3-20 chars total; must start with a letter; lowercase letters, digits, hyphen, underscore.
	usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,19}$`)
	reserved   = map[string]struct{}{
		"admin":     {},
		"system":    {},
		"animasola": {},
		"register":  {},
		"help":      {},
		"mod":       {},
		"moderator": {},
	}
)

func Fingerprint(key ssh.PublicKey) string {
	// Stable, readable fingerprint for identity.
	return ssh.FingerprintSHA256(key)
}

func ValidateUsername(s string) error {
	s = strings.TrimSpace(s)
	if len(s) < 3 {
		return errors.New("Username too short (min 3)")
	}
	if len(s) > 20 {
		return errors.New("Username too long (max 20)")
	}
	if !usernameRe.MatchString(s) {
		return errors.New("Invalid username (use lowercase letters, numbers, hyphens, underscores; start with a letter)")
	}
	if _, ok := reserved[s]; ok {
		return errors.New("Reserved username")
	}
	return nil
}
