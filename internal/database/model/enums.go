package model

// Protocol identifies which VPN backend serves an interface and the accounts
// bound to it. Everything above the backend layer is protocol-agnostic, so this
// is the only place the distinction is spelled out.
type Protocol string

const (
	ProtocolWireGuard Protocol = "wireguard"
	ProtocolOpenVPN   Protocol = "openvpn"
	// ProtocolL2TP is L2TP over IPsec: the VPN Windows, macOS and iOS
	// connect to with nothing installed, a shared key for the tunnel and a
	// username and password per device.
	ProtocolL2TP Protocol = "l2tp"
	// ProtocolPPTP is PPTP: the oldest VPN there is, for the routers and
	// modems that have nothing else. Its encryption has been broken for
	// years, and the panel says so wherever it offers it.
	ProtocolPPTP Protocol = "pptp"
	// ProtocolIKEv2 is IKEv2/IPsec: what Android 12 and newer, iOS, macOS and
	// Windows connect to with nothing installed, now that Android has dropped
	// L2TP and PPTP. A device signs in by username and password (EAP-
	// MSCHAPv2), by a key of its own (PSK), or by a certificate (RSA or
	// EAP-TLS), whichever the tunnel allows.
	ProtocolIKEv2 Protocol = "ikev2"
	// ProtocolOpenConnect is OpenConnect, what Cisco's AnyConnect client --
	// now Cisco Secure Client -- speaks: a VPN inside TLS on TCP, its traffic
	// over DTLS on UDP once signed in, served by ocserv. A username and
	// password per device; the Cisco app is in every store.
	ProtocolOpenConnect Protocol = "openconnect"
	// ProtocolXray is an inbound of a 3x-ui panel added as a node: VLESS,
	// VMess, Trojan, Shadowsocks or Hysteria, served by that panel's own
	// xray. It is never made here: the inbound is read from the node, and
	// what the panel adds is its customers, as that panel's clients.
	ProtocolXray Protocol = "xray"
)

// Valid reports whether p is a protocol the panel knows how to serve.
func (p Protocol) Valid() bool {
	switch p {
	case ProtocolWireGuard, ProtocolOpenVPN, ProtocolL2TP, ProtocolPPTP, ProtocolIKEv2, ProtocolOpenConnect:
		return true
	}
	return false
}

// UsesCredentials reports whether a device on this protocol signs in with a
// username and password, held on its account, rather than carrying a key.
func (p Protocol) UsesCredentials() bool {
	return p == ProtocolOpenVPN || p == ProtocolL2TP || p == ProtocolPPTP || p == ProtocolIKEv2 || p == ProtocolOpenConnect
}

// IsPPP reports whether a protocol's sessions are pppd's, run by the root
// agent: L2TP and PPTP.
func (p Protocol) IsPPP() bool { return p == ProtocolL2TP || p == ProtocolPPTP }

// ByAgent reports whether a protocol's tunnel is carried by the root agent
// rather than by the panel itself: L2TP and PPTP (pppd), IKEv2 (strongSwan)
// and OpenConnect (ocserv). A server has one of each.
func (p Protocol) ByAgent() bool {
	return p.IsPPP() || p == ProtocolIKEv2 || p == ProtocolOpenConnect
}

func (p Protocol) String() string { return string(p) }

// InterfaceMode selects the obfuscation profile of a WireGuard interface.
// Standard is plain WireGuard; Amnezia adds the AmneziaWG junk/padding layer.
type InterfaceMode string

const (
	ModeStandard InterfaceMode = "standard"
	ModeAmnezia  InterfaceMode = "amnezia"
)

// ClientStatus is the lifecycle state of a sellable client.
// Only Active clients are pushed to the kernel.
type ClientStatus string

const (
	StatusActive    ClientStatus = "active"
	StatusDisabled  ClientStatus = "disabled"  // switched off by the admin
	StatusExpired   ClientStatus = "expired"   // past ExpiresAt
	StatusExhausted ClientStatus = "exhausted" // hit QuotaBytes
)

// Serviceable reports whether a client in this state should have its
// accounts present in the kernel.
func (s ClientStatus) Serviceable() bool { return s == StatusActive }

// ResetCycle controls automatic quota renewal.
type ResetCycle string

const (
	ResetNone    ResetCycle = "none"
	ResetDaily   ResetCycle = "daily"
	ResetWeekly  ResetCycle = "weekly"
	ResetMonthly ResetCycle = "monthly"
)

// Granularity tags a traffic sample's bucket width. Samples are rolled up as
// they age: fine buckets are kept briefly, coarse ones for a year.
type Granularity string

const (
	GranularityFine   Granularity = "5m"
	GranularityHourly Granularity = "1h"
	GranularityDaily  Granularity = "1d"
)

// NodeKind distinguishes the panel's own host from remote nodes. Only KindLocal
// is served today; the column exists so multi-node support is an addition
// rather than a migration.
type NodeKind string

const (
	KindLocal  NodeKind = "local"
	KindRemote NodeKind = "remote"
	// KindXUI is a 3x-ui panel used as a node: its inbounds are this panel's
	// tunnels there, and this panel's customers are made its clients.
	KindXUI NodeKind = "xui"
	// KindPG is a Pasarguard panel used as a node, the same way.
	KindPG NodeKind = "pasarguard"
)

// NodeTLSMode is how this panel checks a node's certificate.
//
// The token in every request to a node is a bearer credential for a whole
// panel, so who is on the other end is not a detail.
type NodeTLSMode string

const (
	// TLSVerify is ordinary certificate verification, and is right whenever the
	// node has a real certificate.
	TLSVerify NodeTLSMode = "verify"

	// TLSPin accepts exactly one public key and nothing else. Stronger than
	// verification rather than weaker: a certificate authority mis-issuing for
	// that address does not help, because the key would still be wrong. This is
	// the answer for a node reached by bare address with a certificate it
	// signed itself.
	TLSPin NodeTLSMode = "pin"

	// TLSSkip checks nothing. Offered because refusing to offer it is how
	// operators end up disabling verification somewhere worse, and reported
	// loudly wherever it is in use.
	TLSSkip NodeTLSMode = "skip"

	// TLSMutual verifies the node's certificate normally and presents this
	// panel's own, so the node can refuse a caller that merely knows the token.
	// A token travels in every request and can be read out of a log or a
	// backup; a client key never leaves the panel holding it.
	TLSMutual NodeTLSMode = "mtls"
)

// Safe reports whether a mode actually establishes who is on the other end.
func (m NodeTLSMode) Safe() bool { return m != TLSSkip }

// AdminRole is how much of the panel an operator may reach.
//
// The panel is sold on as well as used: an operator with more capacity than
// customers resells it, and the people they resell to need somewhere to
// manage their own customers without being handed the machine. Three levels
// cover that without inventing a permission system nobody can reason about.
type AdminRole string

const (
	// RoleOwner is the person the panel belongs to. Exactly one exists, it
	// cannot be deleted or switched off, and everything is theirs to see.
	RoleOwner AdminRole = "owner"

	// RoleAdmin sits between the owner and the resellers: the owner's own
	// tier of operator, with a ceiling like a reseller's and resellers of
	// their own beneath them.
	//
	// An admin creates and manages their own resellers and their own
	// customers, and by default sees only that tree -- their customers and
	// their resellers' -- never another admin's. Two switches the owner sets
	// widen this: one to see every customer, one to sell every tunnel. The
	// machine stays the owner's: no nodes, no routing, no engine, no
	// settings, and no say over who administers the panel, including no sight
	// of the admins page itself.
	RoleAdmin AdminRole = "admin"

	// RoleReseller sells the panel's capacity as their own. They see their
	// own customers and their own groups and nothing else, on the tunnels
	// the owner allowed them, within a ceiling the owner set.
	RoleReseller AdminRole = "reseller"
)

// Valid reports whether r is a role this panel knows.
func (r AdminRole) Valid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleReseller:
		return true
	}
	return false
}

// The pages an admin can be granted or kept from, by the owner, with a
// checkbox each. An unchecked page is hidden from the admin and refused to
// them in the backend.
//
// Only these are grantable. The machine -- settings, routing, the engine,
// nodes, tunnels -- and the admins page itself are never here and never an
// admin's, whatever is stored: those stay the owner's, enforced by the
// owner-only middleware, so no stored row can hand one over.
const (
	MenuClients   = "clients"   // the admin's customers
	MenuGroups    = "groups"    // their customer groups
	MenuResellers = "resellers" // the resellers they created
)

// GrantableMenus is every page the owner may grant an admin, in menu order.
func GrantableMenus() []string { return []string{MenuClients, MenuGroups, MenuResellers} }

// ValidMenu reports whether m is a page that can be granted.
func ValidMenu(m string) bool {
	switch m {
	case MenuClients, MenuGroups, MenuResellers:
		return true
	}
	return false
}

func (r AdminRole) String() string { return string(r) }

// ManagesPanel reports whether the role reaches the machine itself --
// interfaces, nodes, outbounds, routing, the engine, settings and backups.
func (r AdminRole) ManagesPanel() bool { return r == RoleOwner }

// ManagesAdmins reports whether the role may add, change or remove admins.
// Only the owner does: an admin who could mint admins is an owner with extra
// steps, so the admins page stays the owner's alone.
func (r AdminRole) ManagesAdmins() bool { return r == RoleOwner }

// ManagesResellers reports whether the role may add, change or remove
// resellers. The owner and an admin do -- an admin only the resellers they
// created, enforced where the action is carried out -- and a reseller never
// does: a reseller who could make resellers would be an admin.
func (r AdminRole) ManagesResellers() bool { return r == RoleOwner || r == RoleAdmin }

// SeesEveryone reports whether the role, on its own, reads every customer and
// every group rather than only their own tree. Only the owner does; an
// admin's reach across the whole panel is a switch the owner sets on the
// admin (CanViewAllClients), not a property of the role.
func (r AdminRole) SeesEveryone() bool { return r == RoleOwner }

// Capped reports whether the role is held to a ceiling -- a customer count, a
// transfer allowance, an expiry, and the tunnels they may sell. A reseller
// and an admin are; the owner is not, a ceiling they set on themselves being
// no ceiling at all.
func (r AdminRole) Capped() bool { return r == RoleReseller || r == RoleAdmin }
