// Package handover is what passes between two W-UI panels when one hands its
// own tunnels and customers over to the other, which manages it as a node.
//
// A W-UI panel that becomes a node usually has customers of its own. The
// managing panel brings them in as its own customers, as it does a 3x-ui or
// Pasarguard panel's users -- but here nothing has to be switched on and off
// over another panel's API, because the two panels speak the same sync. The
// node's own tunnels and customers are handed over whole: the managing panel
// makes the same records with the same keys, ports and addresses, and the
// node marks its rows as managed under the managing panel's ids. The next
// sync then finds every row where it already is, and nothing a customer holds
// changes: not a key, an address, a port or a password.
//
// A tunnel goes with every customer on it. A customer the managing panel does
// not send is removed from a managed tunnel on the next sync, so a tunnel
// handed over with only some of its customers would cut the rest off.
//
// The node reads and claims (fleet); the managing panel makes the records
// (service). Both speak these types, which sit below them.
package handover

import (
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Handover is what a node has of its own, in full: what the managing panel
// needs to make the same records, keys and passwords included.
type Handover struct {
	Tunnels []Tunnel `json:"tunnels"`
	Clients []Client `json:"clients"`
}

// Tunnel is one of the node's own tunnels. Its IKEv2 authority's key comes
// with it: the managing panel is the one that issues from now on.
type Tunnel struct {
	LocalID      uint                     `json:"localId"`
	Name         string                   `json:"name"`
	Protocol     model.Protocol           `json:"protocol"`
	Enabled      bool                     `json:"enabled"`
	ListenPort   int                      `json:"listenPort"`
	Subnet       string                   `json:"subnet"`
	EndpointHost string                   `json:"endpointHost"`
	MTU          int                      `json:"mtu"`
	DNS          string                   `json:"dns"`
	NATInterface string                   `json:"natInterface"`
	Mode         model.InterfaceMode      `json:"mode"`
	PrivateKey   string                   `json:"privateKey,omitempty"`
	PublicKey    string                   `json:"publicKey,omitempty"`
	AWG          *model.AWGParams         `json:"awg,omitempty"`
	OpenVPN      *model.OpenVPNParams     `json:"openvpn,omitempty"`
	L2TP         *model.L2TPParams        `json:"l2tp,omitempty"`
	IKEv2        *model.IKEv2Params       `json:"ikev2,omitempty"`
	OpenConnect  *model.OpenConnectParams `json:"openconnect,omitempty"`
}

// Client is one of the node's own customers, with their plan.
type Client struct {
	LocalID         uint               `json:"localId"`
	Name            string             `json:"name"`
	Note            string             `json:"note,omitempty"`
	Groups          []string           `json:"groups,omitempty"`
	TelegramID      int64              `json:"telegramId,omitempty"`
	BaleID          int64              `json:"baleId,omitempty"`
	QuotaBytes      uint64             `json:"quotaBytes"`
	UsedBytes       uint64             `json:"usedBytes"`
	UpBytes         uint64             `json:"upBytes"`
	DownBytes       uint64             `json:"downBytes"`
	ExpiresAt       *time.Time         `json:"expiresAt,omitempty"`
	StartOnFirstUse bool               `json:"startOnFirstUse,omitempty"`
	DurationDays    int                `json:"durationDays,omitempty"`
	DurationHours   int                `json:"durationHours,omitempty"`
	ActivatedAt     *time.Time         `json:"activatedAt,omitempty"`
	DeviceLimit     int                `json:"deviceLimit"`
	RateBitsPerSec  uint64             `json:"rateBitsPerSec,omitempty"`
	ResetCycle      model.ResetCycle   `json:"resetCycle,omitempty"`
	LastResetAt     *time.Time         `json:"lastResetAt,omitempty"`
	Status          model.ClientStatus `json:"status"`
	SubToken        string             `json:"subToken,omitempty"`
	Accounts        []Account          `json:"accounts"`
	// Elsewhere says they also have a file on a tunnel that is not handed
	// over -- an Xray inbound, or one another panel manages -- which would
	// be cut off; they stay where they are.
	Elsewhere bool `json:"elsewhere,omitempty"`
}

// Account is one of a customer's files, on one of the node's tunnels.
type Account struct {
	LocalID       uint   `json:"localId"`
	TunnelLocalID uint   `json:"tunnelLocalId"`
	DeviceName    string `json:"deviceName"`
	IP            string `json:"ip"`
	Enabled       bool   `json:"enabled"`
	PrivateKey    string `json:"privateKey,omitempty"`
	PublicKey     string `json:"publicKey,omitempty"`
	PresharedKey  string `json:"presharedKey,omitempty"`
	Username      string `json:"username,omitempty"`
	Secret        string `json:"secret,omitempty"`
	CertPEM       string `json:"certPem,omitempty"`
	CertKeyPEM    string `json:"certKeyPem,omitempty"`
	UpBytes       uint64 `json:"upBytes"`
	DownBytes     uint64 `json:"downBytes"`
}

// Claim is the managing panel's answer: which of the node's rows are now its,
// under which of its ids.
type Claim struct {
	Tunnels  []IDPair `json:"tunnels"`
	Clients  []IDPair `json:"clients"`
	Accounts []IDPair `json:"accounts"`
}

// IDPair is a row on the node and its id on the managing panel, with the
// counters the managing panel took over with it.
//
// A managed row's counters are what the node's next drain reports and zeroes,
// so what was handed over has to leave them: reported again, it would be
// charged twice. It is taken off rather than the counters zeroed, so what
// was spent between the handover's read and its claim is still reported.
type IDPair struct {
	LocalID  uint   `json:"localId"`
	OriginID uint   `json:"originId"`
	Used     uint64 `json:"used,omitempty"`
	Up       uint64 `json:"up,omitempty"`
	Down     uint64 `json:"down,omitempty"`
}
