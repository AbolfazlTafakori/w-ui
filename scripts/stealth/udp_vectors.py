#!/usr/bin/env python3
"""Checks internal/stealth/testdata/udp.json against docs/reference/stealth.md.

An implementation of the veil packets of its own, written from the
specification and not from the Go code, so that what the panel sends and what
the specification says are known to be the same. It opens the opening and the
two data packets of the vector, derives every key again, and fails on the
first thing that differs.

    python3 scripts/stealth/udp_vectors.py

It needs the `cryptography` package.
"""

import hashlib
import hmac
import json
import pathlib
import struct
import sys

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305
from cryptography.hazmat.primitives.kdf.hkdf import HKDF, HKDFExpand

LABEL = b"w-ui stealth v1 "
EPOCH = 120


def extract(salt: bytes, ikm: bytes) -> bytes:
    return hmac.new(salt, ikm, hashlib.sha256).digest()


def expand(prk: bytes, info: bytes) -> bytes:
    return HKDFExpand(hashes.SHA256(), 32, info).derive(prk)


def mac(key: bytes, msg: bytes) -> bytes:
    return hmac.new(key, msg, hashlib.sha256).digest()


def device_keys(secret: bytes, anchor: bytes):
    prk = extract(LABEL + b"device", secret)
    tag_key = expand(prk, b"tag\x00" + anchor)
    udp_root = expand(prk, b"udp\x00" + anchor)
    return tag_key, udp_root


def epoch_tag(tag_key: bytes, epoch: int) -> bytes:
    return mac(tag_key, b"epoch" + struct.pack(">Q", epoch))[:16]


def mask_key(anchor: bytes) -> bytes:
    return HKDF(hashes.SHA256(), 32, salt=LABEL + b"mask", info=b"").derive(anchor)


def items(plain: bytes):
    """The datagrams of a chunk, refusing any malformed one."""
    out, i = [], 0
    while i + 2 <= len(plain):
        n = struct.unpack(">H", plain[i : i + 2])[0]
        if n == 0:
            break
        assert 32 <= n <= 1600 and i + 2 + n <= len(plain), "bad item"
        out.append(plain[i + 2 : i + 2 + n])
        i += 2 + n
    assert all(b == 0 for b in plain[i:]), "padding is not zero"
    return out


def main() -> int:
    root = pathlib.Path(__file__).resolve().parents[2]
    v = json.loads((root / "internal/stealth/testdata/udp.json").read_text())
    secret, anchor = bytes.fromhex(v["secret"]), bytes.fromhex(v["anchor"])
    datagram, opening = bytes.fromhex(v["datagram"]), bytes.fromhex(v["opening"])

    tag_key, udp_root = device_keys(secret, anchor)

    # The opening: masked tag, salt, then the seal over the rest.
    masked, salt, sealed = opening[:16], opening[16:48], opening[48:]
    tag = bytes(a ^ b for a, b in zip(masked, mac(mask_key(anchor), salt)[:16]))
    assert tag == epoch_tag(tag_key, v["timestamp"] // EPOCH), "the tag is not the epoch's"

    prk = extract(salt, udp_root)
    k_open = expand(prk, LABEL + b"udp open")
    plain = ChaCha20Poly1305(k_open).decrypt(bytes(12), sealed, opening[:48])
    assert plain[:4] == b"\x01\x00\x00\x00", "version and zero bytes"
    assert struct.unpack(">Q", plain[4:12])[0] == v["timestamp"], "timestamp"
    assert items(plain[12:]) == [datagram], "the opening's datagram"
    assert len(opening) >= 122 and len(opening) <= v["room"], "the opening's size"

    up, down = expand(prk, LABEL + b"udp up"), expand(prk, LABEL + b"udp down")
    assert up.hex() == v["up"] and down.hex() == v["down"], "the session keys"

    # The data packets: a random nonce, then the seal, no associated data.
    for name, key, want in (("dataUp", up, [datagram]), ("dataDown", down, [])):
        p = bytes.fromhex(v[name])
        got = items(ChaCha20Poly1305(key).decrypt(p[:12], p[12:], None))
        assert got == want, name
        assert len(p) <= v["room"], name + " size"

    print("udp.json agrees with the specification")
    return 0


if __name__ == "__main__":
    sys.exit(main())
