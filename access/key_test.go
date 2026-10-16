package access

import (
	"testing"

	"github.com/p1neappleXpress/OpenFlux/share"
)

func TestBuildKeyRoundTrips(t *testing.T) {
	const callLink = "https://telemost.yandex.ru/j/123"
	const secret = "secret-at-least-16-chars"
	link, err := BuildKey(callLink, secret)
	if err != nil {
		t.Fatalf("BuildKey: %v", err)
	}
	cfg, err := share.Decode(link)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(cfg.Transports) != 1 {
		t.Fatalf("want 1 transport, got %d", len(cfg.Transports))
	}
	tr := cfg.Transports[0]
	if tr.Type != "telemost" {
		t.Errorf("type = %q, want telemost", tr.Type)
	}
	if tr.URL != callLink {
		t.Errorf("url = %q, want %q", tr.URL, callLink)
	}
	if cfg.Secret != secret {
		t.Errorf("secret = %q, want %q", cfg.Secret, secret)
	}
	if cfg.Context != callLink {
		t.Errorf("context = %q, want %q", cfg.Context, callLink)
	}
}

func TestBuildKeyIsValidShareConfig(t *testing.T) {
	link, err := BuildKey("https://telemost.yandex.ru/j/123", "secret-at-least-16-chars")
	if err != nil {
		t.Fatalf("BuildKey: %v", err)
	}
	cfg, err := share.Decode(link)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}
