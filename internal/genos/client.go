// Package genos is an HTTP client for the Genos customer API.
// It mints a user-scoped Discord impersonation token. That token is not a
// cluster credential, and this package never reads kubeconfig or a Kubernetes token.
package genos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const impersonationPath = "/api/v1/internal/discord/impersonation-tokens"

// ErrNoInteractiveChannel means the server has no interactive runtime channel.
var ErrNoInteractiveChannel = errors.New("no interactive runtime channel")

// Server is the customer server summary this bot reads.
type Server struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Game           Game     `json:"game"`
	Status         string   `json:"status"`
	NotableUpdates []string `json:"notableUpdates"`
	Metrics        Metrics  `json:"metrics"`
}

// Game is the nested game object.
type Game struct {
	Name string `json:"name"`
}

// Metrics is the nested metrics object. A missing player count means zero.
type Metrics struct {
	PlayerCount *int64 `json:"playerCount"`
}

// Channel is one runtime channel. Interaction "interactive" accepts console text.
type Channel struct {
	ID          string `json:"id"`
	Label       string `json:"label,omitempty"`
	Interaction string `json:"interaction"`
}

// PlayerCount returns a non-negative player total. Missing counts are zero.
func (s Server) PlayerCount() int64 {
	if s.Metrics.PlayerCount == nil || *s.Metrics.PlayerCount < 0 {
		return 0
	}
	return *s.Metrics.PlayerCount
}

// Notable returns trimmed notable update strings. Blank entries are ignored.
func (s Server) Notable() []string {
	out := make([]string, 0, len(s.NotableUpdates))
	for _, item := range s.NotableUpdates {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// Client calls Genos with a cached per-user impersonation token.
type Client struct {
	Origin    string
	BotSecret string
	HTTP      *http.Client
	Now       func() time.Time
	NewKey    func() string

	mu    sync.Mutex
	cache map[string]cachedToken
}

type cachedToken struct {
	value   string
	expires time.Time
}

type mintResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	TokenKind string    `json:"tokenKind"`
}

type actionRequest struct {
	Type                       string `json:"type"`
	ConfirmUnsavedProgressLoss bool   `json:"confirmUnsavedProgressLoss"`
}

type consoleRequest struct {
	Text string `json:"text"`
}

var (
	mintedTokenPattern = regexp.MustCompile(`genos_discord_[A-Za-z0-9_\-]+`)
	bearerPattern      = regexp.MustCompile(`(?i)bearer\s+\S+`)
)

// NewClient builds a client. httpClient may be nil. Redirects are disabled so
// the bearer token is not forwarded to another host.
func NewClient(origin, botSecret string, httpClient *http.Client) *Client {
	return &Client{
		Origin:    strings.TrimRight(strings.TrimSpace(origin), "/"),
		BotSecret: strings.TrimSpace(botSecret),
		HTTP:      normalize(httpClient),
		cache:     map[string]cachedToken{},
	}
}

// ListServers returns the caller's current servers. The API does not include past servers.
func (c *Client) ListServers(ctx context.Context, discordUserID string) ([]Server, error) {
	body, status, err := c.doUser(ctx, discordUserID, http.MethodGet, "/api/v1/servers", nil, "")
	if err != nil {
		return nil, err
	}
	if !statusOK(status) {
		return nil, c.statusError(http.MethodGet, "/api/v1/servers", status, body)
	}
	var payload struct {
		Servers []Server `json:"servers"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("genos list servers: %w", err)
	}
	if payload.Servers == nil {
		payload.Servers = []Server{}
	}
	return payload.Servers, nil
}

// GetServer loads one server. It accepts {"server":{...}} and a bare server object.
func (c *Client) GetServer(ctx context.Context, discordUserID, serverID string) (Server, error) {
	path := serverPath(serverID)
	body, status, err := c.doUser(ctx, discordUserID, http.MethodGet, path, nil, "")
	if err != nil {
		return Server{}, err
	}
	if !statusOK(status) {
		return Server{}, c.statusError(http.MethodGet, path, status, body)
	}
	server, err := parseServer(body)
	if err != nil {
		return Server{}, fmt.Errorf("genos get server: %w", err)
	}
	return server, nil
}

// RequestAction posts a lifecycle action. confirmUnsavedProgressLoss is sent on every call.
// The idempotency key is a UUID. The bot secret is not used on this request.
func (c *Client) RequestAction(ctx context.Context, discordUserID, serverID, actionType string, confirmUnsavedProgressLoss bool) error {
	switch actionType {
	case "start", "stop", "restart", "force-stop":
	default:
		return fmt.Errorf("unsupported action %q", actionType)
	}
	if strings.TrimSpace(serverID) == "" || strings.TrimSpace(discordUserID) == "" {
		return errors.New("missing server or discord user")
	}
	path := serverPath(serverID) + "/actions"
	payload := actionRequest{Type: actionType, ConfirmUnsavedProgressLoss: confirmUnsavedProgressLoss}
	body, status, err := c.doUser(ctx, discordUserID, http.MethodPost, path, payload, c.newKey())
	if err != nil {
		return err
	}
	if !statusOK(status) {
		return c.statusError(http.MethodPost, path, status, body)
	}
	return nil
}

// SendConsole posts text to the first runtime channel whose interaction is "interactive".
func (c *Client) SendConsole(ctx context.Context, discordUserID, serverID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("console text is empty")
	}
	if strings.TrimSpace(serverID) == "" || strings.TrimSpace(discordUserID) == "" {
		return errors.New("missing server or discord user")
	}
	channels, err := c.runtimeChannels(ctx, discordUserID, serverID)
	if err != nil {
		return err
	}
	channelID, ok := firstInteractive(channels)
	if !ok {
		return ErrNoInteractiveChannel
	}
	path := serverPath(serverID) + "/runtime-channels/" + url.PathEscape(channelID) + "/commands"
	body, status, err := c.doUser(ctx, discordUserID, http.MethodPost, path, consoleRequest{Text: text}, "")
	if err != nil {
		return err
	}
	if !statusOK(status) {
		return c.statusError(http.MethodPost, path, status, body)
	}
	return nil
}

func (c *Client) runtimeChannels(ctx context.Context, discordUserID, serverID string) ([]Channel, error) {
	path := serverPath(serverID) + "/runtime-channels"
	body, status, err := c.doUser(ctx, discordUserID, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	if !statusOK(status) {
		return nil, c.statusError(http.MethodGet, path, status, body)
	}
	var payload struct {
		Channels []Channel `json:"channels"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		var bare []Channel
		if bareErr := json.Unmarshal(body, &bare); bareErr != nil {
			return nil, fmt.Errorf("genos runtime channels: %w", err)
		}
		return bare, nil
	}
	if payload.Channels == nil {
		return []Channel{}, nil
	}
	return payload.Channels, nil
}

func firstInteractive(channels []Channel) (string, bool) {
	for _, channel := range channels {
		if channel.Interaction == "interactive" && strings.TrimSpace(channel.ID) != "" {
			return channel.ID, true
		}
	}
	return "", false
}

func (c *Client) doUser(ctx context.Context, discordUserID, method, path string, payload any, key string) ([]byte, int, error) {
	token, err := c.bearerFor(ctx, discordUserID)
	if err != nil {
		return nil, 0, err
	}
	status, body, err := c.do(ctx, method, path, token, payload, key)
	if err != nil {
		return nil, status, err
	}
	if status == http.StatusUnauthorized {
		c.invalidate(discordUserID, token)
		token, err = c.bearerFor(ctx, discordUserID)
		if err != nil {
			return nil, status, err
		}
		status, body, err = c.do(ctx, method, path, token, payload, key)
	}
	return body, status, err
}

func (c *Client) bearerFor(ctx context.Context, discordUserID string) (string, error) {
	discordUserID = strings.TrimSpace(discordUserID)
	if discordUserID == "" {
		return "", errors.New("discord user id is empty")
	}
	now := c.now()
	c.mu.Lock()
	if ent, ok := c.cache[discordUserID]; ok && ent.value != "" && now.Before(ent.expires) {
		token := ent.value
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	token, expires, err := c.mint(ctx, discordUserID)
	if err != nil {
		return "", err
	}
	if !expires.IsZero() {
		c.mu.Lock()
		if c.cache == nil {
			c.cache = map[string]cachedToken{}
		}
		c.cache[discordUserID] = cachedToken{value: token, expires: expires}
		c.mu.Unlock()
	}
	return token, nil
}

func (c *Client) invalidate(discordUserID, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ent, ok := c.cache[discordUserID]; ok && ent.value == token {
		delete(c.cache, discordUserID)
	}
}

func (c *Client) mint(ctx context.Context, discordUserID string) (string, time.Time, error) {
	if c.BotSecret == "" {
		return "", time.Time{}, errors.New("genos bot secret is empty")
	}
	status, body, err := c.do(ctx, http.MethodPost, impersonationPath, c.BotSecret, map[string]string{
		"discordUserID": discordUserID,
	}, "")
	if err != nil {
		return "", time.Time{}, err
	}
	if !statusOK(status) {
		// The mint body can echo a token. Do not include it.
		return "", time.Time{}, c.statusError(http.MethodPost, impersonationPath, status, nil)
	}
	var payload mintResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", time.Time{}, fmt.Errorf("genos impersonation: %w", err)
	}
	payload.Token = strings.TrimSpace(payload.Token)
	if !strings.HasPrefix(payload.Token, "genos_discord_") || payload.Token == c.BotSecret {
		return "", time.Time{}, errors.New("genos impersonation: unexpected token")
	}
	if payload.TokenKind != "" && payload.TokenKind != "discord" {
		return "", time.Time{}, fmt.Errorf("genos impersonation: unexpected token kind %q", payload.TokenKind)
	}
	return payload.Token, payload.ExpiresAt, nil
}

func (c *Client) do(ctx context.Context, method, path, token string, payload any, key string) (int, []byte, error) {
	req, err := c.newJSON(ctx, method, path, token, payload)
	if err != nil {
		return 0, nil, err
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func (c *Client) newJSON(ctx context.Context, method, path, token string, payload any) (*http.Request, error) {
	var buf io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), buf)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "genos-discord")
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("missing bearer token")
	}
	req.Header.Set("Authorization", authorizationBearer(token))
	return req, nil
}

func (c *Client) endpoint(path string) string {
	return c.Origin + path
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) newKey() string {
	if c.NewKey != nil {
		return c.NewKey()
	}
	return uuid.NewString()
}

func (c *Client) statusError(method, path string, status int, body []byte) error {
	if path == impersonationPath {
		return fmt.Errorf("genos impersonation: status %d", status)
	}
	detail := strings.TrimSpace(redact(string(body)))
	if len(detail) > 300 {
		detail = detail[:300]
	}
	if detail == "" {
		return fmt.Errorf("genos %s %s: status %d", method, path, status)
	}
	return fmt.Errorf("genos %s %s: status %d: %s", method, path, status, detail)
}

func parseServer(data []byte) (Server, error) {
	var wrapped struct {
		Server *Server `json:"server"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return Server{}, err
	}
	if wrapped.Server != nil && wrapped.Server.ID != "" {
		return *wrapped.Server, nil
	}
	var server Server
	if err := json.Unmarshal(data, &server); err != nil {
		return Server{}, err
	}
	if server.ID == "" {
		return Server{}, errors.New("server response missing id")
	}
	return server, nil
}

func serverPath(serverID string) string {
	return "/api/v1/servers/" + url.PathEscape(strings.TrimSpace(serverID))
}

func authorizationBearer(secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) >= 7 && strings.EqualFold(secret[:7], "bearer ") {
		secret = strings.TrimSpace(secret[7:])
	}
	return "Bearer " + secret
}

func redact(s string) string {
	s = mintedTokenPattern.ReplaceAllString(s, "genos_discord_[redacted]")
	s = bearerPattern.ReplaceAllString(s, "Bearer [redacted]")
	return s
}

func statusOK(status int) bool {
	return status >= 200 && status < 300
}

func normalize(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{Timeout: 20 * time.Second}
	}
	clone := *base
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}
