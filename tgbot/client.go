// Package tgbot is the Telegram operator bot for the OpenFlux access-key
// service: a thin front end over the loopback Admin API. It speaks the
// Telegram Bot API directly over HTTPS+JSON (no third-party library, pure Go).
// See docs/superpowers/plans/2026-10-17-telegram-operator-bot.md.
package tgbot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Update is one inbound Telegram message, flattened to what the bot needs.
type Update struct {
	UpdateID int
	ChatID   int64
	UserID   int64
	Text     string
}

// Client is the subset of the Telegram Bot API the bot uses.
type Client interface {
	GetUpdates(offset, timeoutSec int) ([]Update, error)
	SendMessage(chatID int64, text string) error
}

// HTTPClient talks to api.telegram.org (or WithBaseURL for tests).
type HTTPClient struct {
	token   string
	baseURL string
	http    *http.Client
}

// Option configures an HTTPClient.
type Option func(*HTTPClient)

// WithBaseURL overrides the Telegram API base (for tests).
func WithBaseURL(base string) Option { return func(c *HTTPClient) { c.baseURL = base } }

// NewHTTPClient returns a Client for the given bot token.
func NewHTTPClient(token string, opts ...Option) Client {
	c := &HTTPClient{
		token:   token,
		baseURL: "https://api.telegram.org",
		http:    &http.Client{Timeout: 65 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *HTTPClient) method(name string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.baseURL, c.token, name)
}

// redact strips the bot token from an error string. The token sits in the
// request URL path, and net/url only redacts userinfo, so a transport error
// (routine on a long-poll loop) would otherwise print the token verbatim.
func (c *HTTPClient) redact(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), c.token, "<token>"))
}

// tgGetUpdatesResponse mirrors the Bot API getUpdates envelope.
type tgGetUpdatesResponse struct {
	OK     bool `json:"ok"`
	Result []struct {
		UpdateID int `json:"update_id"`
		Message  struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			From struct {
				ID int64 `json:"id"`
			} `json:"from"`
			Text string `json:"text"`
		} `json:"message"`
	} `json:"result"`
}

// GetUpdates long-polls for new updates from offset, blocking up to timeoutSec.
func (c *HTTPClient) GetUpdates(offset, timeoutSec int) ([]Update, error) {
	q := url.Values{}
	q.Set("offset", strconv.Itoa(offset))
	q.Set("timeout", strconv.Itoa(timeoutSec))
	resp, err := c.http.Get(c.method("getUpdates") + "?" + q.Encode())
	if err != nil {
		return nil, c.redact(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getUpdates: status %d", resp.StatusCode)
	}
	var out tgGetUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("getUpdates: Telegram returned ok=false")
	}
	ups := make([]Update, 0, len(out.Result))
	for _, r := range out.Result {
		ups = append(ups, Update{
			UpdateID: r.UpdateID,
			ChatID:   r.Message.Chat.ID,
			UserID:   r.Message.From.ID,
			Text:     r.Message.Text,
		})
	}
	return ups, nil
}

// SendMessage posts a text reply to chatID.
func (c *HTTPClient) SendMessage(chatID int64, text string) error {
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": text})
	resp, err := c.http.Post(c.method("sendMessage"), "application/json", bytes.NewReader(body))
	if err != nil {
		return c.redact(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sendMessage: status %d", resp.StatusCode)
	}
	return nil
}
