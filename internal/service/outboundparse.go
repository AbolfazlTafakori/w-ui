package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Turning what an operator pastes into an outbound.
//
// The Xray family is parsed the way 3x-ui parses a share link, into the same
// Xray outbound object 3x-ui would show in its JSON tab, and that object is
// what runs -- verbatim -- so anything a link can say, xray hears. A WireGuard
// configuration file becomes a WireGuard hop, an OpenVPN profile an OpenVPN
// hop, and an Xray outbound object pasted as JSON is taken as it is.

// ParseOutbound reads a share link, a WireGuard .conf, an OpenVPN profile, or
// an Xray outbound JSON object, and returns the outbound it describes.
func ParseOutbound(text string) (*OutboundInput, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, invalidField("text", "nothing to import")
	}
	var in *OutboundInput
	var err error
	switch {
	case strings.HasPrefix(text, "{"):
		in, err = parseXrayJSON(text)
	case strings.Contains(text, "[Interface]"):
		in, err = parseWireGuardConf(text)
	case looksLikeOpenVPN(text):
		in, err = parseOpenVPNProfile(text)
	default:
		if i := strings.Index(text, "://"); i > 0 && !strings.ContainsAny(text[:i], " \n") {
			in, err = parseShareLink(text)
		} else {
			err = invalidField("text", "not a share link, a WireGuard configuration, an OpenVPN profile or an Xray outbound")
		}
	}
	if err != nil {
		return nil, err
	}
	// A remark is anything -- flags, spaces, slashes. A tag is a name.
	in.Tag = cleanTag(in.Tag)
	return in, nil
}

// ── share links ──────────────────────────────────────────────────────────────

func parseShareLink(link string) (*OutboundInput, error) {
	scheme := strings.ToLower(link[:strings.Index(link, "://")])
	switch scheme {
	case "vmess":
		return parseVmess(link)
	case "vless":
		return parseVless(link)
	case "trojan":
		return parseTrojan(link)
	case "ss":
		return parseShadowsocks(link)
	case "hysteria2", "hy2":
		return parseHysteria2(link)
	case "wireguard", "wg":
		return parseWireGuardLink(link)
	case "socks", "socks5", "socks5h", "http", "https":
		return parseProxyShareLink(link)
	}
	return nil, invalidField("text", "%q links are not something this panel can run", scheme)
}

func b64decode(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", errors.New("not base64")
}

func remark(u *url.URL) string {
	if r, err := url.PathUnescape(u.Fragment); err == nil {
		return r
	}
	return u.Fragment
}

func portOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 && n < 65536 {
		return n
	}
	return def
}

func firstParam(q url.Values, keys ...string) string {
	for _, k := range keys {
		if v := q.Get(k); v != "" {
			return v
		}
	}
	return ""
}

// buildStream is 3x-ui's buildStream: the streamSettings object for a
// transport and a security, with the defaults their form starts from.
func buildStream(network, security string) map[string]any {
	st := map[string]any{"network": network, "security": security}
	switch network {
	case "kcp":
		st["kcpSettings"] = map[string]any{"mtu": 1350, "tti": 20, "uplinkCapacity": 5, "downlinkCapacity": 20, "cwndMultiplier": 1, "maxSendingWindow": 2097152}
	case "ws":
		st["wsSettings"] = map[string]any{"path": "/", "host": "", "headers": map[string]any{}, "heartbeatPeriod": 0}
	case "grpc":
		st["grpcSettings"] = map[string]any{"serviceName": "", "authority": "", "multiMode": false}
	case "httpupgrade":
		st["httpupgradeSettings"] = map[string]any{"path": "/", "host": "", "headers": map[string]any{}}
	case "xhttp":
		st["xhttpSettings"] = map[string]any{"path": "/", "host": "", "mode": "auto", "headers": map[string]any{}, "xPaddingBytes": "100-1000"}
	default:
		st["network"] = "tcp"
		st["tcpSettings"] = map[string]any{"header": map[string]any{"type": "none"}}
	}
	switch security {
	case "tls":
		st["tlsSettings"] = map[string]any{"serverName": "", "alpn": []string{}, "fingerprint": "", "echConfigList": "", "verifyPeerCertByName": "", "pinnedPeerCertSha256": ""}
	case "reality":
		st["realitySettings"] = map[string]any{"publicKey": "", "fingerprint": "chrome", "serverName": "", "shortId": "", "spiderX": "", "mldsa65Verify": ""}
	}
	return st
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// applyTransport reads the transport parameters of a vless/trojan link.
func applyTransport(st map[string]any, q url.Values) {
	host := q.Get("host")
	path := q.Get("path")
	if path == "" {
		path = "/"
	}
	switch st["network"] {
	case "ws":
		ws := st["wsSettings"].(map[string]any)
		ws["host"], ws["path"] = host, path
	case "grpc":
		g := st["grpcSettings"].(map[string]any)
		g["serviceName"] = firstParam(q, "serviceName", "path")
		g["authority"] = q.Get("authority")
		g["multiMode"] = q.Get("mode") == "multi"
	case "httpupgrade":
		h := st["httpupgradeSettings"].(map[string]any)
		h["host"], h["path"] = host, path
	case "xhttp":
		x := st["xhttpSettings"].(map[string]any)
		x["host"], x["path"] = host, path
		if m := q.Get("mode"); m != "" {
			x["mode"] = m
		}
		if extra := q.Get("extra"); extra != "" {
			var parsed map[string]any
			if json.Unmarshal([]byte(extra), &parsed) == nil {
				for k, v := range parsed {
					x[k] = v
				}
			}
		}
	case "tcp":
		if q.Get("headerType") == "http" || q.Get("type") == "http" {
			req := map[string]any{"version": "1.1", "method": "GET", "path": splitCSV(path), "headers": map[string]any{}}
			if host != "" {
				req["headers"] = map[string]any{"Host": splitCSV(host)}
			}
			st["tcpSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	}
}

func applySecurity(st map[string]any, q url.Values) {
	switch st["security"] {
	case "tls":
		tls := st["tlsSettings"].(map[string]any)
		tls["serverName"] = q.Get("sni")
		tls["fingerprint"] = q.Get("fp")
		if a := q.Get("alpn"); a != "" {
			tls["alpn"] = splitCSV(a)
		}
		tls["echConfigList"] = q.Get("ech")
		tls["verifyPeerCertByName"] = q.Get("vcn")
		tls["pinnedPeerCertSha256"] = q.Get("pcs")
	case "reality":
		r := st["realitySettings"].(map[string]any)
		r["serverName"] = q.Get("sni")
		if fp := q.Get("fp"); fp != "" {
			r["fingerprint"] = fp
		}
		r["publicKey"] = q.Get("pbk")
		r["shortId"] = q.Get("sid")
		r["spiderX"] = q.Get("spx")
		r["mldsa65Verify"] = q.Get("pqv")
	}
}

func xrayInput(kind model.OutboundKind, tag, address string, port int, outbound map[string]any) (*OutboundInput, error) {
	raw, err := json.MarshalIndent(outbound, "", "  ")
	if err != nil {
		return nil, err
	}
	return &OutboundInput{
		Tag:     tag,
		Kind:    kind,
		Address: net.JoinHostPort(address, strconv.Itoa(port)),
		Config:  string(raw),
	}, nil
}

func parseVmess(link string) (*OutboundInput, error) {
	dec, err := b64decode(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		return nil, invalidField("text", "that vmess link is not base64")
	}
	var j map[string]any
	if err := json.Unmarshal([]byte(dec), &j); err != nil {
		return nil, invalidField("text", "that vmess link does not carry JSON")
	}
	str := func(k string) string {
		switch v := j[k].(type) {
		case string:
			return v
		case float64:
			return strconv.Itoa(int(v))
		}
		return ""
	}
	network := str("net")
	if network == "" {
		network = "tcp"
	}
	security := "none"
	if str("tls") == "tls" {
		security = "tls"
	}
	st := buildStream(network, security)
	host, path := str("host"), str("path")
	if path == "" {
		path = "/"
	}
	switch network {
	case "tcp":
		if str("type") == "http" {
			req := map[string]any{"version": "1.1", "method": "GET", "path": splitCSV(path), "headers": map[string]any{}}
			if host != "" {
				req["headers"] = map[string]any{"Host": splitCSV(host)}
			}
			st["tcpSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	case "ws":
		ws := st["wsSettings"].(map[string]any)
		ws["host"], ws["path"] = host, path
	case "grpc":
		g := st["grpcSettings"].(map[string]any)
		g["serviceName"] = str("path")
		g["authority"] = str("authority")
		g["multiMode"] = str("type") == "multi"
	case "httpupgrade":
		h := st["httpupgradeSettings"].(map[string]any)
		h["host"], h["path"] = host, path
	case "xhttp":
		x := st["xhttpSettings"].(map[string]any)
		x["host"], x["path"] = host, path
		if m := str("mode"); m != "" {
			x["mode"] = m
		}
	}
	if security == "tls" {
		tls := st["tlsSettings"].(map[string]any)
		tls["serverName"] = str("sni")
		tls["fingerprint"] = str("fp")
		if a := str("alpn"); a != "" {
			tls["alpn"] = splitCSV(a)
		}
	}
	port := portOr(str("port"), 443)
	scy := str("scy")
	if scy == "" || scy == "none" || scy == "zero" {
		scy = "auto"
	}
	address := str("add")
	ob := map[string]any{
		"protocol": "vmess",
		"settings": map[string]any{"vnext": []any{map[string]any{
			"address": address, "port": port,
			"users": []any{map[string]any{"id": str("id"), "security": scy}},
		}}},
		"streamSettings": st,
	}
	return xrayInput(model.OutboundVMess, str("ps"), address, port, ob)
}

func parseVless(link string) (*OutboundInput, error) {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		return nil, invalidField("text", "that vless link is malformed")
	}
	q := u.Query()
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	security := q.Get("security")
	if security == "" {
		security = "none"
	}
	st := buildStream(network, security)
	applyTransport(st, q)
	applySecurity(st, q)
	port := portOr(u.Port(), 443)
	enc := q.Get("encryption")
	if enc == "" {
		enc = "none"
	}
	ob := map[string]any{
		"protocol": "vless",
		"settings": map[string]any{"vnext": []any{map[string]any{
			"address": u.Hostname(), "port": port,
			"users": []any{map[string]any{"id": u.User.Username(), "flow": q.Get("flow"), "encryption": enc}},
		}}},
		"streamSettings": st,
	}
	return xrayInput(model.OutboundVLESS, remark(u), u.Hostname(), port, ob)
}

func parseTrojan(link string) (*OutboundInput, error) {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		return nil, invalidField("text", "that trojan link is malformed")
	}
	q := u.Query()
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	security := q.Get("security")
	if security == "" {
		security = "tls"
	}
	st := buildStream(network, security)
	applyTransport(st, q)
	applySecurity(st, q)
	port := portOr(u.Port(), 443)
	ob := map[string]any{
		"protocol": "trojan",
		"settings": map[string]any{"servers": []any{map[string]any{
			"address": u.Hostname(), "port": port, "password": u.User.Username(),
		}}},
		"streamSettings": st,
	}
	return xrayInput(model.OutboundTrojan, remark(u), u.Hostname(), port, ob)
}

func parseShadowsocks(link string) (*OutboundInput, error) {
	rest := strings.TrimPrefix(link, "ss://")
	tag := ""
	if i := strings.Index(rest, "#"); i >= 0 {
		tag, _ = url.PathUnescape(rest[i+1:])
		rest = rest[:i]
	}
	if i := strings.Index(rest, "?"); i >= 0 {
		rest = rest[:i]
	}
	var userInfo, hostPort string
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		raw := rest[:at]
		if strings.Contains(raw, ":") {
			if d, err := url.PathUnescape(raw); err == nil {
				userInfo = d
			} else {
				userInfo = raw
			}
		} else if d, err := b64decode(raw); err == nil {
			userInfo = d
		} else {
			userInfo = raw
		}
		hostPort = rest[at+1:]
	} else {
		d, err := b64decode(rest)
		if err != nil {
			return nil, invalidField("text", "that ss link is malformed")
		}
		at := strings.LastIndex(d, "@")
		if at < 0 {
			return nil, invalidField("text", "that ss link is malformed")
		}
		userInfo, hostPort = d[:at], d[at+1:]
	}
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, invalidField("text", "that ss link has no host and port")
	}
	method, password := "2022-blake3-aes-128-gcm", userInfo
	if i := strings.Index(userInfo, ":"); i >= 0 {
		method, password = userInfo[:i], userInfo[i+1:]
	}
	port := portOr(portStr, 443)
	ob := map[string]any{
		"protocol": "shadowsocks",
		"settings": map[string]any{"servers": []any{map[string]any{
			"address": host, "port": port, "password": password, "method": method,
		}}},
	}
	return xrayInput(model.OutboundShadowsocks, tag, host, port, ob)
}

func parseHysteria2(link string) (*OutboundInput, error) {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		return nil, invalidField("text", "that hysteria2 link is malformed")
	}
	q := u.Query()
	port := portOr(u.Port(), 443)
	alpn := []string{"h3"}
	if a := q.Get("alpn"); a != "" {
		alpn = splitCSV(a)
	}
	st := map[string]any{
		"network":          "hysteria",
		"security":         "tls",
		"hysteriaSettings": map[string]any{"version": 2, "auth": u.User.Username(), "udpIdleTimeout": 60},
		"tlsSettings": map[string]any{
			"serverName": q.Get("sni"), "alpn": alpn, "fingerprint": q.Get("fp"),
			"echConfigList": q.Get("ech"), "verifyPeerCertByName": q.Get("vcn"),
			"pinnedPeerCertSha256": q.Get("pinSHA256"),
		},
	}
	if obfs := strings.ToLower(q.Get("obfs")); obfs == "salamander" || obfs == "gecko" {
		if pw := firstParam(q, "obfs-password", "obfs_password", "obfsPassword"); pw != "" {
			st["finalmask"] = map[string]any{"udp": []any{map[string]any{"type": "salamander", "settings": map[string]any{"password": pw}}}}
		}
	}
	ob := map[string]any{
		"protocol":       "hysteria",
		"settings":       map[string]any{"address": u.Hostname(), "port": port, "version": 2},
		"streamSettings": st,
	}
	return xrayInput(model.OutboundHysteria, remark(u), u.Hostname(), port, ob)
}

func parseProxyShareLink(link string) (*OutboundInput, error) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return nil, invalidField("text", "that proxy link is malformed")
	}
	kind := model.OutboundHTTP
	if strings.HasPrefix(u.Scheme, "socks") {
		kind = model.OutboundSOCKS
	}
	in := &OutboundInput{Tag: remark(u), Kind: kind, Address: u.Host}
	if u.User != nil {
		in.Username = u.User.Username()
		in.Password, _ = u.User.Password()
	}
	return in, nil
}

func parseWireGuardLink(link string) (*OutboundInput, error) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return nil, invalidField("text", "that wireguard link is malformed")
	}
	q := u.Query()
	pk, _ := url.PathUnescape(u.User.Username())
	in := &OutboundInput{
		Tag:          remark(u),
		Kind:         model.OutboundWireGuard,
		Address:      u.Host,
		PrivateKey:   pk,
		PeerPubKey:   firstParam(q, "publickey", "publicKey", "public_key", "peerPublicKey"),
		PresharedKey: firstParam(q, "presharedkey", "preshared_key", "pre-shared-key", "psk"),
		HopAddress:   firstParam(q, "address", "ip"),
		AllowedIPs:   firstParam(q, "allowedips", "allowed_ips"),
	}
	if mtu, err := strconv.Atoi(q.Get("mtu")); err == nil {
		in.HopMTU = mtu
	}
	if ka, err := strconv.Atoi(firstParam(q, "keepalive", "persistentkeepalive", "persistent_keepalive")); err == nil {
		in.Keepalive = ka
	}
	return in, nil
}

// ── files ────────────────────────────────────────────────────────────────────

// parseWireGuardConf reads a wg-quick style file. The first peer is the hop;
// a kernel hop here carries one.
func parseWireGuardConf(text string) (*OutboundInput, error) {
	in := &OutboundInput{Kind: model.OutboundWireGuard}
	section := ""
	peers := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			if section == "peer" {
				peers++
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch section {
		case "interface":
			switch k {
			case "privatekey":
				in.PrivateKey = v
			case "address":
				in.HopAddress = v
			case "mtu":
				in.HopMTU, _ = strconv.Atoi(v)
			case "dns":
				in.HopDNS = v
			}
		case "peer":
			if peers > 1 {
				continue
			}
			switch k {
			case "publickey":
				in.PeerPubKey = v
			case "presharedkey":
				in.PresharedKey = v
			case "endpoint":
				in.Address = v
			case "allowedips":
				in.AllowedIPs = v
			case "persistentkeepalive":
				in.Keepalive, _ = strconv.Atoi(v)
			}
		}
	}
	if in.PrivateKey == "" || in.PeerPubKey == "" || in.Address == "" {
		return nil, invalidField("text", "the WireGuard file needs PrivateKey, a peer PublicKey and an Endpoint")
	}
	return in, nil
}

func looksLikeOpenVPN(text string) bool {
	has := func(d string) bool {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), d) {
				return true
			}
		}
		return false
	}
	return has("client") && has("remote ") || has("<ca>") || has("remote ") && (has("dev ") || has("proto "))
}

// parseOpenVPNProfile keeps the profile whole and reads the first remote out
// of it, which is what the table shows as the address.
func parseOpenVPNProfile(text string) (*OutboundInput, error) {
	in := &OutboundInput{Kind: model.OutboundOpenVPN, Config: text}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[0] == "remote" {
			port := "1194"
			if len(f) >= 3 {
				port = f[2]
			}
			in.Address = net.JoinHostPort(f[1], port)
			break
		}
	}
	if in.Address == "" {
		return nil, invalidField("text", "the OpenVPN profile has no remote line")
	}
	return in, nil
}

// parseXrayJSON takes an Xray outbound object as 3x-ui shows one.
func parseXrayJSON(text string) (*OutboundInput, error) {
	var ob map[string]any
	if err := json.Unmarshal([]byte(text), &ob); err != nil {
		return nil, invalidField("text", "that is not valid JSON: %v", err)
	}
	// This panel's own export is also JSON; it names a kind rather than a
	// protocol, and is taken as an outbound input directly.
	if _, ours := ob["kind"]; ours {
		var in OutboundInput
		if err := json.Unmarshal([]byte(text), &in); err != nil {
			return nil, invalidField("text", "%v", err)
		}
		return &in, nil
	}
	proto, _ := ob["protocol"].(string)
	kind := model.OutboundKind(strings.ToLower(proto))
	if !kind.IsXray() {
		return nil, invalidField("text", "protocol %q is not one this panel can run", proto)
	}
	tag, _ := ob["tag"].(string)
	address, port := xrayTarget(ob)
	return xrayInput(kind, tag, address, port, ob)
}

// xrayTarget finds the server an Xray outbound object dials, wherever its
// protocol keeps it.
func xrayTarget(ob map[string]any) (string, int) {
	settings, _ := ob["settings"].(map[string]any)
	if settings == nil {
		return "", 0
	}
	first := func(key string) map[string]any {
		if arr, ok := settings[key].([]any); ok && len(arr) > 0 {
			if m, ok := arr[0].(map[string]any); ok {
				return m
			}
		}
		return nil
	}
	target := first("vnext")
	if target == nil {
		target = first("servers")
	}
	if target == nil {
		target = settings
	}
	addr, _ := target["address"].(string)
	port := 0
	switch p := target["port"].(type) {
	case float64:
		port = int(p)
	case string:
		port, _ = strconv.Atoi(p)
	}
	return addr, port
}

// xrayFromProxyFields builds the Xray outbound for a socks or http proxy
// typed into the form's fields, so those kinds run the same way the rest do.
func xrayFromProxyFields(in OutboundInput) (string, error) {
	host, portStr, err := net.SplitHostPort(in.Address)
	if err != nil {
		return "", err
	}
	port, _ := strconv.Atoi(portStr)
	server := map[string]any{"address": host, "port": port}
	if in.Username != "" {
		server["users"] = []any{map[string]any{"user": in.Username, "pass": in.Password}}
	}
	ob := map[string]any{
		"protocol": string(in.Kind),
		"settings": map[string]any{"servers": []any{server}},
	}
	raw, err := json.MarshalIndent(ob, "", "  ")
	return string(raw), err
}

// xrayConfigValid checks a pasted outbound object is JSON with the protocol
// the kind says, and returns it normalised.
func xrayConfigValid(kind model.OutboundKind, config string) (string, error) {
	var ob map[string]any
	if err := json.Unmarshal([]byte(config), &ob); err != nil {
		return "", fmt.Errorf("the outbound JSON is not valid: %v", err)
	}
	proto, _ := ob["protocol"].(string)
	if proto == "" {
		ob["protocol"] = string(kind)
	} else if !strings.EqualFold(proto, string(kind)) {
		return "", fmt.Errorf("the JSON says protocol %q but the outbound is %q", proto, kind)
	}
	raw, err := json.MarshalIndent(ob, "", "  ")
	return string(raw), err
}
