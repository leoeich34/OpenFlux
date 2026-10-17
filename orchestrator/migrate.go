package orchestrator

import (
	"fmt"
	"time"
)

// Migration policy thresholds. A call is migrated when it stalls or loses too
// much for long enough, but never more often than minMigrateInterval so a
// merely-slow call does not flap the whole pool.
const (
	stallFloor        = 6 * time.Second
	lossFloor         = 0.5
	minMigrateInterval = 20 * time.Second
)

// shouldMigrate is the pure policy: migrate when the call has stalled past
// stallFloor OR loss is above lossFloor, AND at least minMigrateInterval has
// passed since the last migration (anti-flap).
func shouldMigrate(stall time.Duration, lossFrac float64, sinceLast time.Duration) (bool, string) {
	if sinceLast < minMigrateInterval {
		return false, ""
	}
	if stall >= stallFloor {
		return true, fmt.Sprintf("stalled %s", stall.Round(time.Second))
	}
	if lossFrac >= lossFloor {
		return true, fmt.Sprintf("loss %.0f%%", lossFrac*100)
	}
	return false, ""
}

// MigrationDeps is the environment a MigrationController drives. The concrete
// implementation (telemost lane stats + manager dynamic transports + the
// control server's /exit/nextcall and /exit/commitcall) is verified by the
// manual runbook; the fake makes the sequence unit-testable.
type MigrationDeps interface {
	// Health reports the current call's stall duration and loss fraction.
	Health() (stall time.Duration, lossFrac float64)
	// NextCall reserves and returns a fresh pool call link for keyID.
	NextCall(keyID string) (string, error)
	// StartCall brings up a telemost transport named `name` on `url`, on this
	// node and (via the session control channel) on the peer.
	StartCall(name, url string) error
	// StopCall tears down the transport named `name` on both ends.
	StopCall(name string) error
	// CommitCall promotes the reserved next call to the key's current call.
	CommitCall(keyID string) error
}

// MigrationController migrates one key's call when it degrades. It is driven
// by Step (call it on a timer); the live loop is wired in the exit.
type MigrationController struct {
	keyID string
	deps  MigrationDeps

	lastMigrate time.Time
	seq         int
	curName     string // the transport currently carrying traffic
}

// NewMigrationController builds a controller for keyID. start is treated as
// the time of the (implicit) initial call, so the anti-flap interval applies
// from startup.
func NewMigrationController(keyID string, deps MigrationDeps, start time.Time) *MigrationController {
	return &MigrationController{keyID: keyID, deps: deps, lastMigrate: start}
}

// Step evaluates health once and migrates if the policy says so. It returns
// whether a migration happened.
func (m *MigrationController) Step(now time.Time) (bool, error) {
	stall, loss := m.deps.Health()
	migrate, reason := shouldMigrate(stall, loss, now.Sub(m.lastMigrate))
	if !migrate {
		return false, nil
	}
	newURL, err := m.deps.NextCall(m.keyID)
	if err != nil {
		return false, fmt.Errorf("nextcall: %w", err)
	}
	m.seq++
	name := fmt.Sprintf("telemost-migrated-%d", m.seq)
	if err := m.deps.StartCall(name, newURL); err != nil {
		return false, fmt.Errorf("start new call (%s): %w", reason, err)
	}
	// The old call stops only after the new one is up, so established TCP
	// flows ride the new transport without a gap.
	if err := m.deps.StopCall(m.prevName()); err != nil {
		// The new call is already carrying traffic; a failed stop of the old
		// one is not fatal, it just lingers until reconnect.
		_ = err
	}
	if err := m.deps.CommitCall(m.keyID); err != nil {
		return true, fmt.Errorf("commit: %w", err)
	}
	m.curName = name
	m.lastMigrate = now
	return true, nil
}

// prevName is the transport being replaced (the one StopCall targets). The
// very first migration replaces the bootstrap transport, whose name the exit
// sets via SetCurrentName.
func (m *MigrationController) prevName() string { return m.curName }

// SetCurrentName records the name of the transport currently carrying traffic
// (the bootstrap call at startup).
func (m *MigrationController) SetCurrentName(name string) { m.curName = name }
