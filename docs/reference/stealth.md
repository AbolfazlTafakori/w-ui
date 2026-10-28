---
description: "The stealth transports: wui-udp, WireGuard in UDP packets that look like random bytes; wui-ws, WireGuard as WebSocket behind a web server or a CDN; wui-poll, the same as HTTP requests for a CDN without WebSocket; wui-tls, the same with the gateway as the web server itself; and wui-plain, the same over plain HTTP where TLS itself is cut; the keys, the packets, the server's rules, and what a client must do."
---

# Stealth transport: wui-udp

wui-udp carries a WireGuard tunnel over UDP in packets that have nothing the network can name: no fixed byte, no fixed size, no fixed rhythm. A server answers nobody who does not hold a device's secret, so a probe learns nothing, not even that something is listening.

WireGuard itself is not changed. Its handshake, keys and replay protection stay as they are; the wrapping adds concealment, never trust. On the server the gateway unwraps each datagram and hands it to the tunnel's own WireGuard, from a socket of its own per device and source, so WireGuard sees every device at an endpoint of its own and roams it exactly as it would on plain UDP.

This page is the specification. The format is in `internal/stealth`, the server and the reference client in `internal/stealthgw`, and the panel's side in the reconciler. `internal/stealth/testdata/udp.json` is a test vector, and `scripts/stealth/udp_vectors.py` checks it against this page with an implementation of its own.

## On the panel

A standard WireGuard tunnel chooses its **Transport** in its form, as an OpenVPN tunnel chooses UDP or TCP: **UDP** is WireGuard as it is, and **wui-udp** is WireGuard as it is on the tunnel's port and wui-udp beside it, on a UDP port of its own. In the API they are `transport` (`udp` or `wui-udp`) and `transportPort`. The port may be no other tunnel's on that server, and it is let through the firewall like the tunnel's own. AmneziaWG tunnels have UDP alone: their datagrams are not WireGuard's. **wui-ws**, WebSocket behind a web server or a CDN, **wui-poll**, the same as HTTP requests for a CDN without WebSocket, **wui-tls**, the gateway as the web server itself, and **wui-plain**, WebSocket over plain HTTP, are the others, each in a part of its own below.

The tunnel itself does not change. Its peers, its port and every `.conf` handed out stay as they were; the panel only starts a gateway beside it, gives it the devices the tunnel carries on every pass, and stops it when the tunnel no longer wants one. A device taken off the tunnel, out of its allowance or switched off, loses its gateway sessions with its peer. The online indicator and the sharing report see a device at its own address, not the gateway's.

Every WireGuard device has a transport secret, drawn with its keys and replaced when they are rotated; devices made before wui-udp get one on the next pass. A remote node is given the setting and the secrets with the tunnel.

### The profile

An app of W-UI's own reads the customer's subscription with `?format=wui`:

```json
{
  "version": 1,
  "title": "the subscription's title",
  "serverTime": 1700000000,
  "tunnels": [
    {
      "name": "wg0",
      "device": "phone",
      "wireguard": "[Interface]
PrivateKey = ...",
      "transports": [
        {"type": "wui-udp", "endpoints": ["vpn.example.com:2096"], "secret": "base64", "mtu": 1420}
      ]
    }
  ]
}
```

`wireguard` is the file a stock app gets, and its `[Peer]` public key is the anchor `A`. `endpoints` are every address the customer connects to, best first, on the wui-udp port. `serverTime` is the panel's clock when the profile was made, for a client to keep its offset. An app refuses a `version` it does not know.

## Notation

`|` joins byte strings. HKDF and HMAC are over SHA-256. `Seal(k, n, a, p)` is ChaCha20-Poly1305 with key `k`, nonce `n`, associated data `a` and plaintext `p`; it adds 16 bytes. `L` is the ASCII string `w-ui stealth v1 ` (sixteen bytes, the last a space). `be64` is a big-endian 64-bit integer.

## Keys

Every device has a **transport secret** `S`: 32 random bytes the panel draws when the device is made and hands only to that device. It is not derived from the device's WireGuard key: that key proves who the device is to the tunnel, the secret only lets its packets past the wrapping, and a leak of one says nothing of the other.

Every key is bound to the tunnel's **anchor** `A`, the tunnel's WireGuard public key, which every profile already carries.

```
prk     = HKDF-Extract(salt = L | "device", ikm = S)
k_tag   = HKDF-Expand(prk, "tag" | 0x00 | A, 32)
k_udp   = HKDF-Expand(prk, "udp" | 0x00 | A, 32)
k_mask  = HKDF(salt = L | "mask", ikm = A, info = "", 32)

epoch(t)    = floor(unix seconds of t / 120)
epoch_tag(e) = HMAC(k_tag, "epoch" | be64(e))[0..16]
```

`k_mask` is public: anyone with a profile for the tunnel can work it out. It exists only so that a tag on the wire is different in every packet.

## The chunk

What every stealth transport moves is a chunk: WireGuard datagrams, each behind its length, then zero padding.

```
[len u16 big-endian][datagram] ... [0x00 ...]
```

A datagram is 32 to 1600 bytes, so a zero length can never start one: the first zero length is where padding begins, and every byte after it must be zero. A chunk's plaintext is at most 16 000 bytes. A chunk that breaks any of this is refused whole. On wui-udp a chunk holds exactly one datagram in an opening, and one or none in a data packet; a chunk of two is refused.

### Padding

For each direction of each session the sender draws a floor `base` from 0..63 once. A chunk is padded **wide**, `U(base..wide)`, when it is among the first 16 data packets of its direction or carries a WireGuard handshake message (type 1, 2 or 3); otherwise it is padded **normally**, `U(min(base, normal)..normal)`. `wide` is 512..1023 and `normal` 64..255, both the largest by default. `U` is uniform and inclusive. Padding is lowered so the packet fits its room, so a full-size inner packet carries none.

## Packets

```
opening = masked_tag (16) | salt (32) | Seal(k_open, 0^12, masked_tag | salt,
                                             0x01 | 0x00 0x00 0x00 | be64(timestamp) | chunk)
data    = nonce (12, random) | Seal(k, nonce, "", chunk)

masked_tag = epoch_tag(epoch(timestamp)) XOR HMAC(k_mask, salt)[0..16]

prk_s  = HKDF-Extract(salt = salt, ikm = k_udp)
k_open = HKDF-Expand(prk_s, L | "udp open", 32)
k_up   = HKDF-Expand(prk_s, L | "udp up", 32)     client to server
k_down = HKDF-Expand(prk_s, L | "udp down", 32)   server to client
```

The salt is fresh for every opening, so `k_open` seals one message and its fixed zero nonce is used once. The timestamp is the client's clock in Unix seconds, and the epoch of the tag is taken from the same value.

| Size | Bytes |
|---|---|
| Shortest data packet | 28 |
| Shortest opening | 76 |
| Shortest opening a client sends (`FIRST_ANSWER`) | 122 |
| Largest packet | the tunnel's MTU + 62 |

An opening is padded wide, by a draw of its own that is not one of the 16, and never to less than 122 bytes: the server's first answer is no larger than the opening it answers, so nobody can make it send more than it was sent, and a 92-byte handshake response in a data packet is 122.

A key seals at most 2³¹ packets. The client opens a new session at 2³⁰ sent or received; the server ends a session whose key reaches 2³¹.

## What the server does with a datagram

Whatever is not a packet for this server gets silence: nothing is sent, and a counter says why.

1. Shorter than 28 bytes: drop.
1. If the source address and port have sessions, try the datagram as a data packet of each: the live one, the pending one, and one a newer session replaced in the last 10 seconds. One that opens is handled (below), and that is all.
1. Unmask the tag with `HMAC(k_mask, salt)` and look it up in the tag table, which holds every device's tags for the previous, the current and the next epoch, rebuilt when the epoch changes. A miss: drop. A stranger's datagram costs one HMAC and one lookup.
1. Open the opening under the device's `k_open`, check the version byte, the three zero bytes and the chunk. Any failure: drop.
1. The replay rule (below). A refusal: drop.
1. The datagram must be a handshake initiation of this very device (see "WireGuard on a session") or a keepalive. Anything else: drop.
1. The limits must have room: at most 4 devices per source address and port, 2 pending sessions per device, 8 source addresses per device, 4096 sessions per gateway, and the allowances below. No room: drop.
1. Record the salt, make a pending session for this source and device, in place of a pending one of the same pair, and hand the datagram to the tunnel.

An opening that passed the replay rule and was dropped after it has its salt recorded too, while its device's allowance for that lasts: left unrecorded it would be good later for whoever saw it, and recorded without bound one device could fill the cache for everybody.

A data packet carries no number, so a recorded one opens again. That is harmless: inside is a WireGuard datagram, and WireGuard refuses a repeat. A copy is never activity (below).

### Sessions

A session is **pending** until WireGuard speaks on it, and nothing is sent on a pending session. The tunnel speaks to an endpoint only once something authentic came from it, so its first word on a session's socket makes the session **live**. A transport message under an index the tunnel gave the device in a handshake response the gateway carried also makes a session live, at once, since WireGuard says nothing back to a keepalive.

When a session becomes live it sends one packet: what the tunnel said, or padding alone if that does not fit in the opening's size. The live session it replaces is still read for 10 seconds; the one before that ends.

| Timer | |
|---|---|
| Pending session without WireGuard's word | ends after 10 s |
| Replaced session | read for 10 s |
| Live session with no activity | ends after 180 s |
| Echo of a keepalive | after U(0..3 s) |
| A lone keepalive from the tunnel | held U(0..3 s) |

**Activity** is the tunnel's word on the session, an initiation of the device, or a transport message with a counter higher than any the session has seen under its index: a recorded packet sent again carries an old counter and keeps nothing alive.

The server answers a chunk that held only a fresh WireGuard keepalive with one padding-only packet after the echo delay, unless it sent something else in that time, at most four waiting per session. WireGuard does not answer a keepalive, and a client that hears nothing cannot tell a live session from a dead one.

### WireGuard on a session

A session belongs to the device whose secret sealed its opening, and its WireGuard traffic must be that device's, or one customer's secret would carry another's tunnel. Every handshake initiation that reaches the server, in an opening or a data packet, is taken through the responder's first two steps of WireGuard's handshake with the tunnel's private key: its MAC1 must be right, its encrypted static key must be the session's device, and its encrypted timestamp must open under the static-static secret with that device, which only the device's private key gives. One that fails is dropped. A session takes at most 3 initiations at once and 1 a second after.

Handshake responses (92 bytes), cookie replies (64) and transport messages (32 or more) pass to the tunnel, which authenticates them as it always does. Anything else is dropped.

### The replay rule

An opening's timestamp must be within 60 seconds of the server's clock either way, above the floor, at or below the limit, and its salt must not be in the cache. The cache holds the salts the window still admits, at most 262 144; when it is full, every opening is refused until entries leave.

The **limit** is kept in a file, and the server takes no timestamp above it. Whenever the clock comes within 10 seconds of the limit, a limit 20 seconds above the clock is written. An opening above the limit, from a client whose clock runs ahead, is dropped, its salt recorded, and a limit 20 seconds above its timestamp written, never more than the window and a step above the clock; the client's next opening finds it raised. A limit is in force only once its write has been synced and renamed over the old file.

After a start the **floor** is the limit in the file, and nothing at or below it is taken. Whatever the last run took had a timestamp no higher than its limit, so a restart, a crash or a kill can never take it again, and nothing of the cache need survive. Without a usable file the floor is the clock plus the window. The cost is a short pause after a restart: an honest client waits until the clock passes the floor.

```
"WUIRPLY1" | be64(limit) | SHA-256 of the 16 bytes before
```

### Allowances

Each is a token bucket, a burst and a rate per second, spent only by openings a device sealed that the replay rule passed.

| Allowance | Burst | Per second |
|---|---|---|
| Sessions a device opens | 16 | 1 |
| Openings a device sends over its allowance, whose salts are spent | 32 | 2 |
| Sessions opened from one address | 32 | 4 |
| Initiations on one session | 3 | 1 |

## What a client does

- It opens a session with a WireGuard handshake initiation, or with a keepalive when the tunnel is already up. Never with user data.
- An opening with no answer is sent again after 1, 2 and 4 seconds, each time as a new opening under a fresh salt, since the server takes a salt once. After the last the attempt is lost.
- It listens on its old session and its new one until it hears from the new one, then sends on the new one alone.
- It opens a new session at 2³⁰ packets sent or received, and when a session it sent on has said nothing for 10 seconds.
- Its timestamp is its clock plus the offset from the server, when it knows one. A clock more than a minute off is refused, silently, like everything else.

The reference client in `internal/stealthgw` does this for a stock WireGuard that sends to a local port.

# Stealth transport: wui-ws

wui-ws carries a WireGuard tunnel as WebSocket behind the operator's own web server or a CDN. The front ends TLS, so to the network a device is visiting a site; the gateway behind it takes plain HTTP and answers nobody who cannot prove a device's secret. A request that proves nothing goes to the operator's own site, the decoy, exactly as it came: the gateway never makes an answer of its own for a stranger, so a probe sees the site and nothing else.

The keys, the chunk and its padding are wui-udp's, above. The format is in `internal/stealth` (`proof.go`, `stream.go`, `websocket.go`), the server and the reference client in `internal/stealthgw` (`stream.go`, `streamclient.go`). `internal/stealth/testdata/ws.json` is a test vector, and `scripts/stealth/ws_vectors.py` checks it against this page with an implementation of its own, HPKE included.

## On the panel

Choose **wui-ws** as a standard WireGuard tunnel's **Transport**. Plain WireGuard stays on the tunnel's port, as with wui-udp.

| Field | API | |
|---|---|---|
| wui-ws port | `transportPort` | The TCP port the front sends the transport's path to, as plain HTTP. |
| Front host | `transportWs.host` | The name devices connect to and TLS names: the web server's domain or the CDN's. Required. |
| Front port | `transportWs.frontPort` | The front's HTTPS port; 443 when empty. |
| Path | `transportWs.path` | The path the front sends to the gateway: 12 to 256 characters of letters, digits and `/ - . _ ~`. Left empty, one of 20 characters is drawn, and kept when the settings are saved again with it empty. |
| Decoy site | `transportWs.origin` | The operator's site, `http://` or `https://` with no path. Every request that is not a device's goes there as it came. Empty, such a request is answered 404 and the front dresses it like its own. |
| Trusted fronts | `transportWs.proxies` | Addresses or ranges, by commas or spaces, besides this machine, that may send the transport and name the visitor: a CDN's ranges. |
| Visitor header | `transportWs.clientHeader` | The header a front names the visitor in; `X-Forwarded-For` when empty. |

With no trusted front listed the gateway listens on 127.0.0.1 alone, for a web server on the same machine, and its port stays shut in the firewall. With one listed it listens on every address and its port is opened; a request from an address that is neither a trusted front nor the loopback is a stranger's, whatever it carries. The visitor is the last address in the visitor header that is not a trusted front, and the online indicator and the sharing report see the device there.

The transport has a key pair of its own, drawn when wui-ws is chosen. The panel draws the next one every 30 days and keeps the one before, which the gateway still takes, so an app that fetches its profile at least that often never meets a key it does not know. The private keys never leave the panel by the API; a remote node is given them with the tunnel, since its gateway needs them.

nginx in front on the same machine, the transport on port 28081 and the site where it was:

```nginx
location /41yg3dsw2u5h97g50uvl {
    proxy_pass http://127.0.0.1:28081;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_read_timeout 300s;
}
```

Every other location stays the site's. With the decoy set to the site's own address, a request to the path without a proof is answered by the site as any unknown path is. Behind a CDN, the CDN must carry WebSocket and the query string. A client never lets a session go quiet for more than 40 seconds, well inside the minute a front gives a quiet connection.

### The profile

```json
{"type": "wui-ws", "endpoints": ["cdn.example.com:443"], "url": "wss://cdn.example.com/41yg3dsw2u5h97g50uvl",
 "key": "base64", "secret": "base64", "mtu": 1420}
```

`url` is the name TLS and the request give, with the path; `endpoints` is where the app connects, and an app may connect to a CDN's address instead while it keeps `url`'s name for TLS. `key` is the transport's current public key. The secret and the anchor are wui-udp's.

## The proof

A session opens with one request: a GET with a WebSocket upgrade (RFC 6455, version 13) whose target is the path and a token, `<path>?<token>`. The token is an HPKE message (RFC 9180) to the transport's key, in base mode, with DHKEM(X25519, HKDF-SHA256), HKDF-SHA256 and ChaCha20-Poly1305:

```
token  = base64url without padding (enc (32) | HPKE-Seal(key, info, aad = "", hello))
info   = L | "proof" | 0x00 | "wui-ws" | 0x00 | A
hello  = 0x01 | 0x00 0x00 0x00 | be64(timestamp) | tag (16) | binder (32) | zero padding (0..96)
binder = HMAC(k_bind, Export(L | "binder", 32) | hello[0..28])

tag      = HMAC(k_tag, "device")[0..16]
k_bind   = HKDF-Expand(prk, "bind" | 0x00 | A, 32)
k_stream = HKDF-Expand(prk, "stream" | 0x00 | A, 32)
```

A token is 108 to 204 bytes, 144 to 272 characters. Only the holder of the transport key reads the hello, so a device's tag never shows on the wire. The binder is what proves the secret: it is keyed by the secret and bound to this HPKE context's export, so a tag copied into a token of somebody else's making is no proof. The transport's name and the anchor are in `info`, so a token for one transport or one tunnel opens on no other. The path is not bound, since a front may rewrite it.

The session's keys need both the proof's ephemeral key and the device's secret:

```
prk    = HKDF-Extract(salt = Export(L | "stream", 32), ikm = k_stream)
k_up   = HKDF-Expand(prk, L | "stream up", 32)     client to server
k_down = HKDF-Expand(prk, L | "stream down", 32)   server to client
```

## The session

Every binary WebSocket message is one sealed chunk, in a single frame; the client masks its frames and the server does not. A chunk is sealed with its direction's key under the nonce `0x00000000 | le64(n)`, `n` being the chunk's number in its direction from 0, with no associated data. A chunk holds as many datagrams as are waiting and fit, or none: padding alone is how a side answers without saying anything. The padding is wui-udp's, wide for a direction's first 16 chunks and for a chunk that carries a handshake message.

A text message, a fragmented one, one longer than 16 016 bytes, a frame with a reserved bit, a frame masked the wrong way for its side, or a chunk that does not open ends the session. Pings are answered and pongs ignored. However a session ends, the server sends a close of 1000 with no reason, waits up to 5 seconds for the client's close, and lets the connection go, so the cause cannot be read from outside.

The answer to a proof is `101 Switching Protocols` with `Upgrade`, `Connection`, `Sec-WebSocket-Accept`, `Date`, and a cookie, of a name the transport key gives, whose value is 16 to 96 characters drawn afresh, so that no two answers are one length.

## What the server does with a request

Whatever is not a proof goes to the decoy as it came, and a counter says why.

1. From an address that is neither a trusted front nor the loopback: the decoy.
1. A path other than the transport's, compared in constant time, or no WebSocket upgrade: the decoy.
1. Open the token under every transport key the gateway holds, current and previous, whether or not an earlier one opened it, and check the version byte, the three zero bytes and the padding. Any failure: the decoy.
1. Find the device by its tag and check the binder. A tag no device has is checked against a stand-in, so a known tag and an unknown one cost the same. A failure: the decoy.
1. wui-udp's replay rule, on `enc`, in a file of its own. A proof above the written limit asks for a raise and waits for it up to 2 seconds, since a refused stream costs its client a connection.
1. The limits: at most 8 sessions per device, 2 pending per device, 4096 per gateway, and wui-udp's allowances for the device and for the visitor's address. No room: the decoy, and the proof is recorded while the device's allowance for that lasts.
1. The proof is taken, once, under one lock: of two connections that carry the same proof, only one gets past here.
1. Answer the upgrade, open a socket of the session's own to the tunnel, and carry the session.

### Sessions

A session is **pending** until WireGuard speaks on its socket, or until a transport message comes under an index the tunnel gave the device in a response the gateway carried, and nothing is sent on a pending session. When it becomes live it sends what the tunnel said, or a padding-only chunk.

The first chunk of a session must hold a handshake initiation of this very device, or a keepalive; never user data. Anything else ends it. Every initiation is checked as on wui-udp, against the session's device, its static key and its timestamp, at most 3 at once and 1 a second after. Handshake responses must be 92 bytes, cookie replies 64, transport messages 32 or more, and a datagram of any other type ends the session.

A stream cannot be replayed: its chunks open only in order, under a key of this session alone. So every chunk that opens is activity, unlike wui-udp's data packets.

| Timer | |
|---|---|
| Pending session without WireGuard's word | ends after 10 s |
| Live session with no activity | ends after 300 s |
| Echo of a chunk of padding or keepalives alone | after U(0..3 s), at most four waiting |
| A lone keepalive from the tunnel | held U(0..3 s) |
| Close | 5 s for the client's close |

## What a client does

- It opens a session with a WireGuard handshake initiation, or with a keepalive when the tunnel is already up. Never with user data: what comes while it has no session is dropped.
- A connection that failed is tried again after 250 ms, the wait doubling up to 8 seconds.
- It sends a padding-only chunk after U(20..40 s) of sending nothing, and takes a session for dead when it sent and has heard nothing for 20 seconds: on a one-way flow WireGuard's own keepalive comes after ten seconds, and the gateway holds it a moment more.
- Its timestamp is its clock plus the offset from the server, when it knows one.

The reference client in `internal/stealthgw` does this for a stock WireGuard that sends to a local port, over `ws://` or `wss://`, with the address it dials apart from the name it gives.

# Stealth transport: wui-poll

wui-poll is wui-ws for a CDN that will not carry WebSocket. Behind the same front, the tunnel's datagrams travel in the bodies of ordinary POST requests to the transport's path and in the bodies of their answers: the device asks, one request at a time, and the gateway answers with whatever waits for it. To the network and to the CDN it is a page fetching from its own site. A request that proves nothing goes to the decoy as it came, body and all, exactly as on wui-ws.

The keys, the chunk, its padding and the proof are wui-ws's, above, under wui-poll's own name. The format is in `internal/stealth/poll.go`, the server and the reference client in `internal/stealthgw` (`poll.go`, `pollclient.go`). `internal/stealth/testdata/poll.json` is a test vector, and `scripts/stealth/ws_vectors.py` checks it against this page with an implementation of its own.

## On the panel

Choose **wui-poll** as a standard WireGuard tunnel's **Transport**. Its fields are wui-ws's: the wui-poll port, the front's host and port, the path, the decoy site, the trusted fronts and their visitor header, with the same rules and defaults. With no trusted front the gateway listens on 127.0.0.1 alone and its port stays shut in the firewall. The transport's key pair is its own, drawn when wui-poll is chosen and turned over every 30 days with the one before still taken. A tunnel switched to or from wui-poll carries no path or key across, and a wui-plain front never shares a wui-poll front's name or path.

nginx in front on the same machine, the transport on port 28082:

```nginx
location /41yg3dsw2u5h97g50uvl {
    proxy_pass http://127.0.0.1:28082;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_request_buffering off;
    proxy_buffering off;
    proxy_read_timeout 30s;
}
```

A body is at most 64 KiB each way, inside nginx's default `client_max_body_size`. Behind a CDN, the CDN must pass POST bodies and the query string, must not cache the path, and must not change the body; most do none of that to a POST. Turning off buffering only shortens the wait: a front that buffers works too.

### The profile

```json
{"type": "wui-poll", "endpoints": ["cdn.example.com:443"], "url": "https://cdn.example.com/41yg3dsw2u5h97g50uvl",
 "key": "base64", "secret": "base64", "mtu": 1420}
```

As wui-ws's, with `https://`.

## The requests

The first request of a session is the proof: `POST <path>?<token>`, the token wui-ws's with `wui-poll` in its `info`. Its session has one more value, besides its keys:

```
id = HKDF-Expand(prk, L | "stream id", 16)
```

Every later request names the session by its id and its own number:

```
POST <path>?<base64url(id)>.<n>
```

The id is 22 characters of base64url without padding, in the one text that names 16 bytes, and `n` is decimal, 1 to 4 294 967 295, with no leading zero: 1 for the first request after the proof, one more for each request the gateway took. The id is no key: knowing it opens nothing.

Every request, the proof included, has a body, exactly one `Content-Length` from 18 to 65 536, and no `Transfer-Encoding`. Every answer of the gateway is `200`, `Content-Type: application/octet-stream`, a `Content-Length`, and a body. A body, either way, is the session's sealed chunks, each behind its length, and never empty:

```
body = [be16(len) | sealed chunk] ...      1 or more, 18 to 65 536 bytes in all
```

A chunk is wui-ws's: sealed under its direction's key and the nonce `0x00000000 | le64(n)`, `n` running on from body to body in each direction, from 0. A side with nothing to send sends one padding-only chunk. The padding is wui-ws's.

A body is taken whole or not at all. Every chunk must open at the next numbers before anything in it is used, and the numbers move on only when the body is taken. A body in which any chunk does not open changes nothing. A body whose every chunk opens and one does not parse can only be the peer's own, and ends the session.

## What the server does with a request

Whatever is not a device's goes to the decoy as it came, body included, and a counter says why.

1. From an address that is neither a trusted front nor the loopback, a path other than the transport's, compared in constant time, not a POST, or a body framed otherwise than above: the decoy.
1. A query that reads as `<id>.<n>`: the id must be a session of this gateway, the session must have no request in flight, and `n` must be one more than the last request taken, or the same number again. Otherwise the decoy, and the session is not touched. At most 4 bodies under one id are read at once.
1. Any other query is a proof, judged as on wui-ws, steps 3 to 7, with at most 2 wui-poll sessions per device. A proof that is taken opens a pending session, whatever comes next.
1. The body must arrive whole within 10 seconds. A late one goes to the decoy, which is given what arrives.
1. A repeat of the last request taken, with the same body to the byte, gets the answer the gateway kept: a front may send a request twice. A repeat with another body is the decoy's.
1. Open the body at the session's next numbers. A body that does not open: the decoy, and the session is as it was, so whoever learns an id can send what they like under it and change nothing. A body that opens and holds a chunk that does not parse ends the session, and the request goes to the decoy.
1. On the proof's request, the first datagram must be a handshake initiation of the session's device, or a keepalive, as on wui-ws. Every datagram is checked and handed to the tunnel as on wui-ws.
1. Answer. The proof's request is held until the session is live, since nothing is sent on a pending session; a session that never is ends after 10 seconds, and its request goes to the decoy. Any other request is answered as soon as something waits for the device, or after a hold of 250 ms with a padding-only chunk. The answer holds everything that waits and fits; what does not fit leads the next answer. It is kept before it is written, for a repeat.

A session no request was taken on for 60 seconds ends. There are no echoes: a client's polls are the activity, and their answers the sign of life. A lone keepalive from the tunnel is held U(0..3 s), as on wui-ws.

## What a client does

- One request in flight. It sends at once when it has something to send and no request is in flight. With nothing to send it polls after a wait that starts at 100 ms, grows by half after every exchange that carried nothing either way up to 5 seconds, starts again after one that carried something, and is spread by a number drawn from 0.75 to 1.25; never more than a minute.
- It opens a session with a WireGuard handshake initiation, or a keepalive, in the proof's body, and sends the proof once: a front's repeat of it is a spent proof. Anything but a `200` with a body that opens ends the attempt; a failed one is tried again after 250 ms, the wait doubling up to 8 seconds.
- A later request whose connection ends before its answer is whole is sent again, the same bytes on a new connection, up to 3 times. With no answer within 10 seconds, or an answer that is not a `200` with a body that opens, the session is over.
- A connection is kept from one request to the next, unless the answer says `Connection: close` or the front sent anything unasked between two answers.
- It opens a new session before `n` would pass 4 294 967 295.
- Its request is a browser's fetch from a page of the same site: `Content-Type: application/octet-stream`, and the `Origin`, `Referer` and `Sec-Fetch-*` fields of that fetch. An app sends its own platform's browser's fields, in that browser's order. An answer with a `Content-Encoding` is refused.

The reference client in `internal/stealthgw` does this for a stock WireGuard that sends to a local port, over `https://`, or `http://` for a test, with the address it dials apart from the name it gives.

# Stealth transport: wui-tls

wui-tls is wui-ws without a front: the gateway is the web server, on a port of its own, and ends TLS itself. With nobody in front to tell a device from a visitor, it decides from the bytes alone, as they arrive, and whoever is not a device is handed to the operator's own site -- every byte they sent, as they sent it, and from then on both ways with nothing read or made by the gateway. Devices trust the gateway's certificate by its pin, so no domain or authority is needed.

The proof, the session and the chunk are wui-ws's, above, under the name `wui-tls`; only what happens before the proof differs. The request line is read in `internal/stealth/request.go`, the certificate is made in `internal/stealth/certificate.go`, and the server and the client's TLS are in `internal/stealthgw` (`tls.go`, `tlsclient.go`). `internal/stealth/testdata/tls.json` is a test vector, checked by `scripts/stealth/ws_vectors.py` with wui-ws's.

## On the panel

Choose **wui-tls** as a standard WireGuard tunnel's **Transport**. Plain WireGuard stays on the tunnel's port, as with wui-udp.

| Field | API | |
|---|---|---|
| wui-tls port | `transportPort` | The TCP port devices connect to, on every address, opened in the firewall. 443 looks most like a web server. |
| Server name or address | `transportWs.host` | What devices connect to: a domain pointing at this server, or its address. Required. A domain is also what the certificate names and what devices send as the server name; with an address they send none. |
| Path | `transportWs.path` | As wui-ws's: drawn when left empty, and kept when the settings are saved again with it empty. |
| Decoy site | `transportWs.origin` | The operator's site, `http://` with no path. Required: the gateway hands every stranger to it as they came, after its own TLS, so it speaks plain HTTP to it. |
| Use the panel's certificate | `transportWs.usePanelCert` | Serve the panel's own issued certificate when it is in date for the name, on a tunnel of the panel's own server. |
| PROXY protocol to the decoy | `transportWs.proxyProtocol` | Open every connection to the decoy with a PROXY protocol v2 header naming the visitor, for a site set to expect one. |

The transport's key is wui-ws's: drawn when wui-tls is chosen, the next one drawn every 30 days with the one before still taken, never shown by the API, and given to a remote node with the tunnel. A tunnel switched to wui-tls from another transport carries no path or key across, nor one switched away.

### The certificate

The tunnel has a certificate of its own, which nobody issued. The panel draws an ECDSA P-256 key for it once, when wui-tls is chosen, and keeps it for as long as the tunnel has wui-tls: devices trust the certificate by that key's pin alone, so a new key would be a new pin, and every app would need its profile again before it reached the tunnel. The certificate is made again with the same key when it is 20 days from its end, or claims to start more than a day from now, or names another name than the tunnel's -- none of which a device notices.

Nothing in it says W-UI. It names the tunnel's domain, or, for an address, a name drawn once in a shape a private network's hosts have (`kemo-tiz.lan`, `rufa42.corp`) and kept across renewals. Its serial is 16 random bytes, it lives 80 days and up to 20 more, and it starts at a random moment of the week before it was made, so no two servers' certificates line up. It carries the extensions a server certificate usually has: a key usage of digital signature, an extended key usage of server authentication, no CA, the name as its alternative name, and a key identifier.

With **Use the panel's certificate** on, the panel's own certificate is served instead while it is in date for the tunnel's name and the tunnel is on the panel's server; a node's tunnel never takes it, since its key would have to go to the node. Devices then check it as a browser does. When it stops covering the name, the tunnel's own is served again, and profiles say so. The reconciler's pass keeps either current, and a running gateway takes a new certificate from its next connection on, so no session ends for a renewal. The certificates' keys never leave the panel by the API; a remote node is given the tunnel's own with the tunnel.

### The profile

```json
{"type": "wui-tls", "endpoints": ["edge.example.com:443"], "url": "wss://edge.example.com/41yg3dsw2u5h97g50uvl",
 "pins": ["base64"], "selfSigned": true, "key": "base64", "secret": "base64", "mtu": 1420}
```

`url` is the name TLS and the request give, with the path and the port when it is not 443; an address in it is sent as no name. `pins` are the SHA-256 of the served certificate's SubjectPublicKeyInfo, in standard base64. With `selfSigned` the server is trusted for its pin alone; without it, for the panel's certificate, `pins` is empty and the certificate is checked as a browser checks it. `key` is the transport's current public key.

## What the server does with a connection

1. **TLS**, within 10 seconds of the accept. TLS 1.2 is offered only with ECDHE and an AEAD; ALPN is `http/1.1` alone. A handshake that fails or is too slow is closed as the system closes it.
1. **The decoy, now.** A connection to the decoy is opened at once, before a byte is read, so the decoy's own timers run from the moment they would for a visitor who reached it directly; with the PROXY protocol on, it opens with a v2 header naming the visitor and the port they reached.
1. **The request line**, as it arrives. It stays a candidate only while it can still become `GET /<target> HTTP/1.1` CR LF, with a target of the characters RFC 3986 lets a path and a query carry unescaped. The first byte that cannot belong to such a line, a line of 4096 bytes without its end, 10 seconds without a complete line, the peer closing, or the decoy saying anything or closing first, decides that the peer is a stranger. No secret is used for any of it, so the decision costs everyone the same.
1. **A complete line** is a device's only when its path is the transport's, compared in constant time, its query is a proof that holds -- wui-ws's steps 3 to 7, under the name `wui-tls` -- and TLS 1.3 was spoken, which every client insists on. The token is opened whatever the path and the TLS version, so the work a line costs does not say which of them was wrong. A complete line is weighed whatever the decoy did meanwhile.
1. **A device's head** is read, up to 16 KiB in all and 10 seconds, and the decoy's connection is closed unused. Only `Upgrade: websocket`, `Connection: Upgrade`, `Sec-WebSocket-Version: 13` and `Sec-WebSocket-Key` are looked at; a head without them is closed with no answer -- TLS's close, then the connection's -- and its proof spent. The answer is wui-ws's, with the header fields the decoy's own answers carry (`Server`), learned by asking the decoy for its front page as a visitor would, every hour, or every minute while it fails. The session then runs as wui-ws's.
1. **A stranger's bytes**, every one read so far, go to the decoy unchanged, and from then on the gateway relays both ways and reads nothing. A half-close passes through as it came, a reset from the decoy resets the visitor, and when the decoy closes the gateway lingers up to 5 seconds over the visitor's last bytes, as a web server does, so the visitor is not reset before it has read the answer. The gateway never writes a byte of its own to a stranger: no status, no error page. While the decoy cannot be reached a stranger is closed with nothing, which shows the gateway, and the log says so once a minute.

| Limit | |
|---|---|
| Connections not yet decided | 512; 64 from one address. Over either, the oldest of that address, or of all, is decided at once as a stranger's: a stranger only ever displaces a stranger. |
| Strangers relayed at once | 1024. Over it the one idle longest goes, if it has been idle a minute, else the oldest. |
| When every place is taken | No connection is accepted until one is free; they wait in the system's queue, as at a busy server. |

## What someone on the path can see

- A TLS handshake on the gateway's port, and the certificate in it, which is the gateway's and not the decoy's. On a probe that compares the two, wui-tls shows. Behind a front, as wui-ws, it does not.
- A relayed answer a hop later than the decoy's own would be.
- What a device's connection looks like is the device's TLS. The reference client is Go's, which is not a browser's; W-UI's own app is to speak as a browser does.

## What a client does

- It holds the server's certificate to the check the profile says, never the one the server would like: with `selfSigned`, the leaf's key must be one of the pins and TLS 1.3 must be spoken, and the chain, the name and the dates are not looked at. TLS 1.3 has the server sign the handshake with that key, and the signature is checked all the same, so a server that shows the pinned certificate without holding its key fails. Without `selfSigned`, the chain must lead to a root the device trusts, for the URL's name, at the device's own time; with pins as well, the key must be one of them and TLS 1.3 spoken, a pin being a second condition and never a way around the first.
- TLS 1.2, where it is taken, must end on ECDHE and an AEAD. Nothing that fails is tried again with less.
- The rest is wui-ws's client: the proof, the session, its retries and its timers.

# Stealth transport: wui-plain

wui-plain is wui-ws for a network where TLS itself is what gets a connection cut: the device speaks plain HTTP to the operator's own web server, which hands the gateway the request exactly as wui-ws's front does. Nothing of the session is weaker. Its proof, its keys, its sealed chunks, its padding and its checks of WireGuard are wui-ws's, and a stranger still sees the operator's site and nothing else. What is weaker is what the network sees: with no TLS, everything between the device and the front can be read and written by whoever is on the path, and they can read that a request with a cookie of random text was upgraded to WebSocket. It is the weakest of the transports. An app uses it last, only where nothing covered gets through, and only once its user has agreed to.

The format is wui-ws's, in `internal/stealth`, with the cookie in `cookie.go`; the server is `internal/stealthgw/plain.go`, and the reference client is wui-ws's. `internal/stealth/testdata/plain.json` is a test vector, and `scripts/stealth/ws_vectors.py` checks it against this page with an implementation of its own, the cookie rule included.

## On the panel

Choose **wui-plain** as a standard WireGuard tunnel's **Transport**. Plain WireGuard stays on the tunnel's port, as with the others.

| Field | API | |
|---|---|---|
| wui-plain port | `transportPort` | The TCP port the web server sends the transport's path to. |
| Front host | `transportWs.host` | The name devices connect to over plain HTTP. Required, and never a name a wui-ws transport uses (below). |
| Front port | `transportWs.frontPort` | The front's HTTP port; 80 when empty. |
| Path | `transportWs.path` | As wui-ws's: 12 to 256 characters, drawn when left empty, and never a wui-ws transport's (below). |
| Decoy site | `transportWs.origin` | The operator's site, `http://` or `https://` with no path. **Required**: a stranger's answer crosses the network in the clear, so it must be the site's own, never one the gateway made. |
| Trusted fronts | `transportWs.proxies` | As wui-ws's. |
| Visitor header | `transportWs.clientHeader` | As wui-ws's. |
| Cookie name | `transportWs.cookie` | The cookie a device's token travels in: 1 to 32 characters of `A-Z a-z 0-9 - _`. Left empty, one of 8 to 16 is drawn, a letter first, as a site's session cookie might be named, and kept when the settings are saved again with it empty and when the key turns over. |

The listening rules are wui-ws's: on 127.0.0.1 alone and shut in the firewall with no front elsewhere, on every address and opened with one.

The transport has a key pair of its own, drawn when wui-plain is chosen, and turned over every **7 days** with the one before still taken, where a covered transport's lasts 30: every proof wui-plain opens crossed the network in the clear, and a key that leaked would open the hellos recorded under it. A tunnel switched from one transport to another draws a new key, path and cookie, and never carries over the old transport's path, even if it is sent back: nothing wui-plain showed in the clear goes on to a covered transport, nor the other way round. The private keys never leave the panel by the API; a remote node is given them with the tunnel. The cookie's name is no secret, and the form shows it.

A wui-plain front's name and path are read in the clear, so the panel refuses a wui-plain whose host or path is a covered transport's -- wui-tls's, wui-ws's or wui-poll's -- on any tunnel of any server, and a covered one whose are a wui-plain's: one they shared would tie the covered transport to the exposed one. Two wui-plain transports may share a front, on paths of their own.

nginx on port 80 of the same machine, the transport on port 28082 and the site where it was:

```nginx
server {
    listen 80;
    server_name plain.example.com;

    location = /gu7qym3a1quw18halvk7 {
        proxy_pass http://127.0.0.1:28082;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 300s;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;  # the site, which is also the decoy
    }
}
```

nginx passes the `Cookie` field on as it came, which is all the gateway needs. Do not add `$http_cookie` to an access log's format: a token is good once, but there is no reason to keep it.

### The profile

```json
{"type": "wui-plain", "endpoints": ["plain.example.com:80"], "url": "ws://plain.example.com/gu7qym3a1quw18halvk7",
 "cookie": "psgouzg_t", "key": "base64", "secret": "base64", "mtu": 1420}
```

`url` is always `ws://`, with the port when it is not 80. `cookie` is the name the token travels under. `key` is the transport's current public key, which is wui-plain's own. The secret and the anchor are wui-udp's.

## The proof

wui-plain's proof is wui-ws's, made for its own name:

```
info = L | "proof" | 0x00 | "wui-plain" | 0x00 | A
```

so a token seen on a plain path opens on no covered transport, and none of theirs opens here, even under one key. The hello, the binder and the session's keys are wui-ws's.

The token is not in the target. A session opens with a GET for the path alone, with a WebSocket upgrade, and the token in a cookie:

```
GET /gu7qym3a1quw18halvk7 HTTP/1.1
Host: plain.example.com
...
Cookie: psgouzg_t=<token>
Sec-WebSocket-Key: ...
```

The cookie is read strictly, so that a request has one reading only:

- The request has exactly one `Cookie` field.
- The field is `name=value` pairs joined by `; `, a semicolon and one blank, with none at either end; every name is an HTTP token (RFC 9110).
- The transport's cookie is among them exactly once, by its name as it is spelled.
- Its value is 1 or more characters of `A-Z a-z 0-9 - _`, which is what a token is: no quotes, no blank, no `=`.

Anything else is a stranger's request. A request whose target has a query of any kind, an empty `?` included, is a stranger's too.

## The session

The session is wui-ws's, byte for byte: one sealed chunk to a binary message, the same nonce, the same padding, the same frames refused, the same close. The answer to a proof is `101 Switching Protocols` with `Upgrade`, `Connection`, `Sec-WebSocket-Accept` and `Date`, and nothing more: no cookie, since on a plain path a cookie the client never sends back is one more thing to read, and no extension, whatever the request offered.

## What the server does with a request

wui-ws's steps, with two differences:

1. From an address that is neither a trusted front nor the loopback: the decoy.
1. A path other than the transport's, compared in constant time, no WebSocket upgrade, a query, or no cookie that reads by the rule above: the decoy.
1. Open the cookie's token, as wui-ws opens the query's, under every transport key the gateway holds.
1. The rest is wui-ws's: the device by its tag and its binder, the replay rule on `enc` in a file of its own, the limits, the proof taken once under one lock, and the answer.

## What someone on the path can do

They read, and can change, every byte between the device and the front. What that gives them:

- **Who the device is: nothing.** The tag is inside the HPKE message, and only the transport key opens it. The cookie is random text, of a length drawn afresh.
- **A token: nothing usable.** A token is taken once. Sent first, ahead of the device's own request, it is answered, but the session's keys need the device's secret as well as the token's own, so the first chunk they send fails to open and the session ends with a close of 1000. The device's request, coming second, is a stranger's, and it tries again with a new token.
- **A frame: nothing reaches anyone.** A frame changed, added or cut fails to open or is refused, and the session ends; nothing of it reaches WireGuard, which on its side refuses anything it did not seal.
- **A probe: the site.** Whatever is not a proof is answered by the decoy, and the gateway writes no byte of its own for a stranger.
- **A leaked transport key** opens the hellos recorded under it, and so tells which recorded sessions were one device's, but not their keys, which need the device's secret, nor the tunnel, which is WireGuard's. The key turns over every 7 days, and the one before goes 7 days later.

What they do see is that WebSocket was spoken, the sizes and times of the frames as padding leaves them, and the front's name.

## What a client does

wui-ws's rules, and:

- It ranks wui-plain below every other transport of the profile, and uses it only once its user has agreed to a transport the network can see.
- Its request is a browser's upgrade from a page served over `http://`, the fields in a browser's order, with the token in the cookie and nowhere else; the reference client sends a desktop Chrome's.
- Nothing vouches for an answer on a plain path, so a 101 is no session: an answer that takes an extension, or a first frame that does not open under `k_down`, is a failure, and the session is the gateway's only once one of its chunks has opened.
- It never sends a token twice.

The reference client in `internal/stealthgw` does this with `Transport` set to `wui-plain`, over `ws://`.
