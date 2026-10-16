//go:build smoke

package testdata

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/p1neappleXpress/OpenFlux/access"
)

// TestIssueConnectRevokeFlow exercises the whole control plane with no real
// Telemost: issue a key via the admin API, connect an exit license client
// with the issued id, revoke via the admin API, and assert the exit's onStop
// fires.
func TestIssueConnectRevokeFlow(t *testing.T) {
	st, err := access.Open(filepath.Join(t.TempDir(), "s.json"), []string{
		"https://telemost.yandex.ru/j/a", "https://telemost.yandex.ru/j/b",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	srv := access.NewServer(st)
	admin := httptest.NewServer(srv.AdminHandler())
	defer admin.Close()
	exit := httptest.NewServer(srv.ExitHandler())
	defer exit.Close()

	// Issue.
	body, _ := json.Marshal(map[string]int{"ttlSeconds": 3600})
	resp, err := http.Post(admin.URL+"/admin/issue", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	var issued struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	json.NewDecoder(resp.Body).Decode(&issued)
	resp.Body.Close()
	if issued.ID == "" || !strings.HasPrefix(issued.Key, "openflux://") {
		t.Fatalf("bad issue result: %+v", issued)
	}

	// Connect an exit license client for the issued id.
	wsURL := "ws" + strings.TrimPrefix(exit.URL, "http") + "/exit/ws"
	stopped := make(chan string, 1)
	lc, err := access.Dial(wsURL, issued.ID, 500*time.Millisecond, func(reason string) { stopped <- reason })
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer lc.Close()

	// Revoke.
	rbody, _ := json.Marshal(map[string]string{"id": issued.ID})
	rresp, err := http.Post(admin.URL+"/admin/revoke", "application/json", bytes.NewReader(rbody))
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	rresp.Body.Close()

	select {
	case reason := <-stopped:
		if reason == "" {
			t.Errorf("empty stop reason")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("exit was not stopped after revoke")
	}
}
