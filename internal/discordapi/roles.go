// Package discordapi updates the Customer role over the Discord REST API.
// It does not read guild member roles before a command.
package discordapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAPIBase is the Discord REST prefix, including the API version.
const DefaultAPIBase = "https://discord.com/api/v10"

const userAgent = "DiscordBot (https://github.com/AnthonyPoschen/genos-discord, 0.1.0)"

// Client grants or revokes one guild role.
type Client struct {
	base    string
	token   string
	guildID string
	roleID  string
	http    *http.Client
}

// NewClient builds a role client. httpClient may be nil. apiBase defaults to DefaultAPIBase.
func NewClient(apiBase, botToken, guildID, roleID string, httpClient *http.Client) *Client {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		base = DefaultAPIBase
	}
	return &Client{
		base:    base,
		token:   strings.TrimSpace(botToken),
		guildID: strings.TrimSpace(guildID),
		roleID:  strings.TrimSpace(roleID),
		http:    normalize(httpClient),
	}
}

// GrantCustomer PUTs the Customer role. Owning a server is decided by the caller.
func (c *Client) GrantCustomer(ctx context.Context, discordUserID string) error {
	return c.do(ctx, http.MethodPut, discordUserID)
}

// RevokeCustomer DELETEs the Customer role when the caller owns zero current servers.
func (c *Client) RevokeCustomer(ctx context.Context, discordUserID string) error {
	return c.do(ctx, http.MethodDelete, discordUserID)
}

func (c *Client) do(ctx context.Context, method, discordUserID string) error {
	discordUserID = strings.TrimSpace(discordUserID)
	if discordUserID == "" || c.guildID == "" || c.roleID == "" {
		return errors.New("discord role target is incomplete")
	}
	if c.token == "" {
		return errors.New("discord bot token is empty")
	}
	endpoint := c.base + "/guilds/" + url.PathEscape(c.guildID) + "/members/" + url.PathEscape(discordUserID) + "/roles/" + url.PathEscape(c.roleID)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", botAuthorization(c.token))
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord %s customer role: status %d", method, resp.StatusCode)
	}
	return nil
}

func botAuthorization(token string) string {
	token = strings.TrimSpace(token)
	if len(token) >= 4 && strings.EqualFold(token[:4], "bot ") {
		return "Bot " + strings.TrimSpace(token[4:])
	}
	return "Bot " + token
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
