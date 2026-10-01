package upgradecheck

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
)

// Profile is the part of one served configuration a customer's app depends
// on. Two releases may write a configuration differently -- a comment, the
// order of lines, a setting added -- and both still connect; what must not
// change under a customer is in here.
type Profile struct {
	Kind string            // "wireguard" or "openvpn"
	Keys map[string]string // the lines that decide whether it connects
}

// The WireGuard lines a client needs to reach the same server as the same
// peer. Jc to H4 are AmneziaWG's: a client and server that disagree on them
// never complete a handshake.
var wireguardKeys = map[string]bool{
	"interface.privatekey": true, "interface.address": true,
	"interface.jc": true, "interface.jmin": true, "interface.jmax": true,
	"interface.s1": true, "interface.s2": true,
	"interface.h1": true, "interface.h2": true, "interface.h3": true, "interface.h4": true,
	"peer.publickey": true, "peer.presharedkey": true, "peer.endpoint": true,
}

// The OpenVPN directives and blocks that decide the same.
var openvpnDirectives = map[string]bool{"remote": true, "proto": true, "dev": true, "auth-user-pass": true}
var openvpnBlocks = map[string]bool{"ca": true, "cert": true, "key": true, "tls-crypt": true, "tls-auth": true}

// ParseProfiles reads a subscription answer: one or more WireGuard or OpenVPN
// configurations, one after another, base64 or not.
func ParseProfiles(body []byte) ([]Profile, error) {
	text := strings.TrimSpace(string(body))
	if !strings.Contains(text, "[Interface]") && !strings.Contains(text, "client") {
		if dec, err := base64.StdEncoding.DecodeString(text); err == nil {
			text = string(dec)
		}
	}
	var out []Profile
	var cur *Profile
	section, block := "", ""
	var blockBody strings.Builder
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if block != "" {
			if line == "</"+block+">" {
				cur.Keys["<"+block+">"] = strings.TrimSpace(blockBody.String())
				block = ""
				blockBody.Reset()
			} else {
				blockBody.WriteString(line + "\n")
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		switch {
		case strings.EqualFold(line, "[Interface]"):
			out = append(out, Profile{Kind: "wireguard", Keys: map[string]string{}})
			cur, section = &out[len(out)-1], "interface"
			continue
		case strings.EqualFold(line, "[Peer]"):
			section = "peer"
			continue
		case line == "client" && (cur == nil || cur.Kind != "openvpn" || len(cur.Keys) > 0):
			out = append(out, Profile{Kind: "openvpn", Keys: map[string]string{}})
			cur, section = &out[len(out)-1], ""
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("a line before any configuration: %q", line)
		}
		if cur.Kind == "wireguard" {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key := section + "." + strings.ToLower(strings.TrimSpace(k))
			if wireguardKeys[key] {
				cur.Keys[key] = strings.TrimSpace(v)
			}
			continue
		}
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") && !strings.HasPrefix(line, "</") {
			name := strings.Trim(line, "<>")
			if openvpnBlocks[name] {
				block = name
			}
			continue
		}
		word, rest, _ := strings.Cut(line, " ")
		if openvpnDirectives[word] {
			v := strings.Join(strings.Fields(rest), " ")
			// remote repeats, one per address the client may dial, in order.
			if prev, ok := cur.Keys[word]; ok && word == "remote" {
				v = prev + ", " + v
			}
			cur.Keys[word] = v
		}
	}
	return out, sc.Err()
}

// Validate says what is missing from a profile no app could connect with.
func (p Profile) Validate() error {
	need := []string{"interface.privatekey", "interface.address", "peer.publickey", "peer.endpoint"}
	if p.Kind == "openvpn" {
		need = []string{"remote", "<ca>"}
	}
	for _, k := range need {
		if p.Keys[k] == "" {
			return fmt.Errorf("a %s configuration without %s", p.Kind, k)
		}
	}
	return nil
}

// fingerprint is the profile as one comparable line.
func (p Profile) fingerprint() string {
	keys := make([]string, 0, len(p.Keys))
	for k := range p.Keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(p.Kind)
	for _, k := range keys {
		fmt.Fprintf(&b, "|%s=%s", k, p.Keys[k])
	}
	return b.String()
}

// SameProfiles reports how two answers differ in what a customer's app
// depends on: nil when every configuration in one is in the other.
func SameProfiles(old, now []Profile) error {
	count := func(ps []Profile) map[string]int {
		m := map[string]int{}
		for _, p := range ps {
			m[p.fingerprint()]++
		}
		return m
	}
	a, b := count(old), count(now)
	var diffs []string
	for f, n := range a {
		if b[f] != n {
			diffs = append(diffs, "was served, is not now: "+redact(f))
		}
	}
	for f, n := range b {
		if a[f] != n {
			diffs = append(diffs, "is served now, was not: "+redact(f))
		}
	}
	if len(diffs) == 0 {
		return nil
	}
	sort.Strings(diffs)
	return fmt.Errorf("%s", strings.Join(diffs, "; "))
}

// redact shortens keys and certificates in a message to their first few
// characters: enough to tell two apart, without a test log carrying them.
func redact(f string) string {
	parts := strings.Split(f, "|")
	for i, p := range parts {
		if k, v, ok := strings.Cut(p, "="); ok && len(v) > 12 {
			parts[i] = k + "=" + v[:8] + "…"
		}
	}
	return strings.Join(parts, "|")
}
