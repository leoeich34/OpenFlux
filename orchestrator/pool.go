package orchestrator

import (
	"errors"
	"sync"
	"time"

	"github.com/p1neappleXpress/OpenFlux/access"
)

// ErrNotReady is returned by Issue when a cold-launched exit does not join
// its call within the readiness timeout; the key is rolled back.
var ErrNotReady = errors.New("orchestrator: exit did not become ready in time")

// readyPoll is how often the cold path re-checks readiness.
const readyPoll = 100 * time.Millisecond

// warmTTL is how long a pre-issued warm key stays valid before a buyer
// claims it. Long enough that warm exits are not reaped before use; a claim
// resets the expiry to the buyer's real TTL.
const warmTTL = 24 * time.Hour

// Pool turns the static call pool of Plan 1 into live exits. It launches an
// exit per issued key, keeps `warm` pre-joined exits ready (joining a call
// takes ~30-40s), and stops an exit when its key is released.
type Pool struct {
	store     *access.Store
	launcher  ExitLauncher
	licenseWS string
	warm      int

	mu       sync.Mutex
	handles  map[string]ExitHandle // keyID -> running exit
	warmKeys []string              // issued-but-unclaimed key ids

	ready        func(keyID string) bool // nil = skip the readiness gate
	readyTimeout time.Duration
}

// SetReady installs the readiness gate for cold launches: Issue waits up to
// timeout for ready(keyID) to hold (the exit joined its call) before handing
// out the key, and rolls back with ErrNotReady otherwise. Warm exits bypass
// it, being joined ahead of time.
func (p *Pool) SetReady(ready func(keyID string) bool, timeout time.Duration) {
	p.mu.Lock()
	p.ready, p.readyTimeout = ready, timeout
	p.mu.Unlock()
}

// NewPool builds a pool over store and launcher. licenseWS is the exit WS the
// launched exits report to; warm is the number of pre-joined exits to keep.
func NewPool(store *access.Store, launcher ExitLauncher, licenseWS string, warm int) *Pool {
	return &Pool{
		store:     store,
		launcher:  launcher,
		licenseWS: licenseWS,
		warm:      warm,
		handles:   map[string]ExitHandle{},
	}
}

// launch issues a key (ttl), launches its exit, and records the handle. On
// launch failure it revokes the key (freeing the call link) and returns the
// error — never a live key with no exit.
func (p *Pool) launch(ttl time.Duration) (access.KeyRecord, string, error) {
	rec, err := p.store.Issue(ttl, time.Now())
	if err != nil {
		return access.KeyRecord{}, "", err
	}
	key, err := access.BuildKey(rec.CallLink, rec.Secret)
	if err != nil {
		_ = p.store.Revoke(rec.ID)
		return access.KeyRecord{}, "", err
	}
	h, err := p.launcher.Launch(ExitSpec{
		KeyID:     rec.ID,
		CallLink:  rec.CallLink,
		Secret:    rec.Secret,
		LicenseWS: p.licenseWS,
	})
	if err != nil {
		_ = p.store.Revoke(rec.ID) // frees the call link
		return access.KeyRecord{}, "", err
	}
	p.handles[rec.ID] = h
	return rec, key, nil
}

// Issue hands a buyer a key valid for ttl. It claims a warm exit when one is
// ready (no join latency); otherwise it cold-launches one.
func (p *Pool) Issue(ttl time.Duration) (access.KeyRecord, string, error) {
	p.mu.Lock()
	// Warm path: a pre-joined exit is ready by construction.
	if len(p.warmKeys) > 0 {
		id := p.warmKeys[0]
		p.warmKeys = p.warmKeys[1:]
		if err := p.store.Claim(id, ttl, time.Now()); err == nil {
			rec, _ := p.store.Get(id)
			if key, err := access.BuildKey(rec.CallLink, rec.Secret); err == nil {
				p.mu.Unlock()
				return rec, key, nil
			}
		} else {
			// Claim failed but the warm key is still valid and its exit still
			// running and joined. Return the id to the warm set so the handle
			// is not orphaned (Reconcile won't reap a still-valid key); a later
			// Issue can claim it. This buyer falls through to a cold launch.
			p.warmKeys = append(p.warmKeys, id)
		}
		// Claim/build failed: fall through to a cold launch.
	}
	rec, key, err := p.launch(ttl)
	ready, timeout := p.ready, p.readyTimeout
	p.mu.Unlock()
	if err != nil {
		return access.KeyRecord{}, "", err
	}
	// Cold path: wait for the exit to join its call before handing out the
	// key, so a buyer is never given a key whose exit is not up yet.
	if ready != nil && !waitReady(rec.ID, ready, timeout) {
		_ = p.Release(rec.ID)
		return access.KeyRecord{}, "", ErrNotReady
	}
	_ = p.store.MarkClaimed(rec.ID) // a cold sold key, not a warm one
	return rec, key, nil
}

// AdoptRunning rebuilds the pool's view from the launcher's running exits
// after a restart: valid keys' exits become managed handles, and valid
// unclaimed (warm) ones rejoin the warm set, so EnsureWarm does not launch
// duplicates and Release can stop adopted exits. Invalid ones are left for
// Reconcile to stop.
func (p *Pool) AdoptRunning(now time.Time) {
	handles, err := p.launcher.List()
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, h := range handles {
		rec, ok := p.store.Get(h.KeyID)
		if !ok || !rec.Valid(now) {
			continue
		}
		p.handles[h.KeyID] = h
		if !rec.Claimed {
			p.warmKeys = append(p.warmKeys, h.KeyID)
		}
	}
}

// waitReady polls ready(id) until it holds or timeout elapses.
func waitReady(id string, ready func(string) bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if ready(id) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(readyPoll)
	}
}

// Release stops the key's exit and revokes it, freeing its call link.
func (p *Pool) Release(keyID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if h, ok := p.handles[keyID]; ok {
		_ = p.launcher.Stop(h)
		delete(p.handles, keyID)
	}
	return p.store.Revoke(keyID)
}

// EnsureWarm tops the warm set back up to `warm` by pre-issuing keys and
// launching their exits so they are joined to a call before anyone buys.
func (p *Pool) EnsureWarm() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.warmKeys) < p.warm {
		rec, _, err := p.launch(warmTTL)
		if err != nil {
			return // pool exhausted or launch failing; try again later
		}
		p.warmKeys = append(p.warmKeys, rec.ID)
	}
}
