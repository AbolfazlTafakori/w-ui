package routing

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Xray no longer takes allowInsecure: a server whose certificate does not
// verify is reached only by pinning that certificate. A link that asks to
// skip the check -- allowInsecure=1, insecure=1, common with self-signed
// servers -- is kept with allowInsecure in its tlsSettings, saying what the
// operator wanted, and turned into a pin here, when the hop starts.

// skipsVerify reports whether the outbound asks for its certificate not to
// be checked and has no pin or name of its own to check it by.
func skipsVerify(outbound map[string]any) (tlsSettings map[string]any, ok bool) {
	st, _ := outbound["streamSettings"].(map[string]any)
	if st == nil {
		return nil, false
	}
	ts, _ := st["tlsSettings"].(map[string]any)
	if ts == nil {
		return nil, false
	}
	on, _ := ts["allowInsecure"].(bool)
	return ts, on
}

// isHysteria is an outbound xray carries over QUIC, whose certificate this
// panel cannot fetch over TCP: sing-box carries those itself when asked to
// skip the check.
func isHysteria(outbound map[string]any) bool {
	p, _ := outbound["protocol"].(string)
	return p == "hysteria"
}

// pinFirstCert replaces allowInsecure with the hash of the certificate the
// server presents now. A pin the outbound already has is kept and the flag
// just dropped.
func pinFirstCert(ctx context.Context, outbound map[string]any, host string, port int) error {
	ts, ok := skipsVerify(outbound)
	if !ok {
		return nil
	}
	delete(ts, "allowInsecure")
	if pin, _ := ts["pinnedPeerCertSha256"].(string); pin != "" {
		return nil
	}
	sni, _ := ts["serverName"].(string)
	var alpn []string
	if a, ok := ts["alpn"].([]any); ok {
		for _, v := range a {
			if s, ok := v.(string); ok && s != "" {
				alpn = append(alpn, s)
			}
		}
	}
	hash, err := fetchCertHash(ctx, host, port, sni, alpn)
	if err != nil {
		return fmt.Errorf("could not read the certificate of %s to trust it: %w", net.JoinHostPort(host, strconv.Itoa(port)), err)
	}
	ts["pinnedPeerCertSha256"] = hash
	return nil
}

func fetchCertHash(ctx context.Context, host string, port int, sni string, alpn []string) (string, error) {
	if sni == "" {
		sni = host
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := tls.Dialer{Config: &tls.Config{
		ServerName:         sni,
		NextProtos:         alpn,
		InsecureSkipVerify: true, // read, not trusted: the hash is what is trusted
		MinVersion:         tls.VersionTLS12,
	}}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return "", err
	}
	defer c.Close()
	certs := c.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("it presented none")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

// hysteriaForSingbox is the sing-box outbound for an xray hysteria outbound
// that asks to skip the certificate check.
func hysteriaForSingbox(outbound map[string]any) (map[string]any, error) {
	set, _ := outbound["settings"].(map[string]any)
	st, _ := outbound["streamSettings"].(map[string]any)
	if set == nil || st == nil {
		return nil, errors.New("the hysteria outbound has no settings")
	}
	host, _ := set["address"].(string)
	port, _ := set["port"].(float64)
	hs, _ := st["hysteriaSettings"].(map[string]any)
	auth, _ := hs["auth"].(string)
	if host == "" || port <= 0 {
		return nil, errors.New("the hysteria outbound has no server")
	}
	ts, _ := st["tlsSettings"].(map[string]any)
	tlsOut := map[string]any{"enabled": true, "insecure": true}
	if sni, _ := ts["serverName"].(string); sni != "" {
		tlsOut["server_name"] = sni
	}
	if a, ok := ts["alpn"].([]any); ok && len(a) > 0 {
		tlsOut["alpn"] = a
	}
	out := map[string]any{
		"type":        "hysteria2",
		"tag":         "proxy",
		"server":      host,
		"server_port": int(port),
		"password":    auth,
		"tls":         tlsOut,
	}
	if fm, ok := st["finalmask"].(map[string]any); ok {
		if udp, ok := fm["udp"].([]any); ok && len(udp) > 0 {
			m, _ := udp[0].(map[string]any)
			if t, _ := m["type"].(string); t == "salamander" {
				s, _ := m["settings"].(map[string]any)
				pw, _ := s["password"].(string)
				out["obfs"] = map[string]any{"type": "salamander", "password": pw}
			}
		}
	}
	return out, nil
}
