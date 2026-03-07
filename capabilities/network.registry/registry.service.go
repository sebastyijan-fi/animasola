package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	keys "github.com/sebastyijan/animasola/capabilities/identity.keys"
)

const (
	defaultBaseURL      = "https://animasola.org"
	publicRoomCacheTTL  = 5 * time.Minute
	publicRoomMissTTL   = 1 * time.Minute
	requestTimeout      = 10 * time.Second
)

var (
	ErrUsernameTaken         = errors.New("profile name is already taken")
	ErrPeerAlreadyRegistered = errors.New("this device identity is already registered to another profile name")
	ErrProfileNotRegistered  = errors.New("profile is not registered")
	ErrPublicRoomLimit       = errors.New("public room limit reached")
	ErrRegistryUnavailable   = errors.New("animasola registry unavailable")
)

type cacheEntry struct {
	allowed bool
	until   time.Time
}

type Client struct {
	baseURL    string
	httpClient *http.Client

	mu              sync.Mutex
	profileCache    map[string]cacheEntry
	publicRoomCache map[string]cacheEntry
}

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

type publicRoomReleaseRequest struct {
	PeerID    string `json:"peer_id"`
	RoomID    string `json:"room_id"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

func NewClient() *Client {
	baseURL := strings.TrimSpace(os.Getenv("ANIMASOLA_REGISTRY_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
		profileCache:    make(map[string]cacheEntry),
		publicRoomCache: make(map[string]cacheEntry),
	}
}

func (c *Client) RegisterProfile(ctx context.Context, identity *keys.Keys, username, peerID string) error {
	nonce, err := c.fetchChallenge(ctx, peerID, ActionRegisterProfile)
	if err != nil {
		return err
	}
	reqBody := profileRegisterRequest{
		Username:  username,
		PeerID:    peerID,
		Nonce:     nonce,
		Signature: SignProfileRegistration(identity, username, peerID, nonce),
	}
	return c.postJSON(ctx, http.MethodPost, "/v1/profiles/register", reqBody, mapProfileError)
}

func (c *Client) ReleaseProfile(ctx context.Context, identity *keys.Keys, username, peerID string) error {
	nonce, err := c.fetchChallenge(ctx, peerID, ActionReleaseProfile)
	if err != nil {
		return err
	}
	reqBody := profileReleaseRequest{
		Username:  username,
		PeerID:    peerID,
		Nonce:     nonce,
		Signature: SignProfileRelease(identity, username, peerID, nonce),
	}
	return c.postJSON(ctx, http.MethodPost, "/v1/profiles/release", reqBody, mapProfileReleaseError)
}

func (c *Client) RegisterPublicRoom(ctx context.Context, identity *keys.Keys, username, peerID, roomID, name string) error {
	nonce, err := c.fetchChallenge(ctx, peerID, ActionRegisterPublicRoom)
	if err != nil {
		return err
	}
	reqBody := publicRoomRegisterRequest{
		Username:  username,
		PeerID:    peerID,
		RoomID:    roomID,
		Name:      name,
		Nonce:     nonce,
		Signature: SignPublicRoomRegistration(identity, username, peerID, roomID, name, nonce),
	}
	err = c.postJSON(ctx, http.MethodPost, "/v1/public-rooms/register", reqBody, mapPublicRoomError)
	if err == nil {
		c.cacheValidation(peerID, roomID, true, publicRoomCacheTTL)
	}
	return err
}

func (c *Client) ReleasePublicRoom(ctx context.Context, identity *keys.Keys, peerID, roomID string) error {
	nonce, err := c.fetchChallenge(ctx, peerID, ActionReleasePublicRoom)
	if err != nil {
		return err
	}
	reqBody := publicRoomReleaseRequest{
		PeerID:    peerID,
		RoomID:    roomID,
		Nonce:     nonce,
		Signature: SignPublicRoomRelease(identity, peerID, roomID, nonce),
	}
	if err := c.postJSON(ctx, http.MethodPost, "/v1/public-rooms/release", reqBody, nil); err != nil {
		return err
	}
	c.invalidateValidation(peerID, roomID)
	return nil
}

func (c *Client) ValidatePublicRoom(ctx context.Context, creatorID, roomID string) (bool, error) {
	if allowed, ok := c.cachedValidation(creatorID, roomID); ok {
		return allowed, nil
	}

	values := url.Values{}
	values.Set("creator_id", creatorID)
	values.Set("room_id", roomID)
	endpoint := c.baseURL + "/v1/public-rooms/validate?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrRegistryUnavailable, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrRegistryUnavailable, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		c.cacheValidation(creatorID, roomID, true, publicRoomCacheTTL)
		return true, nil
	case http.StatusNotFound:
		c.cacheValidation(creatorID, roomID, false, publicRoomMissTTL)
		return false, nil
	default:
		return false, decodeAPIError(resp, nil)
	}
}

func (c *Client) ValidateProfile(ctx context.Context, username, peerID string) (bool, error) {
	cacheKey := profileCacheKey(username, peerID)
	if allowed, ok := c.cachedProfileValidation(cacheKey); ok {
		return allowed, nil
	}

	values := url.Values{}
	values.Set("username", username)
	values.Set("peer_id", peerID)
	endpoint := c.baseURL + "/v1/profiles/validate?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrRegistryUnavailable, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrRegistryUnavailable, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		c.cacheProfileValidation(cacheKey, true, publicRoomCacheTTL)
		return true, nil
	case http.StatusNotFound:
		c.cacheProfileValidation(cacheKey, false, publicRoomMissTTL)
		return false, nil
	default:
		return false, decodeAPIError(resp, nil)
	}
}

func (c *Client) postJSON(ctx context.Context, method, path string, payload any, mapErr func(apiError) error) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRegistryUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRegistryUnavailable, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	default:
		return decodeAPIError(resp, mapErr)
	}
}

func (c *Client) fetchChallenge(ctx context.Context, peerID, action string) (string, error) {
	reqBody := challengeRequest{
		PeerID: peerID,
		Action: action,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/challenge", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRegistryUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRegistryUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", decodeAPIError(resp, nil)
	}

	var challenge challengeResponse
	if err := json.NewDecoder(resp.Body).Decode(&challenge); err != nil {
		return "", fmt.Errorf("%w: invalid challenge response", ErrRegistryUnavailable)
	}
	if challenge.Nonce == "" {
		return "", fmt.Errorf("%w: empty challenge nonce", ErrRegistryUnavailable)
	}
	return challenge.Nonce, nil
}

func decodeAPIError(resp *http.Response, mapErr func(apiError) error) error {
	var apiErr apiError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err == nil {
		if mapErr != nil {
			if mapped := mapErr(apiErr); mapped != nil {
				return mapped
			}
		}
		if apiErr.Error != "" {
			return fmt.Errorf("%w: %s", ErrRegistryUnavailable, apiErr.Error)
		}
	}
	return fmt.Errorf("%w: status %d", ErrRegistryUnavailable, resp.StatusCode)
}

func mapProfileError(apiErr apiError) error {
	switch apiErr.Code {
	case "username_taken":
		return fmt.Errorf("%w: %s", ErrUsernameTaken, apiErr.Error)
	case "peer_already_registered":
		return fmt.Errorf("%w: %s", ErrPeerAlreadyRegistered, apiErr.Error)
	default:
		return nil
	}
}

func mapProfileReleaseError(apiErr apiError) error {
	switch apiErr.Code {
	case "not_found":
		return fmt.Errorf("%w: %s", ErrProfileNotRegistered, apiErr.Error)
	default:
		return nil
	}
}

func mapPublicRoomError(apiErr apiError) error {
	switch apiErr.Code {
	case "public_room_limit_reached":
		return fmt.Errorf("%w: %s", ErrPublicRoomLimit, apiErr.Error)
	default:
		return nil
	}
}

func (c *Client) cachedValidation(creatorID, roomID string) (bool, bool) {
	key := creatorID + "|" + roomID
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.publicRoomCache[key]
	if !ok {
		return false, false
	}
	if time.Now().UTC().After(entry.until) {
		delete(c.publicRoomCache, key)
		return false, false
	}
	return entry.allowed, true
}

func (c *Client) cachedProfileValidation(cacheKey string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.profileCache[cacheKey]
	if !ok {
		return false, false
	}
	if time.Now().UTC().After(entry.until) {
		delete(c.profileCache, cacheKey)
		return false, false
	}
	return entry.allowed, true
}

func (c *Client) cacheProfileValidation(cacheKey string, allowed bool, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.profileCache[cacheKey] = cacheEntry{
		allowed: allowed,
		until:   time.Now().UTC().Add(ttl),
	}
}

func (c *Client) cacheValidation(creatorID, roomID string, allowed bool, ttl time.Duration) {
	key := creatorID + "|" + roomID
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publicRoomCache[key] = cacheEntry{
		allowed: allowed,
		until:   time.Now().UTC().Add(ttl),
	}
}

func (c *Client) invalidateValidation(creatorID, roomID string) {
	key := creatorID + "|" + roomID
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.publicRoomCache, key)
}

func profileCacheKey(username, peerID string) string {
	return username + "\x00" + peerID
}
