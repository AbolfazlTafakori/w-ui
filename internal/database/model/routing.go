package model

import "time"

// OutboundKind is how an outbound reaches the internet.
type OutboundKind string

const (
	// OutboundDirect leaves through the server's own address, the way traffic
	// goes when nothing else is configured.
	OutboundDirect OutboundKind = "direct"
	// OutboundBlock discards the traffic. It exists so a routing rule has
	// somewhere to send what should not go anywhere.
	OutboundBlock OutboundKind = "block"
	// OutboundWireGuard forwards through an upstream WireGuard peer, giving the
	// customer a second hop and an exit address in another place.
	OutboundWireGuard OutboundKind = "wireguard"
	// OutboundOpenVPN forwards through an upstream OpenVPN server, from a
	// client profile the operator pastes in. An openvpn process owns the
	// device.
	OutboundOpenVPN OutboundKind = "openvpn"
	// The Xray family. Each is an Xray outbound object stored verbatim, the
	// way 3x-ui keeps one, run by an xray process behind a tun device.
	OutboundSOCKS       OutboundKind = "socks"
	OutboundHTTP        OutboundKind = "http"
	OutboundVLESS       OutboundKind = "vless"
	OutboundVMess       OutboundKind = "vmess"
	OutboundTrojan      OutboundKind = "trojan"
	OutboundShadowsocks OutboundKind = "shadowsocks"
	OutboundHysteria    OutboundKind = "hysteria"
)

// Valid reports whether k is a kind the panel knows how to route through.
func (k OutboundKind) Valid() bool {
	switch k {
	case OutboundDirect, OutboundBlock, OutboundWireGuard, OutboundOpenVPN,
		OutboundSOCKS, OutboundHTTP, OutboundVLESS, OutboundVMess,
		OutboundTrojan, OutboundShadowsocks, OutboundHysteria:
		return true
	}
	return false
}

func (k OutboundKind) String() string { return string(k) }

// NeedsHop reports whether this kind reaches the internet through something
// else that has to be dialled, configured and health-checked -- everything
// but the two built-ins.
func (k OutboundKind) NeedsHop() bool {
	return k.Valid() && k != OutboundDirect && k != OutboundBlock
}

// IsXray reports whether an xray process carries this kind.
func (k OutboundKind) IsXray() bool {
	switch k {
	case OutboundSOCKS, OutboundHTTP, OutboundVLESS, OutboundVMess,
		OutboundTrojan, OutboundShadowsocks, OutboundHysteria:
		return true
	}
	return false
}

// Outbound is a way out of the server.
//
// Every packet a customer sends leaves through exactly one of these. Two are
// created at install and cannot be deleted — `direct`, which is the server's
// own address, and `blocked`, which is the bin — because a routing rule must
// always have somewhere to point and a panel with no outbounds could not carry
// traffic at all.
//
// The rest are hops: another WireGuard server, or a proxy. That is what lets an
// operator sell an exit in a country where they have no server, or keep one
// customer's traffic off the address every other customer shares.
type Outbound struct {
	ID uint `gorm:"primaryKey" json:"id"`

	// Tag is what routing rules refer to. It is stable and unique, and for the
	// two built-ins it is fixed.
	Tag  string       `gorm:"size:64;uniqueIndex;not null" json:"tag"`
	Kind OutboundKind `gorm:"size:16;not null;index" json:"kind"`

	// Builtin marks the two rows that ship with the panel. They can be edited
	// only in ways that cannot break routing, and never removed.
	Builtin bool `gorm:"not null;default:false" json:"builtin"`
	Enabled bool `gorm:"not null;default:true" json:"enabled"`

	// Position orders the list an operator sees and, for rules that name no
	// outbound, decides which hop is tried first.
	Position int `gorm:"not null;default:0;index" json:"position"`

	// Address is host:port for a proxy, or the endpoint for a WireGuard hop.
	// Empty for the two built-ins, which dial nothing.
	Address string `gorm:"size:255" json:"address"`

	// Credentials for a proxy. Stored recoverably rather than hashed: the panel
	// has to present them to the proxy on every connection, so there is nothing
	// a one-way hash could be checked against.
	Username string `gorm:"size:128" json:"username"`
	Password string `gorm:"size:255" json:"-"`

	// WireGuard hop material. Unused by the other kinds.
	PrivateKey   string `gorm:"size:64" json:"-"`
	PeerPubKey   string `gorm:"size:64" json:"peerPubKey"`
	PresharedKey string `gorm:"size:64" json:"-"`
	// AllowedIPs is what the peer is allowed to send us, and so what we
	// route into it: comma-separated prefixes. Empty means everything.
	AllowedIPs string `gorm:"size:512" json:"allowedIps"`

	// Config is the material a process-backed hop runs from: the OpenVPN
	// client profile, or the Xray outbound object as JSON. Secrets live in
	// it, so it is not listed; the form fetches it on its own.
	Config string `gorm:"type:text" json:"-"`
	// Keepalive is the PersistentKeepalive interval in seconds; 0 means
	// the default of 25, which a hop behind NAT needs.
	Keepalive int `gorm:"not null;default:0" json:"keepalive"`
	// HopAddress is the address this server takes inside the upstream tunnel,
	// as issued by whoever runs it.
	HopAddress string `gorm:"size:64" json:"hopAddress"`
	HopDNS     string `gorm:"size:128" json:"hopDns"`
	HopMTU     int    `gorm:"not null;default:1380" json:"hopMtu"`

	// Mark is the packet mark that steers traffic into this outbound's routing
	// table. Assigned by the panel, never by the operator: a mark that collided
	// with another program's would hijack its traffic.
	//
	// Indexed but not unique. It is derived from the row id, so two outbounds
	// cannot share one; and the outbounds that dial nothing all carry zero,
	// which a unique index would reject on the second of them.
	Mark uint32 `gorm:"not null;default:0;index" json:"mark"`

	Note string `gorm:"size:256" json:"note"`

	// Measured by the latency check, not configured.
	LatencyMS   int        `gorm:"not null;default:0" json:"latencyMs"`
	LastCheckAt *time.Time `json:"lastCheckAt"`
	LastError   string     `gorm:"size:512" json:"lastError"`
	// Where an HTTP probe last came out. Empty until one has been run.
	EgressIP      string `gorm:"size:64" json:"egressIp"`
	EgressCountry string `gorm:"size:8" json:"egressCountry"`

	// Provider names the integration that made this outbound -- warp, nord or
	// pia -- so its dialog can find it again, and ProviderRef is the server
	// within that provider. Both empty for one an operator typed in.
	Provider    string `gorm:"size:16;index" json:"provider"`
	ProviderRef string `gorm:"size:128" json:"providerRef"`
	// SubID is the subscription this outbound was fetched from; 0 when typed
	// in. Everything with the same SubID is replaced together on a refresh.
	SubID uint `gorm:"index;not null;default:0" json:"subId"`

	// Counters for what has left through here.
	TxBytes uint64 `gorm:"not null;default:0" json:"txBytes"`
	RxBytes uint64 `gorm:"not null;default:0" json:"rxBytes"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Removable reports whether an operator may delete this outbound.
func (o *Outbound) Removable() bool { return !o.Builtin }

// OutboundSub is a URL that lists outbounds. It is fetched on its interval and
// the outbounds it produced last time are replaced with what it says now.
type OutboundSub struct {
	ID uint `gorm:"primaryKey" json:"id"`

	Remark    string `gorm:"size:64" json:"remark"`
	URL       string `gorm:"type:text;not null" json:"url"`
	TagPrefix string `gorm:"size:32" json:"tagPrefix"`
	// IntervalMin is how often to fetch, in minutes.
	IntervalMin int  `gorm:"not null;default:10" json:"intervalMin"`
	Enabled     bool `gorm:"not null;default:true" json:"enabled"`
	// AllowPrivate lets the URL point at a private or loopback address, which
	// is refused by default so a panel cannot be made to fetch its own network.
	AllowPrivate bool `gorm:"not null;default:false" json:"allowPrivate"`
	// Prepend lists this subscription's outbounds before the manual ones.
	Prepend  bool `gorm:"not null;default:false" json:"prepend"`
	Position int  `gorm:"not null;default:0;index" json:"position"`

	LastFetchAt *time.Time `json:"lastFetchAt"`
	LastError   string     `gorm:"size:512" json:"lastError"`
	// Count is how many outbounds the last successful fetch produced.
	Count int `gorm:"not null;default:0" json:"count"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// RouteMatchKind is what a routing rule looks at.
type RouteMatchKind string

const (
	MatchDomain   RouteMatchKind = "domain"
	MatchIP       RouteMatchKind = "ip"
	MatchPort     RouteMatchKind = "port"
	MatchProtocol RouteMatchKind = "protocol"
	// MatchClient sends one customer's traffic somewhere of its own, which is
	// how a premium exit is sold to a single person.
	MatchClient RouteMatchKind = "client"
	// MatchGroup does the same for everyone in a group.
	MatchGroup RouteMatchKind = "group"
)

// Valid reports whether k is a match the router understands.
func (k RouteMatchKind) Valid() bool {
	switch k {
	case MatchDomain, MatchIP, MatchPort, MatchProtocol, MatchClient, MatchGroup:
		return true
	}
	return false
}

func (k RouteMatchKind) String() string { return string(k) }

// RoutingRule sends some traffic to an outbound other than the default.
//
// Rules are evaluated in Position order and the first match wins, which is the
// same order they are shown in. That is deliberate: a rule list whose effect
// depends on a sort the operator cannot see is a rule list nobody can debug.
type RoutingRule struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Name     string `gorm:"size:64;not null" json:"name"`
	Enabled  bool   `gorm:"not null;default:true" json:"enabled"`
	Position int    `gorm:"not null;default:0;index" json:"position"`

	Match RouteMatchKind `gorm:"size:16;not null" json:"match"`
	// Value is what to match: a domain suffix, a CIDR, a port or range, a
	// protocol name, or a numeric id for client and group matches.
	Value string `gorm:"size:512;not null" json:"value"`

	// OutboundTag names where matching traffic goes.
	OutboundTag string `gorm:"size:64;not null;index" json:"outboundTag"`

	Note string `gorm:"size:256" json:"note"`

	// Hits counts what the rule has actually caught, so an operator can tell a
	// rule that is doing something from one that has never matched.
	Hits uint64 `gorm:"not null;default:0" json:"hits"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Host is one public address customers can be handed for an interface.
//
// An interface listens once, but the address customers dial is not always the
// same one: a domain behind a CDN, a second domain kept in reserve for when the
// first is blocked, a bare IP for clients that cannot resolve names. Each of
// these is a Host, and a customer's configuration is written with whichever one
// their group points at.
type Host struct {
	ID          uint `gorm:"primaryKey" json:"id"`
	InterfaceID uint `gorm:"index;not null" json:"interfaceId"`

	Name    string `gorm:"size:64;not null" json:"name"`
	Enabled bool   `gorm:"not null;default:true" json:"enabled"`

	// Address is what goes into the customer's configuration file.
	Address string `gorm:"size:255;not null" json:"address"`
	// Port overrides the interface's listen port, for a host reached through a
	// forwarder that answers somewhere else.
	Port int `gorm:"not null;default:0" json:"port"`

	// Priority orders which host a client is handed when several are enabled.
	Priority int    `gorm:"not null;default:0" json:"priority"`
	Note     string `gorm:"size:256" json:"note"`

	// Reachability, filled in by the prober rather than the operator.
	Reachable   bool       `gorm:"not null;default:true" json:"reachable"`
	LastCheckAt *time.Time `json:"lastCheckAt"`
	LastError   string     `gorm:"size:512" json:"lastError"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// EffectivePort is the port a customer should dial for this host.
func (h *Host) EffectivePort(ifacePort int) int {
	if h.Port > 0 {
		return h.Port
	}
	return ifacePort
}
