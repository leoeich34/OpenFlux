package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssueCommandHitsAdminAPIAndPrintsKey(t *testing.T) {
	const fakeKey = "openflux://v1/ZmFrZS1rZXk"
	var gotPath string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"abc123","key":"` + fakeKey + `","callLink":"https://telemost.yandex.ru/j/1","expiresAt":"2030-01-01T00:00:00Z"}`))
	}))
	defer stub.Close()

	var out bytes.Buffer
	code := run([]string{"issue", "--admin", stub.URL, "--days", "30"}, &out)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if gotPath != "/admin/issue" {
		t.Errorf("hit %q, want /admin/issue", gotPath)
	}
	if !strings.Contains(out.String(), fakeKey) {
		t.Errorf("output does not contain the key:\n%s", out.String())
	}
}
