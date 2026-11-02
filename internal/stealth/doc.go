// Package stealth is the wire format of W-UI's stealth transports: the ways a
// WireGuard tunnel's packets reach the server when plain WireGuard over UDP is
// blocked or recognised.
//
// WireGuard itself is never changed. A stealth transport only carries
// WireGuard's datagrams, inside a wrapping that shows the network nothing it
// can name: no fixed byte, no fixed size, and on the stream transports no
// reply at all to anyone who cannot prove they hold a device's secret. The
// tunnel's own handshake, keys and replay protection stay exactly as they are,
// so what the wrapping adds is concealment, never trust.
//
// The transports, by the name a profile gives them:
//
//	veil    UDP, every packet indistinguishable from random bytes
//	anchor    WebSocket over TLS the server ends itself, a decoy behind it
//	drift     WebSocket behind the operator's web server or a CDN
//	relay   HTTP long polling, for a CDN that will not carry WebSocket
//	mold  relay in requests of the operator's shape, or one stream
//	bare  WebSocket over plain HTTP, the proof in a cookie
//	mirror a borrowed site's TLS: the proof hidden in a ClientHello
//
// This package holds only the format: key derivation, the sealed chunk every
// transport moves, the UDP packets, the proof a stream opens with, and the
// small HTTP and WebSocket pieces around it. It does no I/O of its own. The
// server is package stealthgw, and docs/reference/stealth.md is the
// specification an app is written from.
package stealth

// Transport names, as profiles and proofs carry them.
const (
	Veil     = "veil"
	Anchored = "anchor"
	Drift    = "drift"
	Relay    = "relay"
	Mold     = "mold"
	Bare     = "bare"
	Mirror   = "mirror"
)

// Version is the first byte of every sealed header and proof. A later format
// changes it, and a server refuses a version it does not speak by silence,
// like anything else it cannot read.
const Version = 1
