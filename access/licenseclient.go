package access

import (
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// LicenseClient holds a WebSocket to the control server for one key id and
// enforces its validity fail-closed: it calls onStop exactly once on a
// revoke message or on a lost connection that cannot be re-established
// within the grace window. A caller-initiated Close never fires onStop.
type LicenseClient struct {
	wsURL   string
	keyID   string
	grace   time.Duration
	onStop  func(reason string)
	once    sync.Once
	closing atomic.Bool
	done    chan struct{}

	connMu sync.Mutex
	conn   *websocket.Conn
}

// Dial connects to wsURL (the exit WebSocket endpoint) for keyID and starts
// enforcing. grace bounds how long a lost connection may stay down before
// onStop fires; heartbeats are sent every grace/2.
func Dial(wsURL, keyID string, grace time.Duration, onStop func(reason string)) (*LicenseClient, error) {
	lc := &LicenseClient{
		wsURL:  wsURL,
		keyID:  keyID,
		grace:  grace,
		onStop: onStop,
		done:   make(chan struct{}),
	}
	conn, err := lc.connect()
	if err != nil {
		return nil, err
	}
	lc.conn = conn
	go lc.run()
	return lc, nil
}

func (lc *LicenseClient) connect() (*websocket.Conn, error) {
	u, err := url.Parse(lc.wsURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("id", lc.keyID)
	u.RawQuery = q.Encode()
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	return c, err
}

func (lc *LicenseClient) stop(reason string) {
	lc.once.Do(func() { lc.onStop(reason) })
}

// run is the read + heartbeat loop with reconnect-within-grace.
func (lc *LicenseClient) run() {
	hb := time.NewTicker(lc.grace / 2)
	defer hb.Stop()
	for {
		lc.connMu.Lock()
		conn := lc.conn
		lc.connMu.Unlock()

		msgCh := make(chan Msg, 1)
		errCh := make(chan error, 1)
		go func() {
			for {
				var m Msg
				if err := conn.ReadJSON(&m); err != nil {
					errCh <- err
					return
				}
				msgCh <- m
			}
		}()

	readLoop:
		for {
			select {
			case <-lc.done:
				return
			case m := <-msgCh:
				if m.Type == "revoke" {
					lc.stop("revoked")
					return
				}
			case <-hb.C:
				lc.connMu.Lock()
				_ = lc.conn.WriteJSON(Msg{Type: "heartbeat"})
				lc.connMu.Unlock()
			case <-errCh:
				if lc.closing.Load() {
					return
				}
				if !lc.reconnectWithinGrace() {
					lc.stop("connection lost")
					return
				}
				break readLoop // reconnected; restart the read goroutine
			}
		}
	}
}

// reconnectWithinGrace retries the dial until grace elapses. Returns true if
// a new connection was established.
func (lc *LicenseClient) reconnectWithinGrace() bool {
	deadline := time.Now().Add(lc.grace)
	for time.Now().Before(deadline) {
		if lc.closing.Load() {
			return false
		}
		if c, err := lc.connect(); err == nil {
			lc.connMu.Lock()
			lc.conn = c
			lc.connMu.Unlock()
			return true
		}
		time.Sleep(lc.grace / 10)
	}
	return false
}

// Close stops the client without firing onStop.
func (lc *LicenseClient) Close() error {
	lc.closing.Store(true)
	select {
	case <-lc.done:
	default:
		close(lc.done)
	}
	lc.connMu.Lock()
	defer lc.connMu.Unlock()
	if lc.conn != nil {
		return lc.conn.Close()
	}
	return nil
}
