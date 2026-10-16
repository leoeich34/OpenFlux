package access

import (
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T, pool []string) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "store.json"), pool)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestIssueAssignsFreeCallUniqueIDAndSecret(t *testing.T) {
	now := time.Now()
	s := newStore(t, []string{"call-a", "call-b"})
	r1, err := s.Issue(time.Hour, now)
	if err != nil {
		t.Fatalf("Issue 1: %v", err)
	}
	r2, err := s.Issue(time.Hour, now)
	if err != nil {
		t.Fatalf("Issue 2: %v", err)
	}
	if r1.ID == r2.ID {
		t.Errorf("IDs not unique: %q", r1.ID)
	}
	if r1.Secret == r2.Secret {
		t.Errorf("secrets not unique")
	}
	if r1.Secret == "" || r2.Secret == "" {
		t.Errorf("empty secret")
	}
	if r1.CallLink == r2.CallLink {
		t.Errorf("same call link assigned twice: %q", r1.CallLink)
	}
	for _, r := range []KeyRecord{r1, r2} {
		if r.CallLink != "call-a" && r.CallLink != "call-b" {
			t.Errorf("call link %q not from pool", r.CallLink)
		}
	}
}

func TestIssuePoolExhausted(t *testing.T) {
	now := time.Now()
	s := newStore(t, []string{"only-one"})
	if _, err := s.Issue(time.Hour, now); err != nil {
		t.Fatalf("first Issue: %v", err)
	}
	if _, err := s.Issue(time.Hour, now); err != ErrPoolExhausted {
		t.Fatalf("want ErrPoolExhausted, got %v", err)
	}
}

func TestValidTrueThenFalseAfterRevoke(t *testing.T) {
	now := time.Now()
	s := newStore(t, []string{"call-a"})
	r, _ := s.Issue(time.Hour, now)
	if !r.Valid(now) {
		t.Fatalf("fresh key should be valid")
	}
	if err := s.Revoke(r.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, ok := s.Get(r.ID)
	if !ok {
		t.Fatalf("Get after revoke: not found")
	}
	if got.Status != StatusRevoked {
		t.Errorf("status = %q, want revoked", got.Status)
	}
	if got.Valid(now) {
		t.Errorf("revoked key should not be valid")
	}
}

func TestRevokeFreesCallLinkForReuse(t *testing.T) {
	now := time.Now()
	s := newStore(t, []string{"only-one"})
	r1, _ := s.Issue(time.Hour, now)
	if err := s.Revoke(r1.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	r2, err := s.Issue(time.Hour, now)
	if err != nil {
		t.Fatalf("re-Issue after revoke: %v", err)
	}
	if r2.CallLink != "only-one" {
		t.Errorf("freed call link not reused: %q", r2.CallLink)
	}
}

func TestRevokeUnknownIsErrNotFound_AlreadyRevokedIsNoError(t *testing.T) {
	now := time.Now()
	s := newStore(t, []string{"call-a"})
	if err := s.Revoke("nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	r, _ := s.Issue(time.Hour, now)
	if err := s.Revoke(r.ID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := s.Revoke(r.ID); err != nil {
		t.Fatalf("second revoke should be idempotent, got %v", err)
	}
}

func TestExpiryMakesInvalid(t *testing.T) {
	now := time.Now()
	s := newStore(t, []string{"call-a"})
	r, _ := s.Issue(time.Hour, now)
	if r.Valid(now.Add(2 * time.Hour)) {
		t.Errorf("key should be invalid after expiry")
	}
}

func TestExtendMovesExpiry(t *testing.T) {
	now := time.Now()
	s := newStore(t, []string{"call-a"})
	r, _ := s.Issue(time.Hour, now)
	if err := s.Extend(r.ID, time.Hour); err != nil {
		t.Fatalf("Extend: %v", err)
	}
	got, _ := s.Get(r.ID)
	if !got.Valid(now.Add(90 * time.Minute)) {
		t.Errorf("extended key should still be valid at now+90m")
	}
	if err := s.Extend("nope", time.Hour); err != ErrNotFound {
		t.Errorf("Extend unknown: want ErrNotFound, got %v", err)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "store.json")
	s1, err := Open(path, []string{"call-a"})
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	r, _ := s1.Issue(time.Hour, now)

	s2, err := Open(path, []string{"call-a"})
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	got, ok := s2.Get(r.ID)
	if !ok {
		t.Fatalf("record not persisted")
	}
	if got.Secret != r.Secret || got.CallLink != r.CallLink {
		t.Errorf("persisted record mismatch")
	}
}
