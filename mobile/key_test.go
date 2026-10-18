package mobile

import (
	"encoding/json"
	"testing"

	"github.com/p1neappleXpress/OpenFlux/share"
)

// keyLink builds a known-good openflux:// access key the way accessd does:
// a Session-capable config carrying the per-user secret.
func keyLink(t *testing.T) (link, secret, doc string) {
	t.Helper()
	secret = "access-key-secret-at-least-16"
	doc = "https://docs.example/telemost-room"
	link, err := share.MakeLink(share.Config{
		Negotiate:  true,
		Secret:     secret,
		Context:    doc,
		Transports: []share.Transport{{Type: "telemost", URL: doc, Priority: 100}},
	})
	if err != nil {
		t.Fatalf("build key: %v", err)
	}
	return link, secret, doc
}

func TestKeySpecsDecodesSessionAndSecret(t *testing.T) {
	link, secret, doc := keyLink(t)

	specsJSON, gotSecret, err := KeySpecs(link)
	if err != nil {
		t.Fatalf("KeySpecs: %v", err)
	}
	if gotSecret != secret {
		t.Fatalf("secret = %q, want %q", gotSecret, secret)
	}

	var got struct {
		Context    string `json:"context"`
		Transports []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"transports"`
	}
	if err := json.Unmarshal([]byte(specsJSON), &got); err != nil {
		t.Fatalf("specsJSON not valid: %v (%s)", err, specsJSON)
	}
	if got.Context != doc {
		t.Fatalf("context = %q, want %q", got.Context, doc)
	}
	if len(got.Transports) != 1 || got.Transports[0].Type != "telemost" || got.Transports[0].URL != doc {
		t.Fatalf("transports = %+v, want one telemost at %q", got.Transports, doc)
	}
}

func TestKeySpecsRejectsGarbage(t *testing.T) {
	if _, _, err := KeySpecs("not a link"); err == nil {
		t.Fatal("KeySpecs on garbage returned nil error")
	}
}

func TestValidateKey(t *testing.T) {
	link, _, _ := keyLink(t)
	if err := ValidateKey(link); err != nil {
		t.Fatalf("ValidateKey on a good key: %v", err)
	}
	if err := ValidateKey("not a link"); err == nil {
		t.Fatal("ValidateKey on garbage returned nil error")
	}
	if err := ValidateKey(""); err == nil {
		t.Fatal("ValidateKey on empty returned nil error")
	}
}
