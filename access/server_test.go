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

// fakeLifecycle records calls and returns canned values.
type fakeLifecycle struct {
	issueKey string
	issueID  string
	released []string
}

func (f *fakeLifecycle) Issue(ttl time.Duration) (KeyRecord, string, error) {
	return KeyRecord{ID: f.issueID, CallLink: "c", Status: StatusActive}, f.issueKey, nil
}
func (f *fakeLifecycle) Release(id string) error {
	f.released = append(f.released, id)
	return nil
}

func TestServerUsesLifecycleForIssue(t *testing.T) {
	srv, admin, _ := newTestServer(t, []string{"x"})
	lc := &fakeLifecycle{issueKey: "openflux://v1/ZmFrZQ", issueID: "ID1"}
	srv.SetLifecycle(lc)
	out := adminIssue(t, admin, 3600)
	if out["key"] != lc.issueKey || out["id"] != lc.issueID {
		t.Errorf("issue did not use lifecycle: %+v", out)
	}
}

func TestServerUsesLifecycleForRevoke(t *testing.T) {
	srv, admin, _ := newTestServer(t, []string{"x"})
	lc := &fakeLifecycle{}
	srv.SetLifecycle(lc)
	body, _ := json.Marshal(map[string]string{"id": "ID9"})
	resp, err := http.Post(admin.URL+"/admin/revoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("revoke POST: %v", err)
	}
	resp.Body.Close()
	if len(lc.released) != 1 || lc.released[0] != "ID9" {
		t.Errorf("Release not called with ID9: %v", lc.released)
	}
}

func TestServerConnectedTracksExitWS(t *testing.T) {
	srv, admin, exit := newTestServer(t, []string{"https://telemost.yandex.ru/j/1"})
	out := adminIssue(t, admin, 3600)
	id := out["id"].(string)
	if srv.Connected(id) {
		t.Errorf("Connected before dial")
	}
	c := dialExit(t, exit, id)
	deadline := time.Now().Add(2 * time.Second)
	for !srv.Connected(id) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.Connected(id) {
		t.Fatalf("Connected false after dial")
	}
	c.Close()
	for srv.Connected(id) && time.Now().Before(deadline.Add(2*time.Second)) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.Connected(id) {
		t.Errorf("Connected true after close")
	}
}

func TestExitNextCallReservesAndCommits(t *testing.T) {
	_, admin, exit := newTestServer(t, []string{"https://telemost.yandex.ru/j/1", "https://telemost.yandex.ru/j/2"})
	out := adminIssue(t, admin, 3600)
	id := out["id"].(string)
	proof := ProofFor(keySecret(t, out), id)

	req, _ := http.NewRequest("GET", exit.URL+"/exit/nextcall?id="+id, nil)
	req.Header.Set("X-OpenFlux-Proof", proof)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("nextcall: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("nextcall status %d", resp.StatusCode)
	}
	var nc map[string]string
	json.NewDecoder(resp.Body).Decode(&nc)
	if nc["callLink"] == "" || nc["callLink"] == out["callLink"] {
		t.Errorf("nextcall gave %q (current %q)", nc["callLink"], out["callLink"])
	}

	creq, _ := http.NewRequest("POST", exit.URL+"/exit/commitcall?id="+id, nil)
	creq.Header.Set("X-OpenFlux-Proof", proof)
	cresp, err := http.DefaultClient.Do(creq)
	if err != nil {
		t.Fatalf("commitcall: %v", err)
	}
	cresp.Body.Close()
	if cresp.StatusCode != 200 {
		t.Errorf("commitcall status %d", cresp.StatusCode)
	}
}

func TestExitNextCallExhaustedIs409(t *testing.T) {
	_, admin, exit := newTestServer(t, []string{"https://telemost.yandex.ru/j/1"}) // only one link
	out := adminIssue(t, admin, 3600)
	id := out["id"].(string)
	req, _ := http.NewRequest("GET", exit.URL+"/exit/nextcall?id="+id, nil)
	req.Header.Set("X-OpenFlux-Proof", ProofFor(keySecret(t, out), id))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("nextcall: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func keySecret(t *testing.T, out map[string]any) string {
	t.Helper()
	cfg, err := share.Decode(out["key"].(string))
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	return cfg.Secret
}

func TestExitNextCallRequiresProof(t *testing.T) {
	_, admin, exit := newTestServer(t, []string{"https://telemost.yandex.ru/j/1", "https://telemost.yandex.ru/j/2"})
	out := adminIssue(t, admin, 3600)
	id := out["id"].(string)
	secret := keySecret(t, out)

	// No proof -> 403
	resp, _ := http.Get(exit.URL + "/exit/nextcall?id=" + id)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("no proof: status %d, want 403", resp.StatusCode)
	}
	// Wrong proof -> 403
	req, _ := http.NewRequest("GET", exit.URL+"/exit/nextcall?id="+id, nil)
	req.Header.Set("X-OpenFlux-Proof", "deadbeef")
	resp2, _ := http.DefaultClient.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != 403 {
		t.Errorf("wrong proof: status %d, want 403", resp2.StatusCode)
	}
	// Correct proof -> 200
	req3, _ := http.NewRequest("GET", exit.URL+"/exit/nextcall?id="+id, nil)
	req3.Header.Set("X-OpenFlux-Proof", ProofFor(secret, id))
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("nextcall: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Errorf("correct proof: status %d, want 200", resp3.StatusCode)
	}
}
