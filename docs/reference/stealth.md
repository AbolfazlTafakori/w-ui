---
description: "The wui-udp stealth transport: WireGuard inside UDP packets that look like random bytes, the keys, the packets, the server's rules, and what a client must do."
---

# Stealth transport: wui-udp

wui-udp carries a WireGuard tunnel over UDP in packets that have nothing the network can name: no fixed byte, no fixed size, no fixed rhythm. A server answers nobody who does not hold a device's secret, so a probe learns nothing, not even that something is listening.

WireGuard itself is not changed. Its handshake, keys and replay protection stay as they are; the wrapping adds concealment, never trust. On the server the gateway unwraps each datagram and hands it to the tunnel's own WireGuard, from a socket of its own per device and source, so WireGuard sees every device at an endpoint of its own and roams it exactly as it would on plain UDP.

This page is the specification. The format is in `internal/stealth`, the server and the reference client in `internal/stealthgw`, and the panel's side in the reconciler. `internal/stealth/testdata/udp.json` is a test vector, and `scripts/stealth/udp_vectors.py` checks it against this page with an implementation of its own.

## On the panel

A standard WireGuard tunnel chooses its **Transport** in its form, as an OpenVPN tunnel chooses UDP or TCP: **UDP** is WireGuard as it is, and **wui-udp** is WireGuard as it is on the tunnel's port and wui-udp beside it, on a UDP port of its own. In the API they are `transport` (`udp` or `wui-udp`) and `transportPort`. The port may be no other tunnel's on that server, and it is let through the firewall like the tunnel's own. AmneziaWG tunnels have UDP alone: their datagrams are not WireGuard's. The other stealth transports join the same menu as they are built.

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
