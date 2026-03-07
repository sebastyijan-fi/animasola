package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	peer "github.com/libp2p/go-libp2p/core/peer"
	registry "github.com/sebastyijan/animasola/capabilities/network.registry"
	_ "modernc.org/sqlite"
)

const (
	maxProfilesPerWindow          = 3
	maxRoomOpsPerWindow           = 10
	maxChallengesPerWindow        = 12
	maxActiveProfilesPerSource    = 5
	maxProfileSuccessesPerWindow  = 8
	rateLimitWindow               = time.Minute
	maxActivePublicRooms          = 3
	profileSuccessCooldown        = 30 * time.Minute
	profileSuccessWindow          = 7 * 24 * time.Hour
	readHeaderTimeout             = 5 * time.Second
	writeTimeout                  = 10 * time.Second
	idleTimeout                   = 30 * time.Second
	challengeTTL                  = 2 * time.Minute
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

type apiError struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

type profileRegisterRequest struct {
	Username  string `json:"username"`
	PeerID    string `json:"peer_id"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

type profileReleaseRequest struct {
	Username  string `json:"username"`
	PeerID    string `json:"peer_id"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

type publicRoomRegisterRequest struct {
	Username  string `json:"username"`
	PeerID    string `json:"peer_id"`
	RoomID    string `json:"room_id"`
	Name      string `json:"name"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

type challengeRequest struct {
	PeerID string `json:"peer_id"`
	Action string `json:"action"`
}

type challengeResponse struct {
	Nonce string `json:"nonce"`
}

type publicRoomReleaseRequest struct {
	PeerID    string `json:"peer_id"`
	RoomID    string `json:"room_id"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

type issuedChallenge struct {
	PeerID string
	Action string
	Expiry time.Time
}

type limiter struct {
	mu      sync.Mutex
	entries map[string][]time.Time
}

func newLimiter() *limiter {
	return &limiter{entries: make(map[string][]time.Time)}
}

func (l *limiter) allow(key string, limit int, window time.Duration) bool {
	now := time.Now().UTC()
	cutoff := now.Add(-window)

	l.mu.Lock()
	defer l.mu.Unlock()

	items := l.entries[key]
	filtered := items[:0]
	for _, ts := range items {
		if ts.After(cutoff) {
			filtered = append(filtered, ts)
		}
	}
	if len(filtered) >= limit {
		l.entries[key] = filtered
		return false
	}
	l.entries[key] = append(filtered, now)
	return true
}

type server struct {
	db               *sql.DB
	profileLimiter   *limiter
	publicOpsLimiter *limiter
	challengeLimiter *limiter
	challengeMu      sync.Mutex
	challenges       map[string]issuedChallenge
}

func main() {
	defaultListen := strings.TrimSpace(os.Getenv("ANIMASOLA_REGISTRY_ADDR"))
	if defaultListen == "" {
		defaultListen = "127.0.0.1:8787"
	}
	defaultDBPath := strings.TrimSpace(os.Getenv("ANIMASOLA_REGISTRY_DB"))
	if defaultDBPath == "" {
		defaultDBPath = "/srv/animasola/registry/registry.db"
	}

	listenAddr := flag.String("listen", defaultListen, "listen address")
	dbPath := flag.String("db", defaultDBPath, "sqlite database path")
	flag.Parse()

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatalf("open registry db: %v", err)
	}
	defer db.Close()

	if err := initDB(db); err != nil {
		log.Fatalf("init registry db: %v", err)
	}

	srv := &server{
		db:               db,
		profileLimiter:   newLimiter(),
		publicOpsLimiter: newLimiter(),
		challengeLimiter: newLimiter(),
		challenges:       make(map[string]issuedChallenge),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", srv.handleHealth)
	mux.HandleFunc("/v1/challenge", srv.handleChallenge)
	mux.HandleFunc("/v1/profiles/register", srv.handleRegisterProfile)
	mux.HandleFunc("/v1/profiles/validate", srv.handleValidateProfile)
	mux.HandleFunc("/v1/profiles/release", srv.handleReleaseProfile)
	mux.HandleFunc("/v1/public-rooms/register", srv.handleRegisterPublicRoom)
	mux.HandleFunc("/v1/public-rooms/validate", srv.handleValidatePublicRoom)
	mux.HandleFunc("/v1/public-rooms/release", srv.handleReleasePublicRoom)

	httpServer := &http.Server{
		Addr:              *listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	log.Printf("animasola registry listening on %s", *listenAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func initDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS profiles (
			username TEXT PRIMARY KEY,
			peer_id TEXT NOT NULL UNIQUE,
			source TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS security_counters (
			source TEXT NOT NULL,
			category TEXT NOT NULL,
			count INTEGER NOT NULL,
			first_seen_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			PRIMARY KEY (source, category)
		);
		CREATE TABLE IF NOT EXISTS profile_issuance_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			peer_id TEXT NOT NULL,
			username TEXT NOT NULL,
			issued_at TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`ALTER TABLE profiles ADD COLUMN source TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return err
	}
	if err := ensurePublicRoomsSchema(db); err != nil {
		return err
	}
	return nil
}

func ensurePublicRoomsSchema(db *sql.DB) error {
	var createSQL string
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'public_rooms'`).Scan(&createSQL)
	switch {
	case err == sql.ErrNoRows:
		_, err = db.Exec(`
			CREATE TABLE public_rooms (
				room_id TEXT PRIMARY KEY,
				peer_id TEXT NOT NULL,
				username TEXT NOT NULL,
				name TEXT NOT NULL,
				created_at TEXT NOT NULL
			)
		`)
		return err
	case err != nil:
		return err
	}

	if strings.Contains(createSQL, "peer_id TEXT NOT NULL UNIQUE") {
		if _, err := db.Exec(`DROP TABLE public_rooms`); err != nil {
			return err
		}
		_, err = db.Exec(`
			CREATE TABLE public_rooms (
				room_id TEXT PRIMARY KEY,
				peer_id TEXT NOT NULL,
				username TEXT NOT NULL,
				name TEXT NOT NULL,
				created_at TEXT NOT NULL
			)
		`)
		return err
	}
	return nil
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !s.challengeLimiter.allow(clientKey(r), maxChallengesPerWindow, rateLimitWindow) {
		s.recordSecurityCounter(r.Context(), clientKey(r), "challenge_rate_limited")
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "too many challenge requests")
		return
	}

	var req challengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "invalid request body")
		return
	}
	req.PeerID = strings.TrimSpace(req.PeerID)
	req.Action = strings.TrimSpace(req.Action)
	if _, err := peer.Decode(req.PeerID); err != nil {
		s.recordSecurityCounter(r.Context(), clientKey(r), "challenge_invalid_peer_id")
		writeJSONError(w, http.StatusBadRequest, "invalid_peer_id", "peer id is invalid")
		return
	}
	switch req.Action {
	case registry.ActionRegisterProfile, registry.ActionReleaseProfile, registry.ActionRegisterPublicRoom, registry.ActionReleasePublicRoom:
	default:
		s.recordSecurityCounter(r.Context(), clientKey(r), "challenge_invalid_action")
		writeJSONError(w, http.StatusBadRequest, "invalid_action", "action is invalid")
		return
	}

	nonce, err := issueNonce()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "failed to issue challenge")
		return
	}
	s.storeChallenge(nonce, req.PeerID, req.Action)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(challengeResponse{Nonce: nonce})
}

func (s *server) handleRegisterProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !s.profileLimiter.allow(clientKey(r), maxProfilesPerWindow, rateLimitWindow) {
		s.recordSecurityCounter(r.Context(), clientKey(r), "profile_rate_limited")
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "too many profile registration attempts")
		return
	}

	var req profileRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "invalid request body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.PeerID = strings.TrimSpace(req.PeerID)
	if !usernamePattern.MatchString(req.Username) {
		s.recordSecurityCounter(r.Context(), clientKey(r), "profile_invalid_username")
		writeJSONError(w, http.StatusBadRequest, "invalid_username", "profile name must be 3-32 characters using letters, numbers, underscore, or hyphen")
		return
	}
	if req.PeerID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_peer_id", "peer id is required")
		return
	}
	if _, err := peer.Decode(req.PeerID); err != nil {
		s.recordSecurityCounter(r.Context(), clientKey(r), "profile_invalid_peer_id")
		writeJSONError(w, http.StatusBadRequest, "invalid_peer_id", "peer id is invalid")
		return
	}
	if err := s.verifySignedRequest(req.PeerID, registry.ActionRegisterProfile, req.Nonce, req.Signature, []byte(fmt.Sprintf("%s\n%s\n%s\n%s", registry.ActionRegisterProfile, req.Username, req.PeerID, req.Nonce))); err != nil {
		s.recordSecurityCounter(r.Context(), clientKey(r), "profile_invalid_signature")
		writeJSONError(w, http.StatusUnauthorized, "invalid_signature", err.Error())
		return
	}

	ctx := r.Context()
	if err := s.registerProfile(ctx, req.Username, req.PeerID, clientKey(r)); err != nil {
		switch err.Error() {
		case "username_taken":
			s.recordSecurityCounter(r.Context(), clientKey(r), "profile_username_taken")
			writeJSONError(w, http.StatusConflict, "username_taken", "profile name is already taken")
		case "peer_already_registered":
			s.recordSecurityCounter(r.Context(), clientKey(r), "profile_peer_already_registered")
			writeJSONError(w, http.StatusConflict, "peer_already_registered", "this key is already bound to another profile name")
		case "source_profile_cooldown":
			s.recordSecurityCounter(r.Context(), clientKey(r), "profile_success_cooldown")
			writeJSONError(w, http.StatusTooManyRequests, "profile_cooldown", "please wait before creating another profile from this source")
		case "source_profile_window_limit_reached":
			s.recordSecurityCounter(r.Context(), clientKey(r), "profile_success_window_limit")
			writeJSONError(w, http.StatusTooManyRequests, "profile_limit_reached", "too many profiles have been created from this source recently")
		case "source_profile_limit_reached":
			s.recordSecurityCounter(r.Context(), clientKey(r), "profile_active_limit_reached")
			writeJSONError(w, http.StatusTooManyRequests, "profile_limit_reached", "too many active profiles are already registered from this source")
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "failed to register profile")
		}
		return
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"username": req.Username,
		"peer_id":  req.PeerID,
	})
}

func (s *server) handleReleaseProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var req profileReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "invalid request body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.PeerID = strings.TrimSpace(req.PeerID)
	if req.Username == "" || req.PeerID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "username and peer id are required")
		return
	}
	if _, err := peer.Decode(req.PeerID); err != nil {
		s.recordSecurityCounter(r.Context(), clientKey(r), "profile_release_invalid_peer_id")
		writeJSONError(w, http.StatusBadRequest, "invalid_peer_id", "peer id is invalid")
		return
	}
	if err := s.verifySignedRequest(req.PeerID, registry.ActionReleaseProfile, req.Nonce, req.Signature, []byte(fmt.Sprintf("%s\n%s\n%s\n%s", registry.ActionReleaseProfile, req.Username, req.PeerID, req.Nonce))); err != nil {
		s.recordSecurityCounter(r.Context(), clientKey(r), "profile_release_invalid_signature")
		writeJSONError(w, http.StatusUnauthorized, "invalid_signature", err.Error())
		return
	}

	deleted, err := s.releaseProfile(r.Context(), req.Username, req.PeerID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "failed to release profile")
		return
	}
	if !deleted {
		s.recordSecurityCounter(r.Context(), clientKey(r), "profile_release_not_found")
		writeJSONError(w, http.StatusNotFound, "not_found", "profile is not registered")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleValidateProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	username := strings.TrimSpace(r.URL.Query().Get("username"))
	peerID := strings.TrimSpace(r.URL.Query().Get("peer_id"))
	if username == "" || peerID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "username and peer_id are required")
		return
	}

	var exists int
	err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM profiles WHERE username = ? AND peer_id = ?`, username, peerID).Scan(&exists)
	if err == sql.ErrNoRows {
		writeJSONError(w, http.StatusNotFound, "not_found", "profile is not registered")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "failed to validate profile")
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"allowed": true,
	})
}

func (s *server) handleRegisterPublicRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !s.publicOpsLimiter.allow(clientKey(r), maxRoomOpsPerWindow, rateLimitWindow) {
		s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_rate_limited")
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "too many public room operations")
		return
	}

	var req publicRoomRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "invalid request body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.PeerID = strings.TrimSpace(req.PeerID)
	req.RoomID = strings.TrimSpace(req.RoomID)
	req.Name = strings.TrimSpace(req.Name)

	if !usernamePattern.MatchString(req.Username) {
		writeJSONError(w, http.StatusBadRequest, "invalid_username", "profile name must be 3-32 characters using letters, numbers, underscore, or hyphen")
		return
	}
	if req.PeerID == "" || req.RoomID == "" || req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "username, peer id, room id, and room name are required")
		return
	}
	if _, err := peer.Decode(req.PeerID); err != nil {
		s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_invalid_peer_id")
		writeJSONError(w, http.StatusBadRequest, "invalid_peer_id", "peer id is invalid")
		return
	}
	if !strings.HasPrefix(req.RoomID, "public-") {
		s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_invalid_room_id")
		writeJSONError(w, http.StatusBadRequest, "invalid_room_id", "public room id is invalid")
		return
	}
	expectedRoomID := registry.DerivePublicRoomID(req.Name)
	if req.RoomID != expectedRoomID {
		s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_invalid_room_id")
		writeJSONError(w, http.StatusBadRequest, "invalid_room_id", "public room id does not match the official room naming scheme")
		return
	}
	if err := s.verifySignedRequest(req.PeerID, registry.ActionRegisterPublicRoom, req.Nonce, req.Signature, []byte(fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s", registry.ActionRegisterPublicRoom, req.Username, req.PeerID, req.RoomID, req.Name, req.Nonce))); err != nil {
		s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_invalid_signature")
		writeJSONError(w, http.StatusUnauthorized, "invalid_signature", err.Error())
		return
	}

	ctx := r.Context()
	if err := s.registerPublicRoom(ctx, req.Username, req.PeerID, req.RoomID, req.Name); err != nil {
		switch err.Error() {
		case "profile_not_registered":
			s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_profile_not_registered")
			writeJSONError(w, http.StatusForbidden, "profile_not_registered", "profile must be registered before creating a public room")
		case "public_room_limit_reached":
			s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_limit_reached")
			writeJSONError(w, http.StatusConflict, "public_room_limit_reached", "only three active public rooms are allowed per profile right now")
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "failed to register public room")
		}
		return
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"room_id": req.RoomID,
	})
}

func (s *server) handleValidatePublicRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	creatorID := strings.TrimSpace(r.URL.Query().Get("creator_id"))
	roomID := strings.TrimSpace(r.URL.Query().Get("room_id"))
	if creatorID == "" || roomID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "creator_id and room_id are required")
		return
	}

	var exists int
	err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM public_rooms WHERE room_id = ? AND peer_id = ?`, roomID, creatorID).Scan(&exists)
	if err == sql.ErrNoRows {
		writeJSONError(w, http.StatusNotFound, "not_found", "public room is not registered")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "failed to validate public room")
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"allowed": true,
	})
}

func (s *server) handleReleasePublicRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var req publicRoomReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "invalid request body")
		return
	}
	req.PeerID = strings.TrimSpace(req.PeerID)
	req.RoomID = strings.TrimSpace(req.RoomID)
	if req.PeerID == "" || req.RoomID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "peer_id and room_id are required")
		return
	}
	if _, err := peer.Decode(req.PeerID); err != nil {
		s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_release_invalid_peer_id")
		writeJSONError(w, http.StatusBadRequest, "invalid_peer_id", "peer id is invalid")
		return
	}
	if err := s.verifySignedRequest(req.PeerID, registry.ActionReleasePublicRoom, req.Nonce, req.Signature, []byte(fmt.Sprintf("%s\n%s\n%s\n%s", registry.ActionReleasePublicRoom, req.PeerID, req.RoomID, req.Nonce))); err != nil {
		s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_release_invalid_signature")
		writeJSONError(w, http.StatusUnauthorized, "invalid_signature", err.Error())
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM public_rooms WHERE room_id = ? AND peer_id = ?`, req.RoomID, req.PeerID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "failed to delete public room")
		return
	}
	rowsAffected, err := res.RowsAffected()
	if err == nil && rowsAffected == 0 {
		s.recordSecurityCounter(r.Context(), clientKey(r), "public_room_release_not_found")
		writeJSONError(w, http.StatusNotFound, "not_found", "public room is not registered")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) registerProfile(ctx context.Context, username, peerID, source string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existingPeer string
	err = tx.QueryRowContext(ctx, `SELECT peer_id FROM profiles WHERE username = ?`, username).Scan(&existingPeer)
	switch {
	case err == nil:
		if existingPeer != peerID {
			return fmt.Errorf("username_taken")
		}
		return tx.Commit()
	case err != sql.ErrNoRows:
		return err
	}

	var existingUsername string
	err = tx.QueryRowContext(ctx, `SELECT username FROM profiles WHERE peer_id = ?`, peerID).Scan(&existingUsername)
	switch {
	case err == nil:
		if existingUsername != username {
			return fmt.Errorf("peer_already_registered")
		}
		return tx.Commit()
	case err != sql.ErrNoRows:
		return err
	}

	var activeProfiles int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM profiles WHERE source = ?`, source).Scan(&activeProfiles); err != nil {
		return err
	}
	if activeProfiles >= maxActiveProfilesPerSource {
		return fmt.Errorf("source_profile_limit_reached")
	}

	cooldownCutoff := time.Now().UTC().Add(-profileSuccessCooldown).Format(time.RFC3339Nano)
	var recentSuccesses int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM profile_issuance_events WHERE source = ? AND issued_at >= ?`, source, cooldownCutoff).Scan(&recentSuccesses); err != nil {
		return err
	}
	if recentSuccesses > 0 {
		return fmt.Errorf("source_profile_cooldown")
	}

	windowCutoff := time.Now().UTC().Add(-profileSuccessWindow).Format(time.RFC3339Nano)
	var windowSuccesses int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM profile_issuance_events WHERE source = ? AND issued_at >= ?`, source, windowCutoff).Scan(&windowSuccesses); err != nil {
		return err
	}
	if windowSuccesses >= maxProfileSuccessesPerWindow {
		return fmt.Errorf("source_profile_window_limit_reached")
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO profiles (username, peer_id, source, created_at) VALUES (?, ?, ?, ?)`, username, peerID, source, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_issuance_events (source, peer_id, username, issued_at) VALUES (?, ?, ?, ?)`, source, peerID, username, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *server) releaseProfile(ctx context.Context, username, peerID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM public_rooms WHERE peer_id = ?`, peerID); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM profiles WHERE username = ? AND peer_id = ?`, username, peerID)
	if err != nil {
		return false, err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if rowsAffected == 0 {
		return false, tx.Commit()
	}
	return true, tx.Commit()
}

func (s *server) registerPublicRoom(ctx context.Context, username, peerID, roomID, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var profilePeerID string
	err = tx.QueryRowContext(ctx, `SELECT peer_id FROM profiles WHERE username = ?`, username).Scan(&profilePeerID)
	if err == sql.ErrNoRows || profilePeerID != peerID {
		return fmt.Errorf("profile_not_registered")
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	var existingPeer string
	err = tx.QueryRowContext(ctx, `SELECT peer_id FROM public_rooms WHERE room_id = ?`, roomID).Scan(&existingPeer)
	switch {
	case err == nil:
		if existingPeer != peerID {
			return fmt.Errorf("public_room_limit_reached")
		}
		return tx.Commit()
	case err != sql.ErrNoRows:
		return err
	}

	var currentCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM public_rooms WHERE peer_id = ?`, peerID).Scan(&currentCount); err != nil {
		return err
	}
	if currentCount >= maxActivePublicRooms {
		return fmt.Errorf("public_room_limit_reached")
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO public_rooms (room_id, peer_id, username, name, created_at) VALUES (?, ?, ?, ?, ?)`, roomID, peerID, username, name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func clientKey(r *http.Request) string {
	host := r.RemoteAddr
	if parsedHost, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = parsedHost
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if forwarded := firstForwardedFor(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			return forwarded
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-Ip")); realIP != "" {
			if parsed := net.ParseIP(realIP); parsed != nil {
				return parsed.String()
			}
		}
	}
	return host
}

func firstForwardedFor(header string) string {
	for _, part := range strings.Split(header, ",") {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			continue
		}
		if parsed := net.ParseIP(candidate); parsed != nil {
			return parsed.String()
		}
	}
	return ""
}

func issueNonce() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

func (s *server) storeChallenge(nonce, peerID, action string) {
	s.challengeMu.Lock()
	defer s.challengeMu.Unlock()
	s.pruneExpiredChallengesLocked()
	s.challenges[nonce] = issuedChallenge{
		PeerID: peerID,
		Action: action,
		Expiry: time.Now().UTC().Add(challengeTTL),
	}
}

func (s *server) consumeChallenge(nonce, peerID, action string) error {
	s.challengeMu.Lock()
	defer s.challengeMu.Unlock()
	s.pruneExpiredChallengesLocked()
	challenge, ok := s.challenges[nonce]
	if !ok {
		return fmt.Errorf("challenge is missing or expired")
	}
	delete(s.challenges, nonce)
	if challenge.PeerID != peerID || challenge.Action != action {
		return fmt.Errorf("challenge does not match request")
	}
	return nil
}

func (s *server) pruneExpiredChallengesLocked() {
	now := time.Now().UTC()
	for nonce, challenge := range s.challenges {
		if now.After(challenge.Expiry) {
			delete(s.challenges, nonce)
		}
	}
}

func (s *server) verifySignedRequest(peerID, action, nonce, signature string, payload []byte) error {
	if nonce == "" || signature == "" {
		return fmt.Errorf("missing challenge signature")
	}
	if err := s.consumeChallenge(nonce, peerID, action); err != nil {
		return err
	}
	decodedPeerID, err := peer.Decode(peerID)
	if err != nil {
		return fmt.Errorf("peer id is invalid")
	}
	pubKey, err := decodedPeerID.ExtractPublicKey()
	if err != nil {
		return fmt.Errorf("failed to extract peer public key")
	}
	rawPub, err := pubKey.Raw()
	if err != nil {
		return fmt.Errorf("failed to decode peer public key")
	}
	decodedSig, err := registry.DecodeSignature(signature)
	if err != nil {
		return fmt.Errorf("invalid signature encoding")
	}
	if !ed25519.Verify(ed25519.PublicKey(rawPub), payload, decodedSig) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

func (s *server) recordSecurityCounter(ctx context.Context, source, category string) {
	if s.db == nil || source == "" || category == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO security_counters (source, category, count, first_seen_at, last_seen_at)
		VALUES (?, ?, 1, ?, ?)
		ON CONFLICT(source, category) DO UPDATE SET
			count = count + 1,
			last_seen_at = excluded.last_seen_at
	`, source, category, now, now)
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{
		Code:  code,
		Error: msg,
	})
}
