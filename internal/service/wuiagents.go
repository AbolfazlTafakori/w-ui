package service

import (
	"encoding/base64"
	"strings"

	"github.com/abolfazl/w-ui/internal/backend/agentkit"
	"github.com/abolfazl/w-ui/internal/backend/ikev2driver"
	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/backend/occdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/listfield"
)

// WUIAgent is one device on a tunnel the root agent serves: IKEv2,
// OpenConnect, L2TP or PPTP. It sits beside the tunnels rather than among
// them, since an app that knows only tunnels refuses one without a
// WireGuard file. Unlike the links and the zip, this profile carries the
// sign-in: it is read by the W-UI app alone, and already carries the
// WireGuard private keys.
type WUIAgent struct {
	Name        string         `json:"name"`
	Device      string         `json:"device"`
	Protocol    string         `json:"protocol"`
	IKEv2       *WUIAgentIKEv2 `json:"ikev2,omitempty"`
	OpenConnect *WUIAgentOC    `json:"openconnect,omitempty"`
	L2TP        *WUIAgentL2TP  `json:"l2tp,omitempty"`
	PPTP        *WUIAgentPPP   `json:"pptp,omitempty"`
}

// WUIAgentIKEv2 is an IKEv2 device's sign-in. In PSK mode the device's key
// is its password.
type WUIAgentIKEv2 struct {
	Servers     []string `json:"servers"`
	Port        int      `json:"port"`
	ServerID    string   `json:"serverId"`
	Methods     []string `json:"methods"`
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	CACert      string   `json:"caCert,omitempty"`
	P12         string   `json:"p12,omitempty"`
	P12Password string   `json:"p12Password,omitempty"`
	TLSIdentity string   `json:"tlsIdentity,omitempty"`
}

// WUIAgentOC is an OpenConnect device's sign-in.
type WUIAgentOC struct {
	URL        string   `json:"url"`
	Others     []string `json:"others,omitempty"`
	Username   string   `json:"username"`
	Password   string   `json:"password"`
	Pin        string   `json:"pin,omitempty"`
	SelfSigned bool     `json:"selfSigned,omitempty"`
}

// WUIAgentL2TP is an L2TP/IPsec device's sign-in.
type WUIAgentL2TP struct {
	Servers    []string `json:"servers"`
	Port       int      `json:"port"`
	PSK        string   `json:"psk"`
	Username   string   `json:"username"`
	Password   string   `json:"password"`
	AllowPlain bool     `json:"allowPlain,omitempty"`
}

// WUIAgentPPP is a PPTP device's sign-in.
type WUIAgentPPP struct {
	Servers  []string `json:"servers"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
}

// forWUI is the interface with the hosts the wui format leaves out taken
// away, so agentkit.Servers lists only what the profile may name.
func forWUI(iface model.Interface) model.Interface {
	hosts := make([]model.Host, 0, len(iface.Hosts))
	for _, h := range iface.Hosts {
		if containsFold(listfield.Split(h.ExcludeFormats), FormatWUI) {
			continue
		}
		hosts = append(hosts, h)
	}
	iface.Hosts = hosts
	return iface
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), s) {
			return true
		}
	}
	return false
}

// wuiAgent is the profile entry for one agent device; false when there is
// nothing a device could dial.
func wuiAgent(acc *model.Account, iface model.Interface) (WUIAgent, bool) {
	at := forWUI(iface)
	a := WUIAgent{Name: iface.Name, Device: acc.DeviceName, Protocol: string(iface.Protocol)}
	switch iface.Protocol {
	case model.ProtocolIKEv2:
		v := ikev2driver.GuideValues(acc, &at)
		p := iface.IKEv2.V
		e := &WUIAgentIKEv2{
			Servers: agentkit.Servers(&at), Port: portOr(iface.ListenPort, 500), ServerID: v.ServerID,
			Methods: []string{}, Username: acc.Username, Password: acc.Secret,
		}
		for _, m := range []struct {
			on   bool
			name string
		}{{p.MSCHAPv2, "mschapv2"}, {p.PSK, "psk"}, {p.RSA, "rsa"}, {p.EAPTLS, "eaptls"}} {
			if m.on {
				e.Methods = append(e.Methods, m.name)
			}
		}
		if p.MSCHAPv2 || p.ByCertificate() {
			e.CACert = p.CACert
		}
		if p.EAPTLS {
			e.TLSIdentity = v.IKEv2.TLSIdentity
		}
		if b, ok, err := ikev2driver.CertBundle(acc, &iface); err == nil && ok {
			e.P12, e.P12Password = base64.StdEncoding.EncodeToString(b), acc.Secret
		}
		if len(e.Servers) == 0 {
			return a, false
		}
		a.IKEv2 = e
	case model.ProtocolOpenConnect:
		v := occdriver.GuideValues(acc, &at)
		if v.Server == "" {
			return a, false
		}
		others := make([]string, 0, len(v.Others))
		for _, u := range v.Others {
			others = append(others, httpsURL(u))
		}
		a.OpenConnect = &WUIAgentOC{
			URL: httpsURL(v.OC.URL), Others: others, Username: acc.Username, Password: acc.Secret,
			Pin: v.OC.Pin, SelfSigned: v.OC.Untrusted,
		}
	case model.ProtocolL2TP:
		v := l2tpdriver.GuideValues(acc, &at)
		servers := agentkit.Servers(&at)
		if len(servers) == 0 {
			return a, false
		}
		a.L2TP = &WUIAgentL2TP{
			Servers: servers, Port: portOr(v.OwnPort, 1701), PSK: v.PSK,
			Username: acc.Username, Password: acc.Secret, AllowPlain: v.AllowPlain,
		}
	case model.ProtocolPPTP:
		v := l2tpdriver.GuideValues(acc, &at)
		servers := agentkit.Servers(&at)
		if len(servers) == 0 {
			return a, false
		}
		a.PPTP = &WUIAgentPPP{Servers: servers, Port: portOr(v.OwnPort, 1723), Username: acc.Username, Password: acc.Secret}
	default:
		return a, false
	}
	return a, true
}

// httpsURL is an OpenConnect address as a URL: the guides give it as a
// person types it into Cisco's client, with no scheme.
func httpsURL(u string) string {
	if strings.Contains(u, "://") {
		return u
	}
	return "https://" + u
}

func portOr(p, std int) int {
	if p == 0 {
		return std
	}
	return p
}
