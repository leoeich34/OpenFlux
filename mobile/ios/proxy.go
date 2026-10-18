//go:build ios

package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	mobile "openflux-mobile"
)

// The in-app core: a local SOCKS5 proxy over the tunnel (the app's own
// connection test and captcha pages), package mobile's Proxy mode.

var proxyStarted time.Time

// OpenFluxStartClient starts the SOCKS5 client on socksAddr for a classic
// profile: transportType ("yandex", "vyandex"/"volga", "boards", "mailru",
// "cupsonline", "direct", "oneme") and url (the document, the room list,
// host:port for direct; a comma-separated list runs one carrier per
// document). With a secret (OpenFluxSetEncryption) it is a Session that
// speaks classic to a node that does not answer the handshake. Returns a
// start code.
//
//export OpenFluxStartClient
func OpenFluxStartClient(transportType, url, socksAddr, maxToken, maxUid *C.char) (rc C.int) {
	defer recoverStart(&rc, "OpenFluxStartClient")
	if mobile.ProxyIsRunning() {
		return startAlreadyRunning
	}
	typ := normalizeType(C.GoString(transportType))
	value := strings.TrimSpace(C.GoString(url))
	addr := C.GoString(socksAddr)
	secret, codec := currentSettings()
	if typ == "direct" && secret == "" {
		log.Printf("[BRIDGE] direct needs an encryption key")
		return startBadEncryption
	}
	var msg string
	if specs := classicSpecs(typ, value); len(specs) > 1 {
		if secret == "" {
			log.Printf("[BRIDGE] several documents need an encryption key (a Session)")
			return startBadEncryption
		}
		b, _ := json.Marshal(specs)
		msg = mobile.StartSessionProxy(string(b), secret, addr, "", "", "")
	} else {
		msg = mobile.StartProxy(typ, value, secret, codec, C.GoString(maxToken), C.GoString(maxUid), addr, "", "", "")
	}
	if msg != "" {
		log.Printf("[BRIDGE] start: %s", msg)
		return startCode(msg)
	}
	proxyStarted = time.Now()
	return startOK
}

// OpenFluxStartSession starts the SOCKS5 client for a Session profile:
// specsJSON is what OpenFluxShareDecode returns as "session" (or package
// mobile's StartSession takes).
//
//export OpenFluxStartSession
func OpenFluxStartSession(specsJSON, secret, socksAddr *C.char) (rc C.int) {
	defer recoverStart(&rc, "OpenFluxStartSession")
	return startSession(C.GoString(specsJSON), strings.TrimSpace(C.GoString(secret)), C.GoString(socksAddr))
}

// OpenFluxStartFromKey starts the SOCKS5 client from a pasted openflux://
// access key: the core decodes the key into the same Session profile and
// secret the manual Session path takes (mobile.KeySpecs) and connects over
// it. A one-carrier key works too (the Session speaks classic to a classic
// node). Returns the same start codes as OpenFluxStartSession, plus
// startBadKey for a key that does not decode. The key and its secret are a
// credential and are never logged.
//
//export OpenFluxStartFromKey
func OpenFluxStartFromKey(link, socksAddr *C.char) (rc C.int) {
	defer recoverStart(&rc, "OpenFluxStartFromKey")
	if mobile.ProxyIsRunning() {
		return startAlreadyRunning
	}
	specsJSON, secret, err := mobile.KeySpecs(C.GoString(link))
	if err != nil {
		log.Printf("[BRIDGE] access key rejected")
		return startBadKey
	}
	return startSession(specsJSON, secret, C.GoString(socksAddr))
}

// startSession is the shared Session connect path behind both
// OpenFluxStartSession and OpenFluxStartFromKey: refuse a second start,
// hand the profile to package mobile, and map its result to a start code.
// It logs neither the specs nor the secret.
func startSession(specsJSON, secret, socksAddr string) C.int {
	if mobile.ProxyIsRunning() {
		return startAlreadyRunning
	}
	msg := mobile.StartSessionProxy(specsJSON, strings.TrimSpace(secret), socksAddr, "", "", "")
	if msg != "" {
		log.Printf("[BRIDGE] start: %s", msg)
		return startCode(msg)
	}
	proxyStarted = time.Now()
	return startOK
}

//export OpenFluxStop
func OpenFluxStop() {
	mobile.StopProxy()
}

//export OpenFluxIsRunning
func OpenFluxIsRunning() C.int {
	if mobile.ProxyIsRunning() {
		return 1
	}
	return 0
}

//export OpenFluxIsConnected
func OpenFluxIsConnected() C.int {
	if mobile.ProxyIsConnected() {
		return 1
	}
	return 0
}

// OpenFluxStatsJSON returns the SOCKS client's state as JSON. Free with
// OpenFluxFreeString.
//
//export OpenFluxStatsJSON
func OpenFluxStatsJSON() *C.char {
	running := mobile.ProxyIsRunning()
	uptime := int64(0)
	if running {
		uptime = int64(time.Since(proxyStarted) / time.Second)
	}
	return C.CString(fmt.Sprintf(
		`{"running":%t,"connected":%t,"bytesSent":%d,"bytesReceived":%d,"uptimeSec":%d,"mode":%q,"transport":%q}`,
		running, mobile.ProxyIsConnected(), mobile.ProxyBytesSent(), mobile.ProxyBytesReceived(), uptime,
		mobile.ConnectionMode(), mobile.CurrentTransport()))
}
