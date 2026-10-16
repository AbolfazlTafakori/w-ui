package service

import (
	"strconv"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// What a 3x-ui or Pasarguard node can serve, by its version.
//
// Read from those panels' own sources, release by release, so the form offers
// a server what it has and nothing it does not: a 3x-ui 2.5 has no Hysteria,
// a 3x-ui before 3.4 no MTProto, and neither panel serves OpenVPN at all.

// InboundKind is one kind of inbound a node can be given.
type InboundKind struct {
	Protocol string `json:"protocol"`
	// Customers says whether this panel's customers can be put on it: it
	// has a client per user that panel counts. An inbound without -- a
	// tunnel, a SOCKS or HTTP proxy with shared accounts -- is still made
	// and kept, for the operator's own use.
	Customers bool `json:"customers"`
	// Stream says it takes a transport and a security (streamSettings);
	// Securities are the ones it can have; Transports the transports.
	Stream     bool     `json:"stream"`
	Securities []string `json:"securities,omitempty"`
	Transports []string `json:"transports,omitempty"`
}

// The transports xray inbounds take, as the panels' forms list them.
var xrayTransports = []string{"tcp", "ws", "grpc", "httpupgrade", "xhttp", "kcp"}

// InboundKinds is what a node of this kind and version can serve, in the
// order the panel's own form lists them. An unknown version is taken as the
// newest, which is what a panel that does not say is most likely to be.
func InboundKinds(panel model.NodeKind, version string) []InboundKind {
	v := strings.TrimSpace(version)
	if i := strings.LastIndex(v, " "); i >= 0 {
		v = v[i+1:]
	}
	at := func(major, minor int) bool { return v == "" || !olderThan(v, major, minor) }
	full := []string{"none", "tls", "reality"}
	noReality := []string{"none", "tls"}
	switch panel {
	case model.KindXUI:
		out := []InboundKind{
			{Protocol: "vless", Customers: true, Stream: true, Securities: full, Transports: xrayTransports},
			{Protocol: "vmess", Customers: true, Stream: true, Securities: noReality, Transports: xrayTransports},
			{Protocol: "trojan", Customers: true, Stream: true, Securities: full, Transports: xrayTransports},
			{Protocol: "shadowsocks", Customers: true, Stream: true, Securities: noReality, Transports: xrayTransports},
		}
		if at(2, 9) {
			out = append(out, InboundKind{Protocol: "hysteria", Customers: true, Stream: true, Securities: []string{"tls"}})
		}
		if at(3, 8) {
			out = append(out, InboundKind{Protocol: "tuic", Customers: true})
		}
		if at(3, 4) {
			out = append(out, InboundKind{Protocol: "mtproto", Customers: true})
		}
		if at(2, 5) {
			// Clients of its own, that panel counts, from 3.1; before it a
			// WireGuard inbound had peers only.
			out = append(out, InboundKind{Protocol: "wireguard", Customers: at(3, 1)})
		}
		if at(3, 8) {
			out = append(out, InboundKind{Protocol: "amneziawg", Customers: true})
		}
		if at(3, 10) {
			// Over a transport of its own, always TLS.
			out = append(out, InboundKind{Protocol: "masque", Customers: true, Stream: true, Securities: []string{"tls"}})
		}
		if at(2, 8) {
			out = append(out,
				InboundKind{Protocol: "mixed", Stream: true, Securities: noReality, Transports: []string{"tcp"}},
				InboundKind{Protocol: "tunnel", Stream: true, Securities: []string{"none"}, Transports: []string{"tcp"}})
		} else if at(2, 5) {
			out = append(out,
				InboundKind{Protocol: "socks"},
				InboundKind{Protocol: "dokodemo-door"})
		}
		out = append(out, InboundKind{Protocol: "http", Stream: true, Securities: noReality, Transports: []string{"tcp"}})
		return out
	case model.KindPG:
		return []InboundKind{
			{Protocol: "vless", Customers: true, Stream: true, Securities: full, Transports: xrayTransports},
			{Protocol: "vmess", Customers: true, Stream: true, Securities: noReality, Transports: xrayTransports},
			{Protocol: "trojan", Customers: true, Stream: true, Securities: full, Transports: xrayTransports},
			{Protocol: "shadowsocks", Customers: true, Stream: true, Securities: noReality, Transports: xrayTransports},
			{Protocol: "hysteria", Customers: true, Stream: true, Securities: []string{"tls"}},
			{Protocol: "wireguard"},
			{Protocol: "mixed"}, {Protocol: "socks"}, {Protocol: "http"},
			{Protocol: "tunnel"}, {Protocol: "dokodemo-door"},
		}
	}
	return nil
}

// InboundKindFor is one kind's entry for a node, and whether it has it.
func InboundKindFor(panel model.NodeKind, version, protocol string) (InboundKind, bool) {
	for _, k := range InboundKinds(panel, version) {
		if k.Protocol == protocol {
			return k, true
		}
	}
	return InboundKind{}, false
}

// olderThan reports whether version v is older than major.minor.
func olderThan(v string, major, minor int) bool {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) < 2 {
		return false
	}
	ma, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(strings.SplitN(parts[1], "-", 2)[0])
	if err1 != nil || err2 != nil {
		return false
	}
	return ma < major || ma == major && mi < minor
}

// SupportedPanelVersions is what the node form says of each kind of panel:
// the versions it works with, and how each is reached.
var SupportedPanelVersions = map[model.NodeKind]string{
	model.KindXUI: "3x-ui 2.0 and later: before 3.0 by username and password; 3.0 to 3.2 by API token, " +
		"with the username and password too for the outbound (and for links on 3.0); 3.3 and later by API token",
	model.KindPG: "Pasarguard 1.0 and later: by an administrator's username and password, or from 5.2 by an API key",
}
