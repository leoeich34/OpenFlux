package access

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Msg is the JSON message exchanged on the exit WebSocket, in both
// directions. Type is "heartbeat" (exit → server) or "revoke" (server → exit).
type Msg struct {
	Type string `json:"type"`
}

// expiryScanInterval is how often a connected exit's key is re-checked for
// expiry. Short so mid-session expiry is enforced promptly.
const expiryScanInterval = 200 * time.Millisecond

// Server is the control plane: an admin HTTP API and an exit WebSocket hub
// over one Store.
type Server struct {
	store    *Store
	upgrader websocket.Upgrader

	mu      sync.Mutex
	revoked map[string]chan struct{} // id -> closed when revoked
}

// NewServer builds a control server over s.
func NewServer(s *Store) *Server {
	return &Server{
		store:   s,
		revoked: map[string]chan struct{}{},
	}
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
	rec, err := srv.store.Issue(time.Duration(req.TTLSeconds)*time.Second, time.Now())
	if err == ErrPoolExhausted {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	key, err := BuildKey(rec.CallLink, rec.Secret)
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
	switch err := srv.store.Revoke(req.ID); err {
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
	return mux
}

func (srv *Server) handleExitWS(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
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
