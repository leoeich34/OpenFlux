package tgbot

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClientGetUpdatesParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			t.Errorf("path = %s, want .../getUpdates", r.URL.Path)
		}
		w.Write([]byte(`{"ok":true,"result":[
			{"update_id":42,"message":{"chat":{"id":100},"from":{"id":7},"text":"/list"}},
			{"update_id":43,"message":{"chat":{"id":100},"from":{"id":8},"text":"/help"}}
		]}`))
	}))
	defer srv.Close()

	c := NewHTTPClient("TOK", WithBaseURL(srv.URL))
	ups, err := c.GetUpdates(0, 0)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(ups) != 2 {
		t.Fatalf("got %d updates, want 2", len(ups))
	}
	if ups[0].UpdateID != 42 || ups[0].ChatID != 100 || ups[0].UserID != 7 || ups[0].Text != "/list" {
		t.Errorf("update[0] = %+v", ups[0])
	}
	if ups[1].UpdateID != 43 || ups[1].UserID != 8 || ups[1].Text != "/help" {
		t.Errorf("update[1] = %+v", ups[1])
	}
}

func TestHTTPClientSendMessagePostsChatAndText(t *testing.T) {
	var gotChat float64
	var gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Errorf("path = %s, want .../sendMessage", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(body, &m)
		gotChat, _ = m["chat_id"].(float64)
		gotText, _ = m["text"].(string)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewHTTPClient("TOK", WithBaseURL(srv.URL))
	if err := c.SendMessage(100, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if int64(gotChat) != 100 || gotText != "hello" {
		t.Errorf("sent chat=%v text=%q", gotChat, gotText)
	}
}

func TestHTTPClientTokenNotInPath(t *testing.T) {
	// The token belongs in the bot<token> path segment; ensure it is used
	// (the server path contains it) so auth actually happens.
	var sawToken bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "SECRET") {
			sawToken = true
		}
		w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()
	c := NewHTTPClient("SECRET", WithBaseURL(srv.URL))
	c.GetUpdates(0, 0)
	if !sawToken {
		t.Errorf("token not placed in the request path")
	}
}

func TestFakeClientQueuesAndRecords(t *testing.T) {
	f := NewFakeClient()
	f.Queue(Update{UpdateID: 1, ChatID: 5, UserID: 9, Text: "/list"})
	ups, err := f.GetUpdates(0, 0)
	if err != nil || len(ups) != 1 || ups[0].Text != "/list" {
		t.Fatalf("GetUpdates = %v, %v", ups, err)
	}
	// drained after read
	ups2, _ := f.GetUpdates(0, 0)
	if len(ups2) != 0 {
		t.Errorf("updates not drained: %v", ups2)
	}
	if err := f.SendMessage(5, "hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := f.Sent(); len(got) != 1 || got[0].ChatID != 5 || got[0].Text != "hi" {
		t.Errorf("Sent = %+v", got)
	}
}

func TestGetUpdatesErrorRedactsToken(t *testing.T) {
	// Point at a closed port so the GET fails at the transport layer; the
	// returned error must not contain the token (it is in the URL path).
	c := NewHTTPClient("SECRET-TOKEN-123", WithBaseURL("http://127.0.0.1:1"))
	_, err := c.GetUpdates(0, 0)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN-123") {
		t.Errorf("token leaked into error: %v", err)
	}
}

func TestGetUpdatesNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
	}))
	defer srv.Close()
	c := NewHTTPClient("TOK", WithBaseURL(srv.URL))
	if _, err := c.GetUpdates(0, 0); err == nil {
		t.Error("401 getUpdates should return an error, not an empty slice")
	}
}
