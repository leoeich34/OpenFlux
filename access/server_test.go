package access

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/p1neappleXpress/OpenFlux/share"
)

func newTestServer(t *testing.T, pool []string) (*Server, *httptest.Server, *httptest.Server) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "s.json"), pool)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := NewServer(st)
	admin := httptest.NewServer(srv.AdminHandler())
	exit := httptest.NewServer(srv.ExitHandler())
	t.Cleanup(admin.Close)
	t.Cleanup(exit.Close)
	return srv, admin, exit
}

func adminIssue(t *testing.T, admin *httptest.Server, ttlSeconds int) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"ttlSeconds": ttlSeconds})
	resp, err := http.Post(admin.URL+"/admin/issue", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("issue POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("issue status %d", resp.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func dialExit(t *testing.T, exit *httptest.Server, id string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(exit.URL, "http") + "/exit/ws?id=" + id
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func readRevoke(t *testing.T, c *websocket.Conn, within time.Duration) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(within))
	for {
		var m Msg
		if err := c.ReadJSON(&m); err != nil {
			t.Fatalf("expected revoke within %s, got read error: %v", within, err)
		}
		if m.Type == "revoke" {
			return
		}
	}
}

func TestAdminIssueReturnsDecodableKey(t *testing.T) {
	_, admin, _ := newTestServer(t, []string{"https://telemost.yandex.ru/j/1"})
	out := adminIssue(t, admin, 3600)
	key, _ := out["key"].(string)
	cfg, err := share.Decode(key)
	if err != nil {
		t.Fatalf("Decode issued key: %v", err)
	}
	if len(cfg.Transports) != 1 || cfg.Transports[0].Type != "telemost" {
		t.Errorf("unexpected transports: %+v", cfg.Transports)
	}
}

func TestAdminListOmitsSecret(t *testing.T) {
	_, admin, _ := newTestServer(t, []string{"https://telemost.yandex.ru/j/1"})
	out := adminIssue(t, admin, 3600)
	resp, err := http.Get(admin.URL + "/admin/list")
	if err != nil {
		t.Fatalf("list GET: %v", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	s := buf.String()
	if strings.Contains(s, "secret") {
		t.Errorf("list output contains the word secret: %s", s)
	}
	_ = out
}

func TestAdminRevokeUnknownIs404(t *testing.T) {
	_, admin, _ := newTestServer(t, []string{"https://telemost.yandex.ru/j/1"})
	body, _ := json.Marshal(map[string]string{"id": "nope"})
	resp, err := http.Post(admin.URL+"/admin/revoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("revoke POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestExitWSGetsRevokeAfterAdminRevoke(t *testing.T) {
	_, admin, exit := newTestServer(t, []string{"https://telemost.yandex.ru/j/1"})
	out := adminIssue(t, admin, 3600)
	id := out["id"].(string)
	c := dialExit(t, exit, id)

	body, _ := json.Marshal(map[string]string{"id": id})
	resp, err := http.Post(admin.URL+"/admin/revoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("revoke POST: %v", err)
	}
	resp.Body.Close()

	readRevoke(t, c, 3*time.Second)
}

func TestExitWSRevokeOnConnectIfExpired(t *testing.T) {
	_, admin, exit := newTestServer(t, []string{"https://telemost.yandex.ru/j/1"})
	out := adminIssue(t, admin, 0) // already expired
	id := out["id"].(string)
	c := dialExit(t, exit, id)
	readRevoke(t, c, 3*time.Second)
}

func TestExitWSRevokeOnMidSessionExpiry(t *testing.T) {
	_, admin, exit := newTestServer(t, []string{"https://telemost.yandex.ru/j/1"})
	out := adminIssue(t, admin, 1) // valid for 1s
	id := out["id"].(string)
	c := dialExit(t, exit, id)
	// stays connected; the key expires ~1s in and the server must push revoke
	readRevoke(t, c, 4*time.Second)
}
