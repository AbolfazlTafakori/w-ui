package model

import "time"

// Node is a machine that terminates tunnels. The panel serves a single local
// node today; the table and the NodeID foreign keys exist so a second node is
// an insert rather than a schema migration.
type Node struct {
	ID      uint     `gorm:"primaryKey" json:"id"`
	Name    string   `gorm:"size:64;uniqueIndex;not null" json:"name"`
	Kind    NodeKind `gorm:"size:16;not null;default:local" json:"kind"`
	Address string   `gorm:"size:255" json:"address"`
	Enabled bool     `gorm:"not null;default:true" json:"enabled"`
	Note    string   `gorm:"size:256" json:"note"`

	// Token authenticates this panel to the remote one. A node is another W-UI
	// panel rather than a purpose-built agent, so the thing being spoken to is
	// the same API served here — which means one implementation to secure and
	// one to keep working, instead of a second protocol nobody exercises.
	//
	// Never returned by the API: it is a bearer credential for a whole panel.
	Token string `gorm:"size:128" json:"-"`

	// What the last probe found. Held on the row rather than in memory so the
	// page has something to show immediately after a restart instead of an
	// empty table that fills in a few seconds later.
	LastSeenAt *time.Time `json:"lastSeenAt"`
	LatencyMS  int        `gorm:"not null;default:0" json:"latencyMs"`
	Version    string     `gorm:"size:32" json:"version"`
	Reachable  bool       `gorm:"not null;default:false" json:"reachable"`
	LastError  string     `gorm:"size:256" json:"lastError"`

	// TLSMode is how this panel checks the node's certificate, and TLSPin is
	// the public key it will accept when the mode is "pin".
	//
	// Verification is the default and is right whenever the node has a real
	// certificate. Pinning exists for the common case it cannot help with — a
	// node reached by bare address with a certificate it signed itself — and is
	// stronger than verification there, not weaker: a certificate authority
	// mis-issuing for that address does not help, because the key would still
	// be wrong.
	TLSMode NodeTLSMode `gorm:"size:16;not null;default:verify" json:"tlsMode"`
	TLSPin  string      `gorm:"size:128" json:"tlsPin"`

	// UsageCoefficient multiplies what this node reports before it is charged
	// against a customer's allowance.
	//
	// Servers do not cost the same. A gigabyte through an expensive one can be
	// billed as two, or a cheap one discounted below one, without needing a
	// second plan or a second panel. Marzban calls it the same thing and it is
	// the closest a reseller gets to per-server pricing.
	//
	// Applied on the way in rather than stored per node, so a customer's total
	// stays one number and the page that shows it needs no arithmetic.
	UsageCoefficient float64 `gorm:"not null;default:1" json:"usageCoefficient"`

	// DataLimitBytes is the server's own transfer allowance, not a customer's.
	//
	// Hosts sell a machine with a monthly cap and charge for what runs over it,
	// or cut the port to a crawl. An operator who cannot see that coming finds
	// out from an invoice. Zero means no cap, which is what a machine on an
	// unmetered line has.
	//
	// Counted in what the node actually carried, before UsageCoefficient: the
	// coefficient is a price, and the host bills bytes.
	DataLimitBytes uint64 `gorm:"not null;default:0" json:"dataLimitBytes"`
	UsedBytes      uint64 `gorm:"not null;default:0" json:"usedBytes"`

	// ResetDay is the day of the month the host's allowance starts again, 1-28,
	// or zero for a counter that only an operator clears.
	//
	// Capped at 28 because a limit that skips February is a limit that fails in
	// the one month an operator is not watching for it.
	ResetDay     int        `gorm:"not null;default:0" json:"resetDay"`
	UsageResetAt *time.Time `json:"usageResetAt"`

	// OverAllowance is what the page and the sync loop both read, so the rule
	// lives here rather than being written out twice with a chance of drifting.
	//
	// Not stored: it is two columns compared, and a stored copy would be a
	// third thing to keep in step.
	OverAllowance bool `gorm:"-" json:"overAllowance"`

	CPUPercent  float64 `gorm:"not null;default:0" json:"cpuPercent"`
	MemPercent  float64 `gorm:"not null;default:0" json:"memPercent"`
	UptimeSec   int64   `gorm:"not null;default:0" json:"uptimeSec"`
	Clients     int64   `gorm:"not null;default:0" json:"clients"`
	Interfaces  int64   `gorm:"not null;default:0" json:"interfaces"`
	Enforcement string  `gorm:"size:32" json:"enforcement"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// APIToken is a long-lived credential for machine access.
//
// A session token expires and is bound to a person signing in; a node talking to
// another node has neither. Stored as a hash for the same reason a password is:
// a leaked database should not hand over live access to every panel this one
// federates with.
type APIToken struct {
	ID     uint   `gorm:"primaryKey" json:"id"`
	Name   string `gorm:"size:64;not null" json:"name"`
	Hash   string `gorm:"size:128;uniqueIndex;not null" json:"-"`
	Prefix string `gorm:"size:12;not null" json:"prefix"`

	LastUsedAt *time.Time `json:"lastUsedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// Interface is one listening VPN endpoint on a node.
//
// Running several interfaces on one node is deliberate: a standard WireGuard
// interface and an AmneziaWG one on different ports give a customer a fallback
// when a port is filtered, without moving them to another server.
type Interface struct {
	ID       uint     `gorm:"primaryKey" json:"id"`
	NodeID   uint     `gorm:"index;not null;uniqueIndex:idx_node_ifname" json:"nodeId"`
	Name     string   `gorm:"size:32;not null;uniqueIndex:idx_node_ifname" json:"name"`
	Protocol Protocol `gorm:"size:16;not null;index" json:"protocol"`
	Enabled  bool     `gorm:"not null;default:true" json:"enabled"`

	ListenPort   int    `gorm:"not null" json:"listenPort"`
	Subnet       string `gorm:"size:64;not null" json:"subnet"`        // e.g. 10.66.0.0/16
	EndpointHost string `gorm:"size:255;not null" json:"endpointHost"` // what clients dial
	MTU          int    `gorm:"not null;default:1420" json:"mtu"`
	DNS          string `gorm:"size:128" json:"dns"`
	NATInterface string `gorm:"size:32" json:"natInterface"` // egress iface for masquerade

	// WireGuard identity. Unused for OpenVPN interfaces.
	PrivateKey string `gorm:"size:64" json:"-"`
	PublicKey  string `gorm:"size:64" json:"publicKey"`

	Mode InterfaceMode        `gorm:"size:16;not null;default:standard" json:"mode"`
	AWG  JSONField[AWGParams] `gorm:"type:text" json:"awg"`

	OpenVPN JSONField[OpenVPNParams] `gorm:"type:text" json:"openvpn"`

	// Configured is set on the way out of the API, not stored. It lets a page
	// say that certificates exist without being handed them.
	Configured bool `gorm:"-" json:"configured"`

	// OriginID is this tunnel's id on the panel that owns it. Only set when
	// Managed, and it is what a sync matches on: a tunnel renamed centrally
	// must move this node's interface rather than leaving the old one behind.
	OriginID uint `gorm:"index;not null;default:0" json:"originId,omitempty"`

	// Managed marks an interface this panel did not create.
	//
	// A panel acting as a node is handed its interfaces and their peers by the
	// panel that holds the records. It programs them exactly as it would its
	// own, and refuses to let anybody edit them here: the central panel would
	// overwrite the change on its next sync, so allowing it would only produce
	// a setting that silently reverts.
	//
	// It also never enforces an allowance on them. The customer's quota is one
	// number on the central panel, spent across every node they reach, and a
	// node deciding for itself would cut them off at its own share.
	Managed bool `gorm:"not null;default:false" json:"managed"`

	// Hosts is filled in on the way to a profile renderer and is neither
	// stored nor serialised.
	//
	// A renderer has no database, and the addresses a customer should dial are
	// not a property of the interface: they are the spares an operator keeps
	// for when the first one is blocked. Attaching them here is what lets one
	// profile carry all of them.
	Hosts []Host `gorm:"-" json:"-"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// IsWireGuard reports whether this interface is served by the WireGuard backend.
func (i *Interface) IsWireGuard() bool { return i.Protocol == ProtocolWireGuard }

// Client is the unit that gets sold. Quota, expiry and the device limit
// live here and apply to the sum of the client's accounts, so a customer
// who buys "50 GB across 3 devices" is one row here and three in accounts.
type Client struct {
	ID       uint     `gorm:"primaryKey" json:"id"`
	Name     string   `gorm:"size:128;not null;index" json:"name"`
	Note     string   `gorm:"size:512" json:"note"`
	Protocol Protocol `gorm:"size:16;not null;index" json:"protocol"`

	// OriginID is this customer's id on the panel that owns them.
	//
	// Only set on a node. A node is handed the peers it must serve and nothing
	// about the plan behind them: no allowance, no expiry, not even the
	// customer's name — the row here is called after the id so that a node,
	// which may be rented from somebody else, never learns who the customers
	// are. Usage is reported back against this id.
	OriginID uint `gorm:"index;not null;default:0" json:"originId,omitempty"`

	// Group is a free-text label, not a foreign key. A group has no identity of
	// its own: the groups page is a GROUP BY over this column, so creating one
	// is typing a name and deleting one is clearing it from its members.
	Group string `gorm:"size:64;index" json:"group"`

	// QuotaBytes of 0 means unlimited.
	QuotaBytes uint64 `gorm:"not null;default:0" json:"quotaBytes"`
	UsedBytes  uint64 `gorm:"not null;default:0" json:"usedBytes"`
	// UpBytes and DownBytes split the total by direction, from the customer's
	// point of view. The quota is enforced on their sum, not on either one.
	//
	// They stay zero on a server whose enforcer cannot tell the directions
	// apart — the in-memory stand-in used when nftables is unavailable — in
	// which case only UsedBytes moves rather than the split being guessed at.
	UpBytes   uint64     `gorm:"not null;default:0" json:"upBytes"`
	DownBytes uint64     `gorm:"not null;default:0" json:"downBytes"`
	ExpiresAt *time.Time `gorm:"index" json:"expiresAt"`

	// A plan that starts when the customer first connects rather than when it
	// was sold. A reseller who hands out ten configs on Monday should not have
	// them all expiring on the same Wednesday whether or not anyone used them.
	//
	// StartOnFirstUse with DurationDays set and ExpiresAt nil means the clock
	// has not started. ActivatedAt records when it did.
	StartOnFirstUse bool       `gorm:"not null;default:false" json:"startOnFirstUse"`
	DurationDays    int        `gorm:"not null;default:0" json:"durationDays"`
	ActivatedAt     *time.Time `json:"activatedAt"`

	DeviceLimit int `gorm:"not null;default:1" json:"deviceLimit"`

	// RateBitsPerSec of 0 means unmetered. Applied via tc by the enforcer.
	RateBitsPerSec uint64 `gorm:"not null;default:0" json:"rateBitsPerSec"`

	ResetCycle  ResetCycle `gorm:"size:16;not null;default:none" json:"resetCycle"`
	LastResetAt *time.Time `json:"lastResetAt"`

	Status ClientStatus `gorm:"size:16;not null;index;default:active" json:"status"`

	// SubToken is the secret in a customer's subscription link. Whoever holds it
	// gets the configuration, so it is never returned by the list endpoints —
	// only by the one call that exists to show it.
	SubToken string `gorm:"size:64;index" json:"-"`

	Accounts []Account `gorm:"foreignKey:ClientID;constraint:OnDelete:CASCADE" json:"accounts,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// QuotaExceeded reports whether usage has reached the quota. An unlimited
// client never exceeds.
func (s *Client) QuotaExceeded() bool {
	return s.QuotaBytes > 0 && s.UsedBytes >= s.QuotaBytes
}

// Expired reports whether the client is past its expiry at the given
// instant. A client with no expiry never expires.
func (s *Client) Expired(now time.Time) bool {
	return s.ExpiresAt != nil && !s.ExpiresAt.After(now)
}

// RemainingBytes returns how much quota is left, or 0 for unlimited.
func (s *Client) RemainingBytes() uint64 {
	if s.QuotaBytes == 0 || s.UsedBytes >= s.QuotaBytes {
		return 0
	}
	return s.QuotaBytes - s.UsedBytes
}

// Account is one device. In WireGuard it maps to a peer and carries a key pair;
// in OpenVPN it maps to a credential.
//
// One device per account is not a convention but a constraint: two devices
// sharing a WireGuard key make the server rewrite the peer's endpoint on every
// handshake, and both connections thrash.
type Account struct {
	ID          uint `gorm:"primaryKey" json:"id"`
	ClientID    uint `gorm:"index;not null" json:"clientId"`
	InterfaceID uint `gorm:"index;not null;uniqueIndex:idx_iface_ip" json:"interfaceId"`
	NodeID      uint `gorm:"index;not null;default:1" json:"nodeId"`

	DeviceName string `gorm:"size:64;not null" json:"deviceName"`
	IP         string `gorm:"size:45;not null;uniqueIndex:idx_iface_ip" json:"ip"`

	// WireGuard credentials.
	PrivateKey   string `gorm:"size:64" json:"-"`
	PublicKey    string `gorm:"size:64;index" json:"publicKey,omitempty"`
	PresharedKey string `gorm:"size:64" json:"-"`

	// OpenVPN credentials.
	//
	// Secret is stored recoverably, not hashed. A reseller has to be able to
	// re-read a customer their password months later, which a one-way hash
	// makes impossible; the operator's own login uses bcrypt, where re-display
	// is never needed. The consequence is that the database file holds live
	// credentials and must be protected accordingly.
	Username string `gorm:"size:64;index" json:"username,omitempty"`
	Secret   string `gorm:"size:128" json:"-"`

	Enabled bool `gorm:"not null;default:true" json:"enabled"`

	// OriginID is this account's id on the panel that owns it.
	//
	// Only set on a node, where it is what usage is reported against: the
	// central panel asked for these peers and needs the answer keyed by names
	// it already has, not by ids this node happened to assign.
	OriginID uint `gorm:"index;not null;default:0" json:"originId,omitempty"`

	// Runtime state, written by the collector. LastEndpoint is what the
	// key-sharing detector watches: a peer whose public endpoint hops between
	// several addresses in a short window is being used on more than one device.
	LastHandshake *time.Time `json:"lastHandshake"`
	LastEndpoint  string     `gorm:"size:64" json:"lastEndpoint"`
	LastSeenAt    *time.Time `json:"lastSeenAt"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TrafficSample is one bucket of the usage time series. Buckets are rolled up
// as they age so the table does not grow without bound.
type TrafficSample struct {
	ID          uint        `gorm:"primaryKey" json:"id"`
	ClientID    uint        `gorm:"not null;index:idx_sub_bucket,priority:1" json:"clientId"`
	BucketTS    time.Time   `gorm:"not null;index:idx_sub_bucket,priority:2" json:"bucketTs"`
	Granularity Granularity `gorm:"size:8;not null;index" json:"granularity"`
	RX          uint64      `gorm:"not null;default:0" json:"rx"`
	TX          uint64      `gorm:"not null;default:0" json:"tx"`
}

// IPLease records which account held a tunnel address over a time window.
//
// This is not analytics. When the host forwards an abuse complaint naming a
// timestamp, this table is the only way to answer which customer was behind the
// address; without it the complaint cannot be answered at all.
type IPLease struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	AccountID      uint       `gorm:"index" json:"accountId"`
	ClientID       uint       `gorm:"index" json:"clientId"`
	IP             string     `gorm:"size:45;not null;index" json:"ip"`
	PublicEndpoint string     `gorm:"size:64" json:"publicEndpoint"`
	FromTS         time.Time  `gorm:"not null;index" json:"fromTs"`
	ToTS           *time.Time `json:"toTs"`
}

// Admin is a panel operator. Customers never get an account here; configs are
// handed to them out of band.
type Admin struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	Username     string     `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash string     `gorm:"size:255;not null" json:"-"`
	TOTPSecret   string     `gorm:"size:64" json:"-"`
	Locale       string     `gorm:"size:8;not null;default:en" json:"locale"`
	LastLoginAt  *time.Time `json:"lastLoginAt"`
	LastLoginIP  string     `gorm:"size:45" json:"lastLoginIp"`

	// SessionEpoch is what makes a session endable.
	//
	// A signed token is valid until it expires, and nothing an operator can do
	// changes that — so discovering a break-in, changing the password and
	// finding the intruder still working for the rest of the day was the state
	// of things. Every token carries the epoch it was issued under; raising it
	// makes every one of them stop at once.
	SessionEpoch int       `gorm:"not null;default:1" json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Setting is a key/value row for panel configuration that an operator can
// change at runtime, as opposed to boot-time environment configuration.
type Setting struct {
	Key       string    `gorm:"size:64;primaryKey" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Group is a named bucket of customers.
//
// The membership itself still lives on the client, as a name, so a rename is one
// statement and nothing can point at a group that is gone. What this row adds is
// existence: a group can be created before it has anybody in it, and survives its
// last member leaving. Without it "create a group" is not an action an operator
// can take — they can only type a name onto a customer and hope.
type Group struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `gorm:"size:64;uniqueIndex;not null" json:"name"`
	Note string `gorm:"size:256" json:"note"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// AccountEndpoint is one public address an account has connected from.
//
// A credential is sold to one person. Two people using it at once show up as
// two public addresses active in the same short window — which is the only
// signal available, because the tunnel itself cannot tell two devices apart once
// they hold the same key. Keeping the addresses rather than a bare count is what
// lets an operator judge a real case from a phone moving between wifi and
// mobile data.
type AccountEndpoint struct {
	AccountID uint   `gorm:"primaryKey;autoIncrement:false" json:"accountId"`
	Addr      string `gorm:"size:64;primaryKey" json:"addr"`

	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `gorm:"index" json:"lastSeen"`
	Hits      uint64    `gorm:"not null;default:1" json:"hits"`
}

// AllModels is the migration set, in dependency order.
func AllModels() []any {
	return []any{
		&Node{},
		&Interface{},
		&Client{},
		&Account{},
		&TrafficSample{},
		&IPLease{},
		&Admin{},
		&APIToken{},
		&Group{},
		&AccountEndpoint{},
		&Outbound{},
		&RoutingRule{},
		&Host{},
		&Setting{},
	}
}
