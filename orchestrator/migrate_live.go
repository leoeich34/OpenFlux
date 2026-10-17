package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/p1neappleXpress/OpenFlux/access"
	"github.com/p1neappleXpress/OpenFlux/transport/control"
	"github.com/p1neappleXpress/OpenFlux/transport/manager"
)

// ManagerMigrateDeps is the live MigrationDeps: health from the session's
// byte counters, call swaps via the manager's dynamic transports, and
// next/commit call over the control hub's HTTP endpoints. Its live behavior
// is verified by the manual runbook (orchestrator/README.md), not unit tests.
type ManagerMigrateDeps struct {
	keyID   string
	secret  string // the per-key secret, for the hub proof
	mgr     *manager.Manager
	hubBase string // http base of the exit hub, derived from the license WS url

	lastBytes   uint64
	lastAdvance time.Time
	seenBytes   bool
}

// NewManagerMigrateDeps builds live deps for keyID. licenseWS is the exit
// WebSocket URL (ws://host:port/exit/ws); the HTTP endpoints live on the same
// host:port.
func NewManagerMigrateDeps(keyID, secret string, mgr *manager.Manager, licenseWS string) *ManagerMigrateDeps {
	return &ManagerMigrateDeps{
		keyID:       keyID,
		secret:      secret,
		mgr:         mgr,
		hubBase:     httpBaseFromWS(licenseWS),
		lastAdvance: time.Now(),
	}
}

func httpBaseFromWS(ws string) string {
	u, err := url.Parse(ws)
	if err != nil {
		return ws
	}
	scheme := "http"
	if u.Scheme == "wss" {
		scheme = "https"
	}
	return scheme + "://" + u.Host
}

// Health reports a stall as the time since the session last received bytes.
// A stall is only meaningful once traffic has actually flowed: until the first
// received byte (no buyer connected yet, or a genuinely idle tunnel) it
// reports 0, so an idle call is never mistaken for a dead one and migrated
// in a loop. Loss is not exposed at the manager layer, so it is reported 0:
// migration triggers on stall, the dominant "the call died" signal.
func (d *ManagerMigrateDeps) Health() (time.Duration, float64) {
	return stallFrom(d.mgr.Stats().BytesReceived, time.Now(), &d.lastBytes, &d.lastAdvance, &d.seenBytes), 0
}

// stallFrom derives the current stall from received-byte progress. It begins
// counting only after the first byte is seen.
func stallFrom(bytesRecv uint64, now time.Time, lastBytes *uint64, lastAdvance *time.Time, seen *bool) time.Duration {
	if !*seen {
		if bytesRecv == 0 {
			return 0
		}
		*seen = true
		*lastBytes = bytesRecv
		*lastAdvance = now
		return 0
	}
	if bytesRecv != *lastBytes {
		*lastBytes = bytesRecv
		*lastAdvance = now
	}
	return now.Sub(*lastAdvance)
}

// NextCall asks the hub for a fresh pool call link.
func (d *ManagerMigrateDeps) NextCall(keyID string) (string, error) {
	req, _ := http.NewRequest("GET", d.hubBase+"/exit/nextcall?id="+url.QueryEscape(keyID), nil)
	req.Header.Set("X-OpenFlux-Proof", access.ProofFor(d.secret, keyID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("nextcall %d: %s", resp.StatusCode, b)
	}
	var out struct {
		CallLink string `json:"callLink"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.CallLink, nil
}

// StartCall brings up a new telemost transport on both ends of the session.
func (d *ManagerMigrateDeps) StartCall(name, callURL string) error {
	return d.mgr.StartTransport(&control.TransportConfig{Name: name, Type: "telemost", URL: callURL})
}

// StopCall tears the named transport down on both ends.
func (d *ManagerMigrateDeps) StopCall(name string) error {
	return d.mgr.StopTransport(name)
}

// CommitCall promotes the reserved next call to current on the hub.
func (d *ManagerMigrateDeps) CommitCall(keyID string) error {
	req, _ := http.NewRequest("POST", d.hubBase+"/exit/commitcall?id="+url.QueryEscape(keyID), nil)
	req.Header.Set("X-OpenFlux-Proof", access.ProofFor(d.secret, keyID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("commitcall %d", resp.StatusCode)
	}
	return nil
}


// RunMigrationLoop drives ctrl on an interval until stop is closed.
func RunMigrationLoop(ctrl *MigrationController, interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if migrated, err := ctrl.Step(time.Now()); err != nil {
				log.Printf("[migrate] %v", err)
			} else if migrated {
				log.Printf("[migrate] migrated to a fresh call")
			}
		}
	}
}
