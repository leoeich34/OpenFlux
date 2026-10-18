package orchestrator

import (
	"errors"
	"testing"
	"time"
)

func TestShouldMigrate(t *testing.T) {
	cases := []struct {
		name      string
		stall     time.Duration
		lossFrac  float64
		sinceLast time.Duration
		want      bool
	}{
		{"healthy", 0, 0.01, time.Hour, false},
		{"stalled, interval elapsed", 10 * time.Second, 0.0, time.Hour, true},
		{"high loss, interval elapsed", 0, 0.6, time.Hour, true},
		{"high loss but within min interval (anti-flap)", 0, 0.9, time.Second, false},
		{"stalled but within min interval (anti-flap)", 30 * time.Second, 0.0, time.Second, false},
		{"mild loss, elapsed", 0, 0.2, time.Hour, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := shouldMigrate(c.stall, c.lossFrac, c.sinceLast)
			if got != c.want {
				t.Errorf("shouldMigrate(%v,%v,%v) = %v, want %v", c.stall, c.lossFrac, c.sinceLast, got, c.want)
			}
			if got && reason == "" {
				t.Errorf("migrate true but empty reason")
			}
		})
	}
}

type fakeMigrateDeps struct {
	stall     time.Duration
	loss      float64
	next      string
	nextErr   error
	commitErr error
	started   []string // "name url"
	stopped   []string
	committed int
}

func (f *fakeMigrateDeps) Health() (time.Duration, float64) { return f.stall, f.loss }
func (f *fakeMigrateDeps) NextCall(string) (string, error)  { return f.next, f.nextErr }
func (f *fakeMigrateDeps) StartCall(name, url string) error {
	f.started = append(f.started, name+" "+url)
	return nil
}
func (f *fakeMigrateDeps) StopCall(name string) error { f.stopped = append(f.stopped, name); return nil }
func (f *fakeMigrateDeps) CommitCall(string) error    { f.committed++; return f.commitErr }

func TestControllerHealthyDoesNothing(t *testing.T) {
	d := &fakeMigrateDeps{stall: 0, loss: 0.01}
	m := NewMigrationController("K1", d, time.Now().Add(-time.Hour))
	m.SetCurrentName("telemost-boot")
	did, err := m.Step(time.Now())
	if err != nil || did {
		t.Fatalf("Step = (%v,%v), want (false,nil)", did, err)
	}
	if len(d.started) != 0 || d.committed != 0 {
		t.Errorf("healthy call migrated")
	}
}

func TestControllerMigratesInOrder(t *testing.T) {
	d := &fakeMigrateDeps{stall: 10 * time.Second, next: "https://telemost.yandex.ru/j/new"}
	m := NewMigrationController("K1", d, time.Now().Add(-time.Hour))
	m.SetCurrentName("telemost-boot")
	did, err := m.Step(time.Now())
	if err != nil || !did {
		t.Fatalf("Step = (%v,%v), want (true,nil)", did, err)
	}
	if len(d.started) != 1 || d.started[0] != "telemost-migrated-1 https://telemost.yandex.ru/j/new" {
		t.Errorf("started = %v", d.started)
	}
	if len(d.stopped) != 1 || d.stopped[0] != "telemost-boot" {
		t.Errorf("stopped old = %v, want [telemost-boot]", d.stopped)
	}
	if d.committed != 1 {
		t.Errorf("committed = %d, want 1", d.committed)
	}
	// anti-flap: an immediate second Step does not migrate again
	did2, _ := m.Step(time.Now())
	if did2 {
		t.Errorf("second Step migrated within min interval")
	}
}

// TestControllerCommitFailureStillAdvances guards against a leak: once the new
// call is up and the old one is stopped, the controller must treat the new
// call as current even if CommitCall fails. Otherwise curName still names the
// already-stopped previous call and lastMigrate is stale, so the next Step
// fires immediately, stops the old (already-stopped) name again, and leaks the
// just-started transport.
func TestControllerCommitFailureStillAdvances(t *testing.T) {
	d := &fakeMigrateDeps{
		stall:     10 * time.Second,
		next:      "https://telemost.yandex.ru/j/new",
		commitErr: errors.New("commit boom"),
	}
	m := NewMigrationController("K1", d, time.Now().Add(-time.Hour))
	m.SetCurrentName("telemost-boot")

	did, err := m.Step(time.Now())
	if !did {
		t.Fatalf("Step did = false, want true (migration happened)")
	}
	if err == nil {
		t.Fatalf("Step err = nil, want the commit error surfaced")
	}
	// The new call is now carrying traffic: it must be current.
	if m.curName != "telemost-migrated-1" {
		t.Errorf("curName = %q, want telemost-migrated-1", m.curName)
	}
	// The old transport was torn down exactly once.
	if len(d.stopped) != 1 || d.stopped[0] != "telemost-boot" {
		t.Errorf("stopped = %v, want [telemost-boot]", d.stopped)
	}
	// Anti-flap must hold: an immediate second Step must not re-migrate
	// (lastMigrate advanced), which would start telemost-migrated-2 and leak
	// telemost-migrated-1.
	did2, _ := m.Step(time.Now())
	if did2 {
		t.Errorf("second Step migrated within min interval after failed commit")
	}
	if len(d.started) != 1 {
		t.Errorf("started = %v, want exactly one transport brought up", d.started)
	}
	if len(d.stopped) != 1 {
		t.Errorf("stopped = %v, want the old transport torn down exactly once", d.stopped)
	}
}

func TestStallFromCountsOnlyAfterFirstByte(t *testing.T) {
	var lastBytes uint64
	var lastAdvance time.Time
	var seen bool
	t0 := time.Now()
	// No bytes yet, long wait -> still 0 (idle, not dead)
	if d := stallFrom(0, t0.Add(time.Hour), &lastBytes, &lastAdvance, &seen); d != 0 {
		t.Errorf("pre-traffic stall = %v, want 0", d)
	}
	// First byte arrives -> 0, clock starts
	if d := stallFrom(100, t0.Add(time.Hour), &lastBytes, &lastAdvance, &seen); d != 0 {
		t.Errorf("first-byte stall = %v, want 0", d)
	}
	// No progress for 30s -> stall ~30s
	if d := stallFrom(100, t0.Add(time.Hour+30*time.Second), &lastBytes, &lastAdvance, &seen); d < 29*time.Second {
		t.Errorf("stall after idle = %v, want ~30s", d)
	}
	// More bytes -> resets to 0
	if d := stallFrom(200, t0.Add(time.Hour+40*time.Second), &lastBytes, &lastAdvance, &seen); d != 0 {
		t.Errorf("stall after new bytes = %v, want 0", d)
	}
}
