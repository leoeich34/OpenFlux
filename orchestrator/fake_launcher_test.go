package orchestrator

import (
	"errors"
	"testing"
)

func TestFakeLaunchRecordsSpecAndReturnsHandle(t *testing.T) {
	f := NewFakeLauncher()
	spec := ExitSpec{KeyID: "K1", CallLink: "call-a", Secret: "s", LicenseWS: "ws://x/exit/ws"}
	h, err := f.Launch(spec)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if h.ID == "" {
		t.Errorf("empty handle id")
	}
	if h.KeyID != "K1" {
		t.Errorf("handle KeyID = %q, want K1", h.KeyID)
	}
	if got := f.Launched(); len(got) != 1 || got[0] != spec {
		t.Errorf("Launched() = %+v, want [%+v]", got, spec)
	}
}

func TestFakeStopRemovesAndListReturnsLive(t *testing.T) {
	f := NewFakeLauncher()
	h1, _ := f.Launch(ExitSpec{KeyID: "K1", CallLink: "a"})
	h2, _ := f.Launch(ExitSpec{KeyID: "K2", CallLink: "b"})
	if err := f.Stop(h1); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	live, err := f.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(live) != 1 || live[0] != h2 {
		t.Errorf("List() = %+v, want [%+v]", live, h2)
	}
	if got := f.Stopped(); len(got) != 1 || got[0] != h1 {
		t.Errorf("Stopped() = %+v, want [%+v]", got, h1)
	}
}

func TestFakeStopUnknownErrors(t *testing.T) {
	f := NewFakeLauncher()
	if err := f.Stop(ExitHandle{ID: "nope"}); err == nil {
		t.Errorf("Stop(unknown) returned nil error")
	}
}

func TestFakeFailNextLaunch(t *testing.T) {
	f := NewFakeLauncher()
	boom := errors.New("boom")
	f.FailNext(boom)
	if _, err := f.Launch(ExitSpec{KeyID: "K1"}); !errors.Is(err, boom) {
		t.Fatalf("first Launch err = %v, want boom", err)
	}
	if _, err := f.Launch(ExitSpec{KeyID: "K2"}); err != nil {
		t.Fatalf("second Launch should succeed, got %v", err)
	}
	if live, _ := f.List(); len(live) != 1 {
		t.Errorf("failed launch left a live handle: %+v", live)
	}
}
