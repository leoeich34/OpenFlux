package tgbot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestBot(t *testing.T, adminHandler http.HandlerFunc, allow []int64) (*Bot, *FakeClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(adminHandler)
	t.Cleanup(srv.Close)
	fc := NewFakeClient()
	b := NewBot(fc, NewAdminClient(srv.URL), allow)
	return b, fc, srv
}

func TestHandleNonAdminRefusedNoAdminCall(t *testing.T) {
	called := false
	b, fc, _ := newTestBot(t, func(w http.ResponseWriter, r *http.Request) { called = true }, []int64{7})
	b.Handle(Update{ChatID: 100, UserID: 9, Text: "/issue 30"})
	if called {
		t.Errorf("admin API was called for a non-admin")
	}
	sent := fc.Sent()
	if len(sent) != 1 || !strings.Contains(strings.ToLower(sent[0].Text), "not authorized") {
		t.Errorf("reply = %+v, want a 'not authorized' message", sent)
	}
}

func TestHandleIssueFromAdminReturnsKey(t *testing.T) {
	b, fc, _ := newTestBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"K1","key":"openflux://v1/abc","callLink":"https://t/j/1","expiresAt":"2030-01-01T00:00:00Z"}`))
	}, []int64{7})
	b.Handle(Update{ChatID: 100, UserID: 7, Text: "/issue 30"})
	sent := fc.Sent()
	if len(sent) != 1 {
		t.Fatalf("want 1 reply, got %d", len(sent))
	}
	if !strings.Contains(sent[0].Text, "openflux://v1/abc") || !strings.Contains(sent[0].Text, "K1") {
		t.Errorf("issue reply missing key/id: %q", sent[0].Text)
	}
}

func TestHandleMalformedCommandNoAdminCall(t *testing.T) {
	called := false
	b, fc, _ := newTestBot(t, func(w http.ResponseWriter, r *http.Request) { called = true }, []int64{7})
	b.Handle(Update{ChatID: 100, UserID: 7, Text: "/issue abc"})
	if called {
		t.Errorf("admin API called for a malformed command")
	}
	sent := fc.Sent()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "usage") {
		t.Errorf("reply = %+v, want usage", sent)
	}
}

func TestHandlePoolExhaustedSurfaced(t *testing.T) {
	b, fc, _ := newTestBot(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "pool", http.StatusConflict)
	}, []int64{7})
	b.Handle(Update{ChatID: 100, UserID: 7, Text: "/issue 30"})
	sent := fc.Sent()
	if len(sent) != 1 || !strings.Contains(strings.ToLower(sent[0].Text), "pool") {
		t.Errorf("reply = %+v, want a pool-exhausted message", sent)
	}
}

func TestHandleExactlyOneReplyPerUpdate(t *testing.T) {
	b, fc, _ := newTestBot(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}, []int64{7})
	b.Handle(Update{ChatID: 100, UserID: 7, Text: "/list"})
	if len(fc.Sent()) != 1 {
		t.Errorf("want exactly 1 reply, got %d", len(fc.Sent()))
	}
}
