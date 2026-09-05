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
