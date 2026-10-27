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
//	wui-udp    UDP, every packet indistinguishable from random bytes
//	wui-tls    WebSocket over TLS the server ends itself, a decoy behind it
//	wui-ws     WebSocket behind the operator's web server or a CDN
//	wui-poll   HTTP long polling, for a CDN that will not carry WebSocket
//	wui-plain  WebSocket over plain HTTP, the proof in a cookie
//
// This package holds only the format: key derivation, the sealed chunk every
// transport moves, the UDP packets, the proof a stream opens with, and the
// small HTTP and WebSocket pieces around it. It does no I/O of its own. The
// server is package stealthgw, and docs/reference/stealth.md is the
// specification an app is written from.
package stealth

// Transport names, as profiles and proofs carry them.
const (
	UDP   = "wui-udp"
	TLS   = "wui-tls"
	WS    = "wui-ws"
	Poll  = "wui-poll"
	Plain = "wui-plain"
)

// Version is the first byte of every sealed header and proof. A later format
// changes it, and a server refuses a version it does not speak by silence,
// like anything else it cannot read.
const Version = 1
