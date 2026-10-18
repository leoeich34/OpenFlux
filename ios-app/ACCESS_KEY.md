# Connecting from an access key (iOS)

A buyer connects by pasting one `openflux://` access key — no transport
picker to understand, no URL or credentials to fill in. This runbook builds
the client and verifies the flow end to end. It needs a Mac with Xcode; the
core logic it wraps (`mobile.KeySpecs`) is unit-tested off-device in
[`mobile/key_test.go`](../mobile/key_test.go), so what is left to verify here
is the cgo export, the SwiftUI validation, and a live tunnel from a key.

## Build the core

```bash
./build_ios.sh
```

This produces `output/ios/liboflux.a` and a refreshed `output/ios/liboflux.h`.
Confirm the new export is in the header:

```bash
grep OpenFluxStartFromKey output/ios/liboflux.h
```

It must list `OpenFluxStartFromKey` (and `OpenFluxShareDecode`, used for
field validation). If it does not, the archive is stale — rebuild.

## Open and run the app

```bash
cd ios-app
xcodegen generate      # regenerates OpenFlux.xcodeproj from project.yml
open OpenFlux.xcodeproj
```

Run on a device or the Simulator (the SOCKS proxy path works in the
Simulator; the whole-device VPN needs a real device and a provisioning
profile — and is intentionally not offered for a key, see below).

## Issue a key to test with

On the host running `accessd`, issue one with `accessctl` or the Telegram
operator bot:

```bash
go run ./cmd/accessctl issue --admin 127.0.0.1:8099 --days 30
```

Copy the `openflux://…` line it prints.

## Verify

1. **Valid key connects.** Launch the app, pick **Access key** in the
   transport picker, paste the key. The hint under the field stays neutral
   and **Start** becomes enabled. Tap **Start** → the status dot goes to
   *Connecting…* then *Connected*; tap **Test** → the log shows an exit IP
   that is the pool exit's, not your own. Covers Review-Focus *Valid key,
   connect* and *Key with a bare single transport* (the issued key carries
   one telemost carrier and still connects, because the Session speaks
   classic to a one-carrier node).

2. **Malformed key is refused.** Replace the key with garbage
   (`openflux://nope`) or plain text. The field shows the core's worded
   error (from `OpenFluxShareDecode`) and **Start** is disabled — no connect
   attempt. Covers *Malformed / non-key text pasted*.

3. **Empty input.** Clear the field. The hint returns to neutral and
   **Start** is disabled — never an empty-config connect. Covers *Empty /
   whitespace input*.

4. **Secret never shown or logged.** Turn on **Verbose log** and repeat a
   connect. The log never prints the key or its secret (the bridge logs only
   `access key rejected` on a bad key, never the key text). Covers *Secret
   never logged* for the export; the core helper is covered by the unit test.

## Scope note

An access key connects through the **local SOCKS5 proxy**. The whole-device
**VPN** is not offered for a key: its packet-tunnel path has no
from-key export yet (`OpenFluxStartFromKey` is the proxy export). The app
shows a note to that effect under the VPN section when *Access key* is
selected. Wiring a packet-tunnel-from-key export is a follow-up.
