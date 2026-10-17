package orchestrator

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/p1neappleXpress/OpenFlux/access"
	"github.com/p1neappleXpress/OpenFlux/share"
)

func newPool(t *testing.T, pool []string, warm int) (*Pool, *FakeLauncher, *access.Store) {
	t.Helper()
	st, err := access.Open(filepath.Join(t.TempDir(), "s.json"), pool)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f := NewFakeLauncher()
	p := NewPool(st, f, "ws://hub/exit/ws", warm)
	return p, f, st
}

func TestIssueLaunchesExitAndReturnsDecodableKey(t *testing.T) {
	p, f, _ := newPool(t, []string{"https://telemost.yandex.ru/j/1"}, 0)
	rec, key, err := p.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	launched := f.Launched()
	if len(launched) != 1 {
		t.Fatalf("launched %d exits, want 1", len(launched))
	}
	if launched[0].KeyID != rec.ID || launched[0].CallLink != rec.CallLink {
		t.Errorf("exit not bound to issued key: %+v vs %+v", launched[0], rec)
	}
	cfg, err := share.Decode(key)
	if err != nil {
		t.Fatalf("Decode key: %v", err)
	}
	if cfg.Transports[0].URL != rec.CallLink {
		t.Errorf("key call link %q != record %q", cfg.Transports[0].URL, rec.CallLink)
	}
}

func TestIssueLaunchFailureFreesLink(t *testing.T) {
	p, f, _ := newPool(t, []string{"only-one"}, 0)
	f.FailNext(errors.New("no image"))
	if _, _, err := p.Issue(time.Hour); err == nil {
		t.Fatalf("Issue should fail when launch fails")
	}
	// The single pool link must be free again (the failed key was revoked).
	if _, _, err := p.Issue(time.Hour); err != nil {
		t.Fatalf("re-Issue after launch failure: %v", err)
	}
}

func TestReleaseStopsExitAndFreesLink(t *testing.T) {
	p, f, _ := newPool(t, []string{"only-one"}, 0)
	rec, _, err := p.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := p.Release(rec.ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if len(f.Stopped()) != 1 {
		t.Errorf("Release did not stop the exit")
	}
	if _, _, err := p.Issue(time.Hour); err != nil {
		t.Fatalf("link not freed after Release: %v", err)
	}
}

func TestEnsureWarmKeepsWarmExits(t *testing.T) {
	p, f, _ := newPool(t, []string{"a", "b", "c"}, 2)
	p.EnsureWarm()
	if n := len(f.Launched()); n != 2 {
		t.Fatalf("EnsureWarm launched %d, want 2", n)
	}
	// Idempotent: already at target, no new launches.
	p.EnsureWarm()
	if n := len(f.Launched()); n != 2 {
		t.Errorf("second EnsureWarm launched more: %d", n)
	}
}

func TestIssueClaimsWarmExitWithoutNewLaunch(t *testing.T) {
	p, f, _ := newPool(t, []string{"a", "b"}, 1)
	p.EnsureWarm() // 1 warm exit launched and joined ahead of time
	before := len(f.Launched())
	rec, key, err := p.Issue(time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(f.Launched()) != before {
		t.Errorf("Issue launched a new exit instead of claiming the warm one")
	}
	if !strings.HasPrefix(key, "openflux://") {
		t.Errorf("claimed key is not an openflux link: %q", key)
	}
	if !rec.Valid(time.Now()) {
		t.Errorf("claimed key not valid")
	}
}
