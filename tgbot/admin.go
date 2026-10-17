package tgbot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Errors the dispatcher phrases for the operator.
var (
	ErrPoolExhausted = errors.New("call pool exhausted (no free link / warm exit)")
	ErrNotFound      = errors.New("key not found")
)

// KeyInfo is one row of the admin list.
type KeyInfo struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CallLink  string `json:"callLink"`
	ExpiresAt string `json:"expiresAt"`
}

// AdminClient calls the accessd loopback Admin API.
type AdminClient struct {
	base string
	http *http.Client
}

// NewAdminClient returns a client for the admin API at base (host:port or URL).
func NewAdminClient(base string) *AdminClient {
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	return &AdminClient{base: strings.TrimRight(base, "/"), http: &http.Client{}}
}

// statusErr maps an HTTP status to a phrased error.
func statusErr(code int) error {
	switch code {
	case http.StatusOK:
		return nil
	case http.StatusConflict:
		return ErrPoolExhausted
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("admin API error (status %d)", code)
	}
}

func (a *AdminClient) post(path string, body any) (*http.Response, error) {
	b, _ := json.Marshal(body)
	return a.http.Post(a.base+path, "application/json", bytes.NewReader(b))
}

// Issue creates a key valid for `days` days and returns its id and openflux:// key.
func (a *AdminClient) Issue(days int) (id, key string, err error) {
	resp, err := a.post("/admin/issue", map[string]int{"ttlSeconds": days * 86400})
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if err := statusErr(resp.StatusCode); err != nil {
		return "", "", err
	}
	var out struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	return out.ID, out.Key, nil
}

// Revoke revokes a key.
func (a *AdminClient) Revoke(id string) error {
	resp, err := a.post("/admin/revoke", map[string]string{"id": id})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return statusErr(resp.StatusCode)
}

// Extend moves a key's expiry out by `days` days.
func (a *AdminClient) Extend(id string, days int) error {
	resp, err := a.post("/admin/extend", map[string]any{"id": id, "ttlSeconds": days * 86400})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return statusErr(resp.StatusCode)
}

// List returns all keys.
func (a *AdminClient) List() ([]KeyInfo, error) {
	resp, err := a.http.Get(a.base + "/admin/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := statusErr(resp.StatusCode); err != nil {
		return nil, err
	}
	var out []KeyInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
