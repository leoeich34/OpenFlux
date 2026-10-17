// Package access is the control plane for OpenFlux access keys: a
// file-backed key store, an openflux:// key builder, a control server
// (admin API + exit WebSocket hub) and a fail-closed exit-side license
// client. See docs/superpowers/specs/2026-10-16-access-key-service-design.md.
package access

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// Status is a key's lifecycle state.
type Status string

const (
	StatusActive  Status = "active"
	StatusRevoked Status = "revoked"
)

// Errors returned by the store.
var (
	ErrPoolExhausted = errors.New("access: no free call link in pool")
	ErrNotFound      = errors.New("access: key not found")
)

// KeyRecord is one issued access key.
type KeyRecord struct {
	ID        string    `json:"id"`
	Secret    string    `json:"secret"`
	CallLink  string    `json:"callLink"`
	Status    Status    `json:"status"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	// NextCallLink is a pool link reserved for an in-progress call migration;
	// it is held (not reissued to anyone else) until the migration commits.
	NextCallLink string `json:"nextCallLink,omitempty"`
	// Claimed is true once a buyer has taken this key (a cold sold issue or a
	// warm key handed out). It lets the pool tell warm (unclaimed) exits from
	// sold ones when adopting running containers after a restart.
	Claimed bool `json:"claimed,omitempty"`
}

// Valid reports whether the key may carry traffic at time now.
func (r KeyRecord) Valid(now time.Time) bool {
	return r.Status == StatusActive && now.Before(r.ExpiresAt)
}

// Store is a JSON-file-backed set of keys plus a static pool of Telemost
// call links to assign from. Safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	pool []string
	keys map[string]KeyRecord
}

// Open loads (or creates) the store at path. pool is the static list of
// Telemost call links Issue assigns from.
func Open(path string, pool []string) (*Store, error) {
	s := &Store{path: path, pool: pool, keys: map[string]KeyRecord{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &s.keys); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// assignedLinks returns the set of call links held by keys still valid at
// now. A revoked or expired key no longer holds its link, so the link is
// free to reissue (the spec recycles calls on revoke and on expiry).
func (s *Store) assignedLinks(now time.Time) map[string]bool {
	used := map[string]bool{}
	for _, r := range s.keys {
		if r.Valid(now) {
			used[r.CallLink] = true
			if r.NextCallLink != "" {
				used[r.NextCallLink] = true
			}
		}
	}
	return used
}

// freeLink returns a pool link not held by any key valid at now, or "".
func (s *Store) freeLink(now time.Time) string {
	used := s.assignedLinks(now)
	for _, link := range s.pool {
		if !used[link] {
			return link
		}
	}
	return ""
}

// Issue allocates a free call link and a fresh key valid for ttl from now.
func (s *Store) Issue(ttl time.Duration, now time.Time) (KeyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	link := s.freeLink(now)
	if link == "" {
		return KeyRecord{}, ErrPoolExhausted
	}
	r := KeyRecord{
		ID:        randID(),
		Secret:    randSecret(),
		CallLink:  link,
		Status:    StatusActive,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
	}
	s.keys[r.ID] = r
	if err := s.persist(); err != nil {
		delete(s.keys, r.ID)
		return KeyRecord{}, err
	}
	return r, nil
}

// Get returns the record for id.
func (s *Store) Get(id string) (KeyRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.keys[id]
	return r, ok
}

// Revoke marks a key revoked and frees its call link. It is idempotent for
// an already-revoked key, and returns ErrNotFound for an unknown id.
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.keys[id]
	if !ok {
		return ErrNotFound
	}
	if r.Status == StatusRevoked {
		return nil
	}
	r.Status = StatusRevoked
	s.keys[id] = r
	return s.persist()
}

// Claim re-arms a pre-issued (warm) key for a buyer: it sets the key active
// and its expiry to now+ttl. Used when the pool hands out a warm exit whose
// key was generated ahead of time. ErrNotFound for an unknown id.
func (s *Store) Claim(id string, ttl time.Duration, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.keys[id]
	if !ok {
		return ErrNotFound
	}
	r.Status = StatusActive
	r.IssuedAt = now
	r.ExpiresAt = now.Add(ttl)
	r.Claimed = true
	s.keys[id] = r
	return s.persist()
}

// MarkClaimed records that a buyer has taken the key (used for a cold sold
// issue, where the key was not pre-issued warm).
func (s *Store) MarkClaimed(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.keys[id]
	if !ok {
		return ErrNotFound
	}
	r.Claimed = true
	s.keys[id] = r
	return s.persist()
}

// ReserveNextCall reserves a free pool link as the key's migration target and
// returns it. The link is held (counted as assigned) until CommitNextCall or
// the key is released. ErrNotFound for an unknown id, ErrPoolExhausted when no
// link is free.
func (s *Store) ReserveNextCall(id string, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.keys[id]
	if !ok {
		return "", ErrNotFound
	}
	link := s.freeLink(now)
	if link == "" {
		return "", ErrPoolExhausted
	}
	r.NextCallLink = link
	s.keys[id] = r
	if err := s.persist(); err != nil {
		return "", err
	}
	return link, nil
}

// CommitNextCall promotes a reserved migration target to the key's current
// call link, freeing the old one. A no-op (nil) when nothing is reserved.
func (s *Store) CommitNextCall(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.keys[id]
	if !ok {
		return ErrNotFound
	}
	if r.NextCallLink != "" {
		r.CallLink = r.NextCallLink
		r.NextCallLink = ""
		s.keys[id] = r
		return s.persist()
	}
	return nil
}

// Extend moves a key's expiry out by d.
func (s *Store) Extend(id string, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.keys[id]
	if !ok {
		return ErrNotFound
	}
	r.ExpiresAt = r.ExpiresAt.Add(d)
	s.keys[id] = r
	return s.persist()
}

// List returns every key record.
func (s *Store) List() []KeyRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KeyRecord, 0, len(s.keys))
	for _, r := range s.keys {
		out = append(out, r)
	}
	return out
}

// persist writes the key map atomically (temp file + rename). Caller holds mu.
func (s *Store) persist() error {
	data, err := json.MarshalIndent(s.keys, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// randID is a short, URL-safe key id.
func randID() string {
	b := make([]byte, 7)
	_, _ = rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

// randSecret is a 32-byte base64url shared secret.
func randSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
