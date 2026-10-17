package main

import (
	"testing"

	"github.com/p1neappleXpress/OpenFlux/tgbot"
)

func TestNextOffsetAdvancesPastHighest(t *testing.T) {
	ups := []tgbot.Update{{UpdateID: 42}, {UpdateID: 45}, {UpdateID: 43}}
	if got := nextOffset(ups, 0); got != 46 {
		t.Errorf("nextOffset = %d, want 46", got)
	}
}

func TestNextOffsetEmptyKeepsCurrent(t *testing.T) {
	if got := nextOffset(nil, 7); got != 7 {
		t.Errorf("nextOffset(empty) = %d, want 7", got)
	}
}

func TestParseAdmins(t *testing.T) {
	got, err := parseAdmins(" 7, 42 ,100 ")
	if err != nil {
		t.Fatalf("parseAdmins: %v", err)
	}
	want := []int64{7, 42, 100}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if _, err := parseAdmins("7,abc"); err == nil {
		t.Errorf("parseAdmins with a bad id should error")
	}
	if _, err := parseAdmins(""); err == nil {
		t.Errorf("empty admins should error")
	}
}
