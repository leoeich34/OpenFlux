package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/p1neappleXpress/OpenFlux/access"
	"github.com/p1neappleXpress/OpenFlux/transport"
)

// licenseGrace is how long the exit tolerates a lost control connection
// before it fail-closes.
var licenseGrace = 10 * time.Second

// licenseValidate reports whether the license client is enabled, enforcing
// that --license-server and --license-key-id are set together or not at all.
func licenseValidate(server, keyID string) (bool, error) {
	switch {
	case server == "" && keyID == "":
		return false, nil
	case server != "" && keyID != "":
		return true, nil
	default:
		return false, fmt.Errorf("--license-server and --license-key-id must be set together")
	}
}

// startLicense dials the control server for keyID. On revoke, expiry, or a
// lost connection beyond the grace window it cleanly stops the transport and
// exits (fail-closed) — the same clean leave the Telemost signal path uses.
func startLicense(server, keyID string, trans transport.Transport) (*access.LicenseClient, error) {
	return access.Dial(server, keyID, licenseGrace, func(reason string) {
		log.Printf("Access key %s: %s — stopping", keyID, reason)
		_ = trans.Stop()
		os.Exit(0)
	})
}
