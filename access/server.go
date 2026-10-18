package access

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ProofFor returns the proof a holder of a key's secret presents to the exit
// hub's mutating endpoints for id: hex(HMAC-SHA256(secret, id)). The secret
// never travels; an attacker who knows only the (non-secret) key id cannot
// forge it.
func ProofFor(secret, id string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	return hex.EncodeToString(mac.Sum(nil))
}

// Msg is the JSON message exchanged on the exit WebSocket, in both
// directions. Type is "heartbeat" (exit → server) or "revoke" (server → exit).
type Msg struct {
	Type string `json:"type"`
}

// expiryScanInterval is how often a connected exit's key is re-checked for
// expiry. Short so mid-session expiry is enforced promptly.
const expiryScanInterval = 200 * time.Millisecond

// KeyLifecycle lets a higher layer (the pool orchestrator) own issue and
// revoke so that issuing a key also launches its exit and revoking stops it.
// When unset, the server issues/revokes against the store directly (Plan 1).
type KeyLifecycle interface {
	Issue(ttl time.Duration) (KeyRecord, string, error)
	Release(id string) error
}

// Server is the control plane: an admin HTTP API and an exit WebSocket hub
// over one Store.
type Server struct {
	store    *Store
	upgrader websocket.Upgrader

	mu        sync.Mutex
	revoked   map[string]chan struct{} // id -> closed when revoked
	connected map[string]int           // id -> live exit WS count
	lifecycle KeyLifecycle
}

// NewServer builds a control server over s.
func NewServer(s *Store) *Server {
	return &Server{
		store:     s,
		revoked:   map[string]chan struct{}{},
		connected: map[string]int{},
	}
}

// SetLifecycle routes /admin/issue and /admin/revoke through l.
func (srv *Server) SetLifecycle(l KeyLifecycle) {
	srv.mu.Lock()
	srv.lifecycle = l
	srv.mu.Unlock()
}

// Connected reports whether an exit for id currently holds the WebSocket.
func (srv *Server) Connected(id string) bool {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.connected[id] > 0
}

func (srv *Server) markConnected(id string, delta int) {
	srv.mu.Lock()
	srv.connected[id] += delta
	if srv.connected[id] <= 0 {
		delete(srv.connected, id)
	}
	srv.mu.Unlock()
}

// revokeChan returns (creating if needed) the broadcast channel for id.
func (srv *Server) revokeChan(id string) chan struct{} {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	ch, ok := srv.revoked[id]
	if !ok {
		ch = make(chan struct{})
		srv.revoked[id] = ch
	}
	return ch
}

// signalRevoke closes id's broadcast channel so any connected exit wakes.
func (srv *Server) signalRevoke(id string) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	ch, ok := srv.revoked[id]
	if !ok {
		ch = make(chan struct{})
		srv.revoked[id] = ch
	}
	select {
	case <-ch: // already closed
	default:
		close(ch)
	}
}

// AdminHandler serves the loopback operator API.
func (srv *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/issue", srv.handleIssue)
	mux.HandleFunc("/admin/revoke", srv.handleRevoke)
	mux.HandleFunc("/admin/extend", srv.handleExtend)
	mux.HandleFunc("/admin/list", srv.handleList)
	return mux
}

func (srv *Server) handleIssue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TTLSeconds int `json:"ttlSeconds"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	ttl := time.Duration(req.TTLSeconds) * time.Second

	srv.mu.Lock()
	lc := srv.lifecycle
	srv.mu.Unlock()

	var (
		rec KeyRecord
		key string
		err error
	)
	if lc != nil {
		rec, key, err = lc.Issue(ttl)
	} else {
		rec, err = srv.store.Issue(ttl, time.Now())
		if err == nil {
			key, err = BuildKey(rec.CallLink, rec.Secret)
		}
	}
	if err == ErrPoolExhausted {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"id":        rec.ID,
		"key":       key,
		"callLink":  rec.CallLink,
		"expiresAt": rec.ExpiresAt,
	})
}

func (srv *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	srv.mu.Lock()
	lc := srv.lifecycle
	srv.mu.Unlock()

	var err error
	if lc != nil {
		err = lc.Release(req.ID)
	} else {
		err = srv.store.Revoke(req.ID)
	}
	switch err {
	case nil:
		srv.signalRevoke(req.ID)
		writeJSON(w, map[string]string{"status": "revoked"})
	case ErrNotFound:
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (srv *Server) handleExtend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         string `json:"id"`
		TTLSeconds int    `json:"ttlSeconds"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	switch err := srv.store.Extend(req.ID, time.Duration(req.TTLSeconds)*time.Second); err {
	case nil:
		writeJSON(w, map[string]string{"status": "extended"})
	case ErrNotFound:
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// listItem is the public view of a key: never the secret.
type listItem struct {
	ID        string    `json:"id"`
	Status    Status    `json:"status"`
	CallLink  string    `json:"callLink"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (srv *Server) handleList(w http.ResponseWriter, r *http.Request) {
	recs := srv.store.List()
	out := make([]listItem, 0, len(recs))
	for _, rec := range recs {
		out = append(out, listItem{ID: rec.ID, Status: rec.Status, CallLink: rec.CallLink, ExpiresAt: rec.ExpiresAt})
	}
	writeJSON(w, out)
}

// ExitHandler serves the exit-facing WebSocket: GET /exit/ws?id=<keyID>.
func (srv *Server) ExitHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/exit/ws", srv.handleExitWS)
	mux.HandleFunc("/exit/nextcall", srv.handleNextCall)
	mux.HandleFunc("/exit/commitcall", srv.handleCommitCall)
	return mux
}

// checkProof returns true when the request carries a valid per-key proof for
// id. It also writes the appropriate error (404 unknown id, 403 bad proof).
func (srv *Server) checkProof(w http.ResponseWriter, r *http.Request, id string) bool {
	rec, ok := srv.store.Get(id)
	if !ok {
		http.Error(w, ErrNotFound.Error(), http.StatusNotFound)
		return false
	}
	want := ProofFor(rec.Secret, id)
	got := r.Header.Get("X-OpenFlux-Proof")
	if !hmac.Equal([]byte(got), []byte(want)) {
		http.Error(w, "invalid proof", http.StatusForbidden)
		return false
	}
	return true
}

// handleNextCall reserves a fresh pool call link for the key's migration.
func (srv *Server) handleNextCall(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !srv.checkProof(w, r, id) {
		return
	}
	link, err := srv.store.ReserveNextCall(id, time.Now())
	switch err {
	case nil:
		writeJSON(w, map[string]string{"callLink": link})
	case ErrNotFound:
		http.Error(w, err.Error(), http.StatusNotFound)
	case ErrPoolExhausted:
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleCommitCall promotes the reserved migration target to the current call.
func (srv *Server) handleCommitCall(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !srv.checkProof(w, r, id) {
		return
	}
	switch err := srv.store.CommitNextCall(id); err {
	case nil:
		writeJSON(w, map[string]string{"status": "committed"})
	case ErrNotFound:
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (srv *Server) handleExitWS(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	// Require the same per-key proof as the mutating hub endpoints. The hub
	// binds on all interfaces, and a connected WS counts toward cold-launch
	// readiness (srv.connected); without this a spoofed connection could
	// satisfy readiness before the real exit joins. Checked before the upgrade
	// so a forged dial is rejected with a plain 403/404.
	if !srv.checkProof(w, r, id) {
		return
	}
	c, err := srv.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()

	sendRevoke := func() { _ = c.WriteJSON(Msg{Type: "revoke"}) }

	rec, ok := srv.store.Get(id)
	if !ok || !rec.Valid(time.Now()) {
		sendRevoke()
		return
	}

	srv.markConnected(id, 1)
	defer srv.markConnected(id, -1)

	revoked := srv.revokeChan(id)

	// Drain inbound heartbeats so the read side stays alive and so a client
	// disconnect unblocks us.
	readErr := make(chan struct{})
	go func() {
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				close(readErr)
				return
			}
		}
	}()

	ticker := time.NewTicker(expiryScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-revoked:
			sendRevoke()
			return
		case <-readErr:
			return
		case <-ticker.C:
			rec, ok := srv.store.Get(id)
			if !ok || !rec.Valid(time.Now()) {
				sendRevoke()
				return
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
