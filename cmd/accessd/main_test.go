package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadPoolFileTrimsAndSkipsBlanks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.txt")
	content := "  https://telemost.yandex.ru/j/1  \n" +
		"\n" +
		"# a comment\n" +
		"https://telemost.yandex.ru/j/2\n" +
		"   \n" +
		"# another\n" +
		"https://telemost.yandex.ru/j/3\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadPool(path)
	if err != nil {
		t.Fatalf("loadPool: %v", err)
	}
	want := []string{
		"https://telemost.yandex.ru/j/1",
		"https://telemost.yandex.ru/j/2",
		"https://telemost.yandex.ru/j/3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080": true,
		"[::1]:8080":     true,
		"localhost:8080": true,
		"0.0.0.0:8080":   false,
		"192.168.0.5:80": false,
	}
	for addr, want := range cases {
		if got := isLoopbackAddr(addr); got != want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}
