#!/usr/bin/env python3
"""Checks internal/stealth/testdata/ws.json, tls.json, plain.json and poll.json
against docs/reference/stealth.md.

An implementation of the wui-ws, wui-tls and wui-plain proofs and stream chunks of its
own, written from the specification and from RFC 9180 and not from the Go
code, HPKE included, so that what the panel sends and what the specification
says are known to be the same. It opens each vector's token with the server's
transport key, checks every field of the hello and its binder, derives the
session's two keys again and opens the chunk each way, and fails on the first
thing that differs. For wui-plain it also reads the token back from the
vector's Cookie field by the specification's rule, and checks that the token
opens under no other transport's name. For wui-poll it derives the session's
id, reads the query of the first later request by the specification's rule,
and opens a body each way, chunk by chunk behind their lengths.

    python3 scripts/stealth/ws_vectors.py

It needs the `cryptography` package.
"""

import base64
import hashlib
import hmac
import json
import pathlib
import struct
import sys

from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey, X25519PublicKey
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat

LABEL = b"w-ui stealth v1 "
PROOF_PAD = 96


def extract(salt: bytes, ikm: bytes) -> bytes:
    return hmac.new(salt, ikm, hashlib.sha256).digest()


def expand(prk: bytes, info: bytes, n: int = 32) -> bytes:
    out, block, i = b"", b"", 1
    while len(out) < n:
        block = hmac.new(prk, block + info + bytes([i]), hashlib.sha256).digest()
        out += block
        i += 1
    return out[:n]


def mac(key: bytes, msg: bytes) -> bytes:
    return hmac.new(key, msg, hashlib.sha256).digest()


# HPKE, RFC 9180: base mode, DHKEM(X25519, HKDF-SHA256) 0x0020,
# HKDF-SHA256 0x0001, ChaCha20Poly1305 0x0003.

KEM_ID, KDF_ID, AEAD_ID = 0x0020, 0x0001, 0x0003
KEM_SUITE = b"KEM" + struct.pack(">H", KEM_ID)
HPKE_SUITE = b"HPKE" + struct.pack(">HHH", KEM_ID, KDF_ID, AEAD_ID)


def labeled_extract(suite: bytes, salt: bytes, label: bytes, ikm: bytes) -> bytes:
    return extract(salt, b"HPKE-v1" + suite + label + ikm)


def labeled_expand(suite: bytes, prk: bytes, label: bytes, info: bytes, n: int) -> bytes:
    return expand(prk, struct.pack(">H", n) + b"HPKE-v1" + suite + label + info, n)


class Recipient:
    def __init__(self, sk: X25519PrivateKey, enc: bytes, info: bytes):
        pk_r = sk.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)
        dh = sk.exchange(X25519PublicKey.from_public_bytes(enc))
        eae_prk = labeled_extract(KEM_SUITE, b"", b"eae_prk", dh)
        shared = labeled_expand(KEM_SUITE, eae_prk, b"shared_secret", enc + pk_r, 32)

        psk_id_hash = labeled_extract(HPKE_SUITE, b"", b"psk_id_hash", b"")
        info_hash = labeled_extract(HPKE_SUITE, b"", b"info_hash", info)
        context = b"\x00" + psk_id_hash + info_hash
        secret = labeled_extract(HPKE_SUITE, shared, b"secret", b"")
        self.key = labeled_expand(HPKE_SUITE, secret, b"key", context, 32)
        self.nonce = labeled_expand(HPKE_SUITE, secret, b"base_nonce", context, 12)
        self.exporter = labeled_expand(HPKE_SUITE, secret, b"exp", context, 32)

    def open_first(self, ct: bytes) -> bytes:
        return ChaCha20Poly1305(self.key).decrypt(self.nonce, ct, b"")

    def export(self, what: bytes, n: int = 32) -> bytes:
        return labeled_expand(HPKE_SUITE, self.exporter, b"sec", what, n)


def derive(secret: bytes, anchor: bytes) -> dict:
    prk = extract(LABEL + b"device", secret)
    tag_key = expand(prk, b"tag\x00" + anchor)
    return {
        "tag": mac(tag_key, b"device")[:16],
        "bind": expand(prk, b"bind\x00" + anchor),
        "stream": expand(prk, b"stream\x00" + anchor),
    }


def open_chunk(key: bytes, n: int, sealed: bytes) -> list:
    nonce = b"\x00" * 4 + struct.pack("<Q", n)
    plain = ChaCha20Poly1305(key).decrypt(nonce, sealed, b"")
    assert len(plain) <= 16000, "a chunk longer than 16 000 bytes"
    items, i = [], 0
    while i + 2 <= len(plain):
        (size,) = struct.unpack(">H", plain[i : i + 2])
        if size == 0:
            break
        assert 32 <= size <= 1600 and i + 2 + size <= len(plain), "a bad item"
        items.append(plain[i + 2 : i + 2 + size])
        i += 2 + size
    assert all(b == 0 for b in plain[i:]), "padding that is not zero"
    return items


TOKEN_CHARS = set("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_")
HTTP_TOKEN = TOKEN_CHARS | set("!#$%&'*+.^`|~")


def cookie_token(field: str, name: str):
    """The value of the cookie name in one Cookie field, or None: pairs joined
    by "; ", every name an HTTP token, name there once, its value one or more
    characters of base64url."""
    found = None
    for pair in field.split("; "):
        key, eq, value = pair.partition("=")
        if not eq or not key or not set(key) <= HTTP_TOKEN:
            return None
        if key == name:
            if found is not None:
                return None
            found = value
    if not found or not set(found) <= TOKEN_CHARS:
        return None
    return found


def open_body(key: bytes, first: int, body: bytes) -> list:
    """The chunks of a wui-poll body, at numbers from first: each behind its
    length, all of them opening, the body 18 to 65 536 bytes."""
    assert 18 <= len(body) <= 65536, "a body of the wrong length"
    chunks, i, n = [], 0, first
    while i < len(body):
        assert i + 2 <= len(body), "a body that ends inside a length"
        (size,) = struct.unpack(">H", body[i : i + 2])
        assert 16 <= size <= 16016 and i + 2 + size <= len(body), "a chunk of the wrong length"
        chunks.append(open_chunk(key, n, body[i + 2 : i + 2 + size]))
        i, n = i + 2 + size, n + 1
    return chunks


def poll_query(q: str):
    """The session id and number of a later wui-poll request, or None."""
    text, dot, digits = q.partition(".")
    if not dot or len(text) != 22 or not set(text) <= TOKEN_CHARS:
        return None
    raw = base64.urlsafe_b64decode(text + "==")
    if len(raw) != 16 or base64.urlsafe_b64encode(raw).rstrip(b"=").decode() != text:
        return None
    if not (1 <= len(digits) <= 10) or not digits.isascii() or not digits.isdigit() or digits[0] == "0":
        return None
    n = int(digits)
    return (raw, n) if n <= 0xFFFFFFFF else None


def info_of(transport: str, anchor: bytes) -> bytes:
    return LABEL + b"proof\x00" + transport.encode() + b"\x00" + anchor


def check(name: str) -> None:
    path = pathlib.Path(__file__).resolve().parents[2] / "internal" / "stealth" / "testdata" / name
    v = json.loads(path.read_text())
    h = bytes.fromhex
    secret, anchor = h(v["secret"]), h(v["anchor"])
    dev = derive(secret, anchor)

    token = v["token"]
    assert 144 <= len(token) <= 272, "a token of the wrong length"
    assert set(token) <= TOKEN_CHARS, "a token that is not unpadded base64url"
    if v["transport"] == "wui-plain":
        cookie = v["cookie"].partition("=")[0]
        assert cookie_token(v["cookie"], cookie) == token, "the token does not read back from its Cookie field"
        assert cookie_token(v["cookie"] + "; " + v["cookie"], cookie) is None, "a cookie named twice was read"
    raw = base64.urlsafe_b64decode(token + "=" * (-len(token) % 4))
    enc, ct = raw[:32], raw[32:]

    sk = X25519PrivateKey.from_private_bytes(h(v["transportKey"]))
    r = Recipient(sk, enc, info_of(v["transport"], anchor))
    hello = r.open_first(ct)
    for other in ("wui-ws", "wui-tls", "wui-plain", "wui-poll"):
        if other == v["transport"]:
            continue
        try:
            Recipient(sk, enc, info_of(other, anchor)).open_first(ct)
        except Exception:
            continue
        raise AssertionError(f"a {v['transport']} token opened as {other}")

    assert 60 <= len(hello) <= 60 + PROOF_PAD, "a hello of the wrong length"
    assert hello[0] == 1 and hello[1:4] == b"\x00\x00\x00", "a bad version or reserved bytes"
    (ts,) = struct.unpack(">Q", hello[4:12])
    assert ts == v["timestamp"], "the timestamp differs"
    assert hello[12:28] == dev["tag"], "the tag differs"
    assert hmac.compare_digest(hello[28:60], mac(dev["bind"], r.export(LABEL + b"binder") + hello[:28])), "the binder differs"
    assert all(b == 0 for b in hello[60:]), "the proof's padding is not zero"

    prk = extract(r.export(LABEL + b"stream"), dev["stream"])
    up, down = expand(prk, LABEL + b"stream up"), expand(prk, LABEL + b"stream down")
    assert up == h(v["up"]) and down == h(v["down"]), "the session keys differ"

    assert open_chunk(up, 0, h(v["chunkUp"])) == [h(v["datagram"])], "the chunk up differs"
    assert open_chunk(down, 0, h(v["chunkDown"])) == [], "the chunk down is not padding alone"

    if v["transport"] == "wui-poll":
        sid = expand(prk, LABEL + b"stream id", 16)
        assert sid == h(v["id"]), "the session id differs"
        assert poll_query(v["query"]) == (sid, 1), "the query does not read back as the id and 1"
        for bad in (v["query"] + "0" * 10, v["query"][:-1] + "0", v["query"][:-1] + "01", v["query"][1:]):
            assert poll_query(bad) is None, f"{bad!r} was read as a later request"
        assert open_body(up, 0, h(v["bodyUp"])) == [[h(v["datagram"])]], "the body up differs"
        assert open_body(down, 0, h(v["bodyDown"])) == [[]], "the body down is not padding alone"

    print(f"{name} matches the specification")


def main() -> int:
    check("ws.json")
    check("tls.json")
    check("plain.json")
    check("poll.json")
    return 0


if __name__ == "__main__":
    sys.exit(main())
