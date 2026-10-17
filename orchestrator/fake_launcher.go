package orchestrator

import (
	"fmt"
	"sync"
)

// FakeLauncher is an in-memory ExitLauncher for tests. It records every
// launched spec and stopped handle, and can be told to fail the next launch.
type FakeLauncher struct {
	mu       sync.Mutex
	next     int
	live     []ExitHandle
	launched []ExitSpec
	stopped  []ExitHandle
	failNext error
}

// NewFakeLauncher returns an empty FakeLauncher.
func NewFakeLauncher() *FakeLauncher { return &FakeLauncher{} }

// FailNext makes the next Launch return err (once).
func (f *FakeLauncher) FailNext(err error) {
	f.mu.Lock()
	f.failNext = err
	f.mu.Unlock()
}

// Launch records spec and returns a new live handle.
func (f *FakeLauncher) Launch(spec ExitSpec) (ExitHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failNext; err != nil {
		f.failNext = nil
		return ExitHandle{}, err
	}
	f.next++
	h := ExitHandle{ID: fmt.Sprintf("fake-%d", f.next), KeyID: spec.KeyID}
	f.live = append(f.live, h)
	f.launched = append(f.launched, spec)
	return h, nil
}

// Stop removes a live handle; unknown handles are an error.
func (f *FakeLauncher) Stop(h ExitHandle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, l := range f.live {
		if l.ID == h.ID {
			f.live = append(f.live[:i], f.live[i+1:]...)
			f.stopped = append(f.stopped, l)
			return nil
		}
	}
	return fmt.Errorf("fake launcher: unknown handle %q", h.ID)
}

// List returns the live handles.
func (f *FakeLauncher) List() ([]ExitHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ExitHandle(nil), f.live...), nil
}

// Launched returns every spec passed to a successful Launch, in order.
func (f *FakeLauncher) Launched() []ExitSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ExitSpec(nil), f.launched...)
}

// Stopped returns every handle stopped, in order.
func (f *FakeLauncher) Stopped() []ExitHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ExitHandle(nil), f.stopped...)
}
