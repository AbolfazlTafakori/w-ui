package l2tpguide

import (
	"fmt"
	"strings"
)

// Words the page shows that are not a device's own.
const (
	keyAnything  = "l2tpAnything"
	keyEmpty     = "l2tpEmpty"
	keyOneDevice = "l2tpOneDevice"
)

var words = map[string]Text{
	keyAnything: {EN: "any name", FA: "هر نامی"},
	keyEmpty:    {EN: "leave empty", FA: "خالی بگذارید"},
	keyOneDevice: {
		EN: "One device per address: two devices behind the same router or the same mobile carrier address cannot both use L2TP/IPsec at once; the second ends the first. A router carries the whole home on one connection.",
		FA: "یک دستگاه برای هر آدرس: دو دستگاه پشت یک مودم یا یک آدرس اپراتور موبایل نمی‌توانند هم‌زمان با L2TP/IPsec وصل باشند؛ دومی اولی را قطع می‌کند. یک روتر کل خانه را با یک اتصال می‌برد.",
	},
	"l2tpDevice":      {EN: "Your device", FA: "دستگاه شما"},
	"l2tpGroupDevice": {EN: "Phones and computers", FA: "گوشی و کامپیوتر"},
	"l2tpGroupRouter": {EN: "Routers and modems", FA: "روتر و مودم"},
	"l2tpGroupPlain":  {EN: "Routers and modems without IPsec (not encrypted)", FA: "روتر و مودم بدون IPsec (بدون رمزگذاری)"},
	"l2tpPlainWarn": {
		EN: "This device has no IPsec: the connection is not encrypted. Use it only where nothing else will do.",
		FA: "این دستگاه IPsec ندارد: اتصال رمزگذاری نمی‌شود. فقط وقتی استفاده کنید که راه دیگری نیست.",
	},
	"l2tpOthers": {EN: "If it does not answer, use instead", FA: "اگر جواب نداد، به‌جایش"},
}

// adslID is the entry that is not a client at all: the modems that have none.
const adslID = "adsl"

// rosQuote is a value as RouterOS's console reads it back unchanged: quoted,
// with the one character that means something inside quotes escaped. The rest
// a password or key may hold here -- no spaces, quotes or backslashes -- is
// literal there.
func rosQuote(v string) string {
	return `"` + strings.ReplaceAll(v, "$", `\$`) + `"`
}

func mikrotik(ipsec bool) func(Values) string {
	return func(v Values) string {
		if ipsec {
			return fmt.Sprintf("/interface l2tp-client add name=wui-l2tp connect-to=%s user=%s password=%s use-ipsec=yes ipsec-secret=%s allow=mschap2 disabled=no",
				rosQuote(v.Server), rosQuote(v.Username), rosQuote(v.Password), rosQuote(v.PSK))
		}
		return fmt.Sprintf("/interface l2tp-client add name=wui-l2tp connect-to=%s user=%s password=%s use-ipsec=no allow=mschap2,chap disabled=no",
			rosQuote(v.Server), rosQuote(v.Username), rosQuote(v.Password))
	}
}

// The catalogue. Each device's path and field names are its own, as its
// maker's documentation and its settings pages show them; where a family's
// models differ, the common menu comes first and the older one in brackets.
var catalogue = []Guide{
	// ── phones and computers ──────────────────────────────────────────────
	{
		ID: "windows", Name: Text{EN: "Windows 10 / 11"}, Kind: Device, IPsec: true,
		Path: "Settings > Network & internet > VPN > Add VPN",
		Fields: []Field{
			{Label: "VPN provider", Value: "Windows (built-in)"},
			{Label: "Connection name", Source: Anything},
			{Label: "Server name or address", Source: Server},
			{Label: "VPN type", Value: "L2TP/IPsec with pre-shared key"},
			{Label: "Pre-shared key", Source: PSK},
			{Label: "Type of sign-in info", Value: "User name and password"},
			{Label: "User name", Source: Username},
			{Label: "Password", Source: Password},
		},
		Notes: []Text{{
			EN: "Error 809: run this once in a Command Prompt opened as administrator, then restart:",
			FA: "خطای 809: این را یک بار در Command Prompt با دسترسی Administrator اجرا کنید و سیستم را ری‌استارت کنید:",
		}},
		Command: func(Values) string {
			return `reg add HKLM\SYSTEM\CurrentControlSet\Services\PolicyAgent /v AssumeUDPEncapsulationContextOnSendRule /t REG_DWORD /d 2 /f`
		},
	},
	{
		ID: "ios", Name: Text{EN: "iPhone / iPad"}, Kind: Device, IPsec: true,
		Path: "Settings > General > VPN & Device Management > VPN > Add VPN Configuration",
		Fields: []Field{
			{Label: "Type", Value: "L2TP"},
			{Label: "Description", Source: Anything},
			{Label: "Server", Source: Server},
			{Label: "Account", Source: Username},
			{Label: "RSA SecurID", Value: "off"},
			{Label: "Password", Source: Password},
			{Label: "Secret", Source: PSK},
			{Label: "Send All Traffic", Value: "on"},
		},
	},
	{
		ID: "macos", Name: Text{EN: "macOS"}, Kind: Device, IPsec: true,
		Path: "System Settings > VPN > Add VPN Configuration > L2TP over IPSec",
		Fields: []Field{
			{Label: "Display name", Source: Anything},
			{Label: "Server address", Source: Server},
			{Label: "Account name", Source: Username},
			{Label: "User authentication: Password", Source: Password},
			{Label: "Machine authentication: Shared secret", Source: PSK},
		},
		Notes: []Text{{
			EN: "Under Options, turn on Send all traffic over VPN connection, or only the tunnel's own network goes through it.",
			FA: "در Options گزینه Send all traffic over VPN connection را روشن کنید، وگرنه فقط شبکه خود تانل از VPN می‌رود.",
		}},
	},
	{
		ID: "android", Name: Text{EN: "Android 11 and older", FA: "اندروید ۱۱ و قدیمی‌تر"}, Kind: Device, IPsec: true,
		Path: "Settings > Network & internet > VPN > +",
		Fields: []Field{
			{Label: "Name", Source: Anything},
			{Label: "Type", Value: "L2TP/IPSec PSK"},
			{Label: "Server address", Source: Server},
			{Label: "L2TP secret", Source: Empty},
			{Label: "IPSec identifier", Source: Empty},
			{Label: "IPSec pre-shared key", Source: PSK},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
	},
	{
		ID: "android12", Name: Text{EN: "Android 12 and newer", FA: "اندروید ۱۲ و جدیدتر"}, Kind: Device, IPsec: true,
		Notes: []Text{{
			EN: "Android 12 and newer no longer have L2TP. Use another configuration on your plan, or connect through a router from this list.",
			FA: "اندروید ۱۲ به بعد دیگر L2TP ندارد. از کانفیگ دیگری از پلن خود استفاده کنید، یا از طریق یکی از روترهای این فهرست وصل شوید.",
		}},
	},
	{
		ID: "linux", Name: Text{EN: "Linux (NetworkManager)"}, Kind: Device, IPsec: true,
		Path: "Settings > Network > VPN > + > Layer 2 Tunneling Protocol (L2TP)",
		Fields: []Field{
			{Label: "Gateway", Source: Server},
			{Label: "User name", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "IPsec Settings: Enable IPsec tunnel to L2TP host", Value: "on"},
			{Label: "IPsec Settings: Pre-shared key", Source: PSK},
		},
		Notes: []Text{{
			EN: "Needs the network-manager-l2tp package (network-manager-l2tp-gnome on Ubuntu).",
			FA: "بسته network-manager-l2tp لازم است (در اوبونتو network-manager-l2tp-gnome).",
		}},
	},
	{
		ID: "chromeos", Name: Text{EN: "ChromeOS"}, Kind: Device, IPsec: true,
		Path: "Settings > Network > Add connection > Add built-in VPN",
		Fields: []Field{
			{Label: "Service name", Source: Anything},
			{Label: "Provider type", Value: "L2TP/IPsec"},
			{Label: "Server hostname", Source: Server},
			{Label: "Authentication type", Value: "Pre-shared key"},
			{Label: "Pre-shared key", Source: PSK},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
	},

	// ── routers with IPsec ────────────────────────────────────────────────
	{
		ID: "mikrotik", Name: Text{EN: "MikroTik (RouterOS)"}, Kind: Router, IPsec: true,
		Path: "Interfaces > + > L2TP Client > Dial Out (or paste the command in Terminal)",
		Fields: []Field{
			{Label: "Connect To", Source: Server},
			{Label: "User", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Use IPsec", Value: "yes"},
			{Label: "IPsec Secret", Source: PSK},
			{Label: "Allow", Value: "mschap2"},
		},
		Command: mikrotik(true),
	},
	{
		ID: "keenetic", Name: Text{EN: "Keenetic"}, Kind: Router, IPsec: true,
		Path: "Internet > Other connections > VPN connections > Add connection",
		Fields: []Field{
			{Label: "Type (protocol)", Value: "L2TP/IPsec"},
			{Label: "Connection name", Source: Anything},
			{Label: "Server address", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Secret key", Source: PSK},
		},
	},
	{
		ID: "tplink-vpn", Name: Text{EN: "TP-Link Archer with VPN Client (AX20, AX23, AX55, AX73 and others)"}, Kind: Router, IPsec: true,
		Path: "Advanced > VPN Client > Server List > Add",
		Fields: []Field{
			{Label: "Description", Source: Anything},
			{Label: "VPN Type", Value: "L2TP/IPSec"},
			{Label: "VPN Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "IPSec Pre-Shared Key", Source: PSK},
		},
		Notes: []Text{{
			EN: "Then under Device List, add the devices that should go through the VPN.",
			FA: "سپس در Device List دستگاه‌هایی را که باید از VPN بروند اضافه کنید.",
		}},
	},
	{
		ID: "omada", Name: Text{EN: "TP-Link Omada (ER605, ER7206 and others)"}, Kind: Router, IPsec: true,
		Path: "VPN > L2TP > L2TP Client (controller: Settings > VPN > Create New VPN > Client > L2TP)",
		Fields: []Field{
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Server IP", Source: ServerIP},
			{Label: "IPsec Encryption", Value: "Encrypted"},
			{Label: "Pre-shared Key", Source: PSK},
			{Label: "Working Mode", Value: "NAT"},
		},
	},

	{
		ID: adslID, Name: Text{EN: "ADSL / VDSL modem (TD-W8961N, TD-W9970, DSL-124, DSL-2750U, Utel and others)", FA: "مودم ADSL / VDSL (TD-W8961N، TD-W9970، DSL-124، DSL-2750U، یوتل و دیگران)"}, Kind: Router,
		Notes: []Text{{
			EN: "These modems have no L2TP client of their own. Put one of the routers in this list behind the modem, or set up each device itself.",
			FA: "این مودم‌ها کلاینت L2TP ندارند. یکی از روترهای این فهرست را پشت مودم بگذارید، یا روی خود هر دستگاه تنظیم کنید.",
		}},
	},

	// ── routers and modems without IPsec ──────────────────────────────────
	{
		ID: "tfi60", Name: Text{EN: "Irancell TF-i60 (G1, B1, H1), GP-2101", FA: "ایرانسل TF-i60 (G1، B1، H1)، GP-2101"}, Kind: Router,
		Path: "Settings > VPN",
		Fields: []Field{
			{Label: "VPN", Value: "Enable"},
			{Label: "Protocol", Value: "L2TP"},
			{Label: "VPN Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Tunnel Password", Source: TunnelPassword},
		},
		Notes: []Text{{
			EN: "TF-i60 E1 has no VPN settings.",
			FA: "مدل TF-i60 E1 تنظیمات VPN ندارد.",
		}},
	},
	{
		ID: "huawei", Name: Text{EN: "Huawei 4G/5G (B311, B316, B525, B535, B612, 4G Router 2/3)"}, Kind: Router,
		Path: "Advanced > Router > VPN",
		Fields: []Field{
			{Label: "Enable VPN", Value: "on"},
			{Label: "Connection type", Value: "L2TP VPN client"},
			{Label: "LNS address", Source: Server},
			{Label: "Hostname", Source: Anything},
			{Label: "Tunnel password", Source: TunnelPassword},
			{Label: "PPP user name", Source: Username},
			{Label: "PPP password", Source: Password},
		},
	},
	{
		ID: "tplink-wan", Name: Text{EN: "TP-Link router (TL-WR840N, TL-WR940N, Archer C6, C20, C50, C80 and others)"}, Kind: Router,
		Path: "Advanced > Network > Internet (older panels: Network > WAN)",
		Fields: []Field{
			{Label: "Internet Connection Type", Value: "L2TP (or Russia L2TP)"},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Secondary Connection", Value: "Dynamic IP"},
			{Label: "VPN Server IP/Domain Name", Source: ServerIP},
			{Label: "Connection Mode", Value: "Connect Automatically"},
		},
		Notes: []Text{{
			EN: "The router's WAN port goes to a LAN port of the modem; it reaches the server through the modem.",
			FA: "پورت WAN روتر به یکی از پورت‌های LAN مودم وصل می‌شود؛ روتر از طریق مودم به سرور می‌رسد.",
		}},
	},
	{
		ID: "dlink", Name: Text{EN: "D-Link router (DIR-615, DIR-822, DIR-842 and others)"}, Kind: Router,
		Path: "Setup > Internet > Manual Internet Connection Setup",
		Fields: []Field{
			{Label: "My Internet Connection is", Value: "L2TP (Username/Password)"},
			{Label: "Address Mode", Value: "Dynamic IP"},
			{Label: "L2TP Server IP Address", Source: ServerIP},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Reconnect Mode", Value: "Always on"},
		},
		Notes: []Text{{
			EN: "The router's WAN port goes to a LAN port of the modem.",
			FA: "پورت WAN روتر به یکی از پورت‌های LAN مودم وصل می‌شود.",
		}},
	},
	{
		ID: "tenda", Name: Text{EN: "Tenda (AC6, AC10, AC21 and others)"}, Kind: Router,
		Path: "Internet Settings (or VPN > PPTP/L2TP Client)",
		Fields: []Field{
			{Label: "Connection Type", Value: "L2TP (or Russia L2TP)"},
			{Label: "Server IP/Domain Name", Source: ServerIP},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Address Mode", Value: "Dynamic"},
		},
	},
	{
		ID: "xiaomi", Name: Text{EN: "Xiaomi (Mi Router 4A, 4C, AX3000 and others)"}, Kind: Router,
		Path: "Advanced settings > VPN > Add service",
		Fields: []Field{
			{Label: "Name", Source: Anything},
			{Label: "Protocol", Value: "L2TP"},
			{Label: "Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
	},
	{
		ID: "asus", Name: Text{EN: "ASUS"}, Kind: Router,
		Path: "VPN > VPN Fusion > Add profile (older: VPN > VPN Client > Add profile)",
		Fields: []Field{
			{Label: "VPN type", Value: "L2TP"},
			{Label: "Description", Source: Anything},
			{Label: "VPN Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
	},
	{
		ID: "openwrt", Name: Text{EN: "OpenWrt"}, Kind: Router,
		Path: "Network > Interfaces > Add new interface > Protocol: L2TP",
		Fields: []Field{
			{Label: "L2TP Server", Source: Server},
			{Label: "PAP/CHAP username", Source: Username},
			{Label: "PAP/CHAP password", Source: Password},
		},
		Notes: []Text{{
			EN: "Needs the xl2tpd and ppp-mod-pppol2tp packages; put the new interface in the wan firewall zone. Or run:",
			FA: "بسته‌های xl2tpd و ppp-mod-pppol2tp لازم است؛ اینترفیس جدید را در زون فایروال wan بگذارید. یا اجرا کنید:",
		}},
		Command: func(v Values) string {
			return fmt.Sprintf("opkg update && opkg install xl2tpd ppp-mod-pppol2tp && uci set network.wui=interface && uci set network.wui.proto=l2tp && uci set network.wui.server='%s' && uci set network.wui.username='%s' && uci set network.wui.password='%s' && uci commit network && /etc/init.d/network reload",
				v.Server, v.Username, v.Password)
		},
	},
	{
		ID: "mikrotik-plain", Name: Text{EN: "MikroTik without IPsec", FA: "میکروتیک بدون IPsec"}, Kind: Router,
		Path: "Interfaces > + > L2TP Client > Dial Out (or paste the command in Terminal)",
		Fields: []Field{
			{Label: "Connect To", Source: Server},
			{Label: "User", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Use IPsec", Value: "no"},
		},
		Command: mikrotik(false),
	},
}
