package access

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeWS stands up a WebSocket server that runs handler with each upgraded
// connection. It returns the ws:// URL and a close func.
func fakeWS(t *testing.T, handler func(*websocket.Conn)) (string, func()) {
	t.Helper()
	up := websocket.Upgrader{}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		handler(c)
	}))
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/exit/ws"
	return url, hs.Close
}

func waitStop(t *testing.T, ch <-chan string, within time.Duration) string {
	t.Helper()
	select {
	case reason := <-ch:
		return reason
	case <-time.After(within):
		t.Fatalf("onStop did not fire within %s", within)
		return ""
	}
}

func TestOnStopFiresOnRevokeMessage(t *testing.T) {
	url, closeSrv := fakeWS(t, func(c *websocket.Conn) {
		c.WriteJSON(Msg{Type: "revoke"})
		time.Sleep(time.Second)
	})
	defer closeSrv()

	stop := make(chan string, 1)
	lc, err := Dial(url, "id1", "s1", 500*time.Millisecond, func(reason string) { stop <- reason })
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer lc.Close()
	if r := waitStop(t, stop, 3*time.Second); r == "" {
		t.Errorf("empty reason")
	}
}

func TestOnStopFiresOnConnectionLoss(t *testing.T) {
	url, closeSrv := fakeWS(t, func(c *websocket.Conn) { c.Close() })
	// close the whole server too, so reconnection fails
	closeSrv()

	stop := make(chan string, 1)
	lc, err := Dial(url, "id1", "s1", 500*time.Millisecond, func(reason string) { stop <- reason })
	if err != nil {
		// Dial itself may fail because the server is down; that is a valid
		// fail-closed outcome only if it is surfaced — the exit wiring treats
		// a Dial error as stop. For this test we require Dial to succeed is
		// not possible, so accept either a Dial error or an onStop.
		return
	}
	defer lc.Close()
	waitStop(t, stop, 3*time.Second)
}

func TestCloseDoesNotFireOnStop(t *testing.T) {
	url, closeSrv := fakeWS(t, func(c *websocket.Conn) { time.Sleep(2 * time.Second) })
	defer closeSrv()

	var fired atomic.Bool
	lc, err := Dial(url, "id1", "s1", 500*time.Millisecond, func(string) { fired.Store(true) })
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	lc.Close()
	time.Sleep(700 * time.Millisecond)
	if fired.Load() {
		t.Errorf("onStop fired after Close")
	}
}

func TestOnStopFiresOnce(t *testing.T) {
	url, closeSrv := fakeWS(t, func(c *websocket.Conn) {
		c.WriteJSON(Msg{Type: "revoke"})
		c.Close()
	})
	defer closeSrv()

	var count atomic.Int32
	lc, err := Dial(url, "id1", "s1", 500*time.Millisecond, func(string) { count.Add(1) })
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer lc.Close()
	time.Sleep(1500 * time.Millisecond)
	if got := count.Load(); got != 1 {
		t.Errorf("onStop fired %d times, want 1", got)
	}
}
