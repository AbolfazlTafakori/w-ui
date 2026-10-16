package service

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/abolfazl/w-ui/internal/wgkey"
)

// XrayKeys draws what an inbound's form needs drawn: a Reality key pair and
// short id, a WireGuard key pair, a Shadowsocks 2022 key of its cipher's
// size, a UUID, an MTProto fake-TLS secret.
func XrayKeys(kind, method, domain string) (map[string]string, error) {
	switch kind {
	case "reality":
		k, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		enc := base64.RawURLEncoding
		sid := make([]byte, 8)
		if _, err := rand.Read(sid); err != nil {
			return nil, err
		}
		return map[string]string{"privateKey": enc.EncodeToString(k.Bytes()), "publicKey": enc.EncodeToString(k.PublicKey().Bytes()),
			"shortId": hex.EncodeToString(sid)}, nil
	case "wireguard":
		k, err := wgkey.GeneratePrivate()
		if err != nil {
			return nil, err
		}
		pub, err := k.Public()
		if err != nil {
			return nil, err
		}
		return map[string]string{"privateKey": k.String(), "publicKey": pub.String()}, nil
	case "shadowsocks":
		size := 32
		if method == "2022-blake3-aes-128-gcm" {
			size = 16
		}
		b := make([]byte, size)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(method, "2022-") {
			return map[string]string{"password": hex.EncodeToString(b[:16])}, nil
		}
		return map[string]string{"password": base64.StdEncoding.EncodeToString(b)}, nil
	case "uuid":
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		b[6] = b[6]&0x0f | 0x40
		b[8] = b[8]&0x3f | 0x80
		h := hex.EncodeToString(b)
		return map[string]string{"uuid": h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]}, nil
	case "mtproto":
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		if domain == "" {
			domain = "www.cloudflare.com"
		}
		return map[string]string{"secret": "ee" + hex.EncodeToString(b) + hex.EncodeToString([]byte(domain))}, nil
	}
	return nil, fmt.Errorf("no key of kind %q", kind)
}
