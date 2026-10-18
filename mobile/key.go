package mobile

import "github.com/p1neappleXpress/OpenFlux/share"

// Connecting from an access key. A buyer pastes one openflux:// key; the
// client turns it into the exact (specsJSON, secret) the manual Session
// path already takes, so a key reaches the same place as the equivalent
// hand-built profile. The secret lives inside the key and is a credential:
// nothing here logs it or the full key.

// KeySpecs decodes an openflux:// access key into the Session profile the
// connect path takes — the link's own context and carrier names, via
// sessionSpecsOf — and the key's secret. A malformed key returns an error
// whose text matches what share.Read reports (share.Decode's *share.Error).
// A one-carrier key works too: the Session speaks classic to a classic node.
func KeySpecs(link string) (specsJSON string, secret string, err error) {
	c, err := share.Decode(link)
	if err != nil {
		return "", "", err
	}
	return sessionSpecsOf(c), c.Secret, nil
}

// ValidateKey reports whether link is a connectable access key: nil for a
// good key, else a worded error the UI shows before connecting (the same
// error share.Read would carry, so the app and the core agree on what a
// valid key is). Empty or non-key text is rejected.
func ValidateKey(link string) error {
	_, err := share.Decode(link)
	return err
}
