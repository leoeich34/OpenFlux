package orchestrator

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/p1neappleXpress/OpenFlux/access"
)

func TestReconcileStopsStaleExits(t *testing.T) {
	now := time.Now()
	st, err := access.Open(filepath.Join(t.TempDir(), "s.json"), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f := NewFakeLauncher()

	valid, _ := st.Issue(10*time.Hour, now)
	expired, _ := st.Issue(time.Hour, now) // expires before the reconcile time
	revoked, _ := st.Issue(10*time.Hour, now)
	_ = st.Revoke(revoked.ID)

	for _, id := range []string{valid.ID, expired.ID, revoked.ID, "ghost"} {
		f.Launch(ExitSpec{KeyID: id})
	}

	stopped, err := Reconcile(st, f, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	sort.Strings(stopped)
	want := []string{expired.ID, revoked.ID, "ghost"}
	sort.Strings(want)
	if len(stopped) != len(want) {
		t.Fatalf("stopped %v, want %v", stopped, want)
	}
	for i := range want {
		if stopped[i] != want[i] {
			t.Fatalf("stopped %v, want %v", stopped, want)
		}
	}
	live, _ := f.List()
	if len(live) != 1 || live[0].KeyID != valid.ID {
		t.Errorf("live = %+v, want only the valid key %q", live, valid.ID)
	}
}
