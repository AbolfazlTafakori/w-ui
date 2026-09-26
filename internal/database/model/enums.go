package model

// Protocol identifies which VPN backend serves an interface and the accounts
// bound to it. Everything above the backend layer is protocol-agnostic, so this
// is the only place the distinction is spelled out.
type Protocol string

const (
	ProtocolWireGuard Protocol = "wireguard"
	ProtocolOpenVPN   Protocol = "openvpn"
)

// Valid reports whether p is a protocol the panel knows how to serve.
func (p Protocol) Valid() bool {
	switch p {
	case ProtocolWireGuard, ProtocolOpenVPN:
		return true
	}
	return false
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

	// RoleAdmin is a second pair of hands over every customer, without the
	// machine underneath: no interfaces, no nodes, no routing, no settings,
	// and no say over who administers the panel.
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

func (r AdminRole) String() string { return string(r) }

// ManagesPanel reports whether the role reaches the machine itself --
// interfaces, nodes, outbounds, routing, the engine, settings and backups.
func (r AdminRole) ManagesPanel() bool { return r == RoleOwner }

// ManagesAdmins reports whether the role may add, change or remove operators.
// Only the owner does: an administrator who could mint administrators is an
// owner with extra steps.
func (r AdminRole) ManagesAdmins() bool { return r == RoleOwner }

// SeesEveryone reports whether the role reads every customer and every group
// rather than only their own.
func (r AdminRole) SeesEveryone() bool { return r == RoleOwner || r == RoleAdmin }

// Capped reports whether the role is held to a ceiling -- a customer count, a
// transfer allowance and an expiry. Only a reseller is: a ceiling on the
// owner would be a ceiling they set on themselves, and an administrator sells
// nothing of their own.
func (r AdminRole) Capped() bool { return r == RoleReseller }
