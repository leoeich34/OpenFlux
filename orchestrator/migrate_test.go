package orchestrator

import (
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
	stall    time.Duration
	loss     float64
	next     string
	nextErr  error
	started  []string // "name url"
	stopped  []string
	committed int
}

func (f *fakeMigrateDeps) Health() (time.Duration, float64) { return f.stall, f.loss }
func (f *fakeMigrateDeps) NextCall(string) (string, error)  { return f.next, f.nextErr }
func (f *fakeMigrateDeps) StartCall(name, url string) error {
	f.started = append(f.started, name+" "+url)
	return nil
}
func (f *fakeMigrateDeps) StopCall(name string) error { f.stopped = append(f.stopped, name); return nil }
func (f *fakeMigrateDeps) CommitCall(string) error    { f.committed++; return nil }

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
