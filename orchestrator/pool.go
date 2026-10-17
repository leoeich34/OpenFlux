package orchestrator

import (
	"sync"
	"time"

	"github.com/p1neappleXpress/OpenFlux/access"
)

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
	defer p.mu.Unlock()

	if len(p.warmKeys) > 0 {
		id := p.warmKeys[0]
		p.warmKeys = p.warmKeys[1:]
		if err := p.store.Claim(id, ttl, time.Now()); err == nil {
			rec, _ := p.store.Get(id)
			key, err := access.BuildKey(rec.CallLink, rec.Secret)
			if err == nil {
				return rec, key, nil
			}
		}
		// Claim/build failed: fall through to a cold launch.
	}
	return p.launch(ttl)
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
