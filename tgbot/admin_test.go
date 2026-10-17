package tgbot

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminIssuePostsTTLAndReturnsKey(t *testing.T) {
	var gotTTL float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/issue" {
			t.Errorf("path %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		gotTTL, _ = m["ttlSeconds"].(float64)
		w.Write([]byte(`{"id":"K1","key":"openflux://v1/abc","callLink":"https://t/j/1","expiresAt":"2030-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	a := NewAdminClient(srv.URL)
	id, key, err := a.Issue(30)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if int(gotTTL) != 30*86400 {
		t.Errorf("ttlSeconds = %v, want %d", gotTTL, 30*86400)
	}
	if id != "K1" || !strings.HasPrefix(key, "openflux://") {
		t.Errorf("id=%q key=%q", id, key)
	}
}

func TestAdminIssuePoolExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "pool", http.StatusConflict)
	}))
	defer srv.Close()
	a := NewAdminClient(srv.URL)
	if _, _, err := a.Issue(30); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("want ErrPoolExhausted, got %v", err)
	}
}

func TestAdminRevokeNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	a := NewAdminClient(srv.URL)
	if err := a.Revoke("X"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAdminListParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":"K1","status":"active","callLink":"https://t/j/1","expiresAt":"2030-01-01T00:00:00Z"}]`))
	}))
	defer srv.Close()
	a := NewAdminClient(srv.URL)
	ks, err := a.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ks) != 1 || ks[0].ID != "K1" || ks[0].Status != "active" {
		t.Errorf("List = %+v", ks)
	}
}

func TestAdminExtendOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"extended"}`))
	}))
	defer srv.Close()
	a := NewAdminClient(srv.URL)
	if err := a.Extend("K1", 7); err != nil {
		t.Fatalf("Extend: %v", err)
	}
}
