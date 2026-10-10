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
	"l2tpGroupRouter": {EN: "Routers and modems with IPsec", FA: "روتر و مودم با IPsec"},
	"l2tpGroupNone":   {EN: "Models with no L2TP client", FA: "مدل‌هایی که کلاینت L2TP ندارند"},
	"l2tpGroupPlain":  {EN: "Routers and modems without IPsec (not encrypted)", FA: "روتر و مودم بدون IPsec (بدون رمزگذاری)"},
	"l2tpPlainWarn": {
		EN: "This device has no IPsec: the connection is not encrypted. Use it only where nothing else will do.",
		FA: "این دستگاه IPsec ندارد: اتصال رمزگذاری نمی‌شود. فقط وقتی استفاده کنید که راه دیگری نیست.",
	},
	"l2tpOthers": {EN: "If it does not answer, use instead", FA: "اگر جواب نداد، به‌جایش"},
}

// adslID is the generic entry for the modems with no client at all.
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

// ── families ─────────────────────────────────────────────────────────────
//
// Most makers put one set of pages on many models. Each family below is one
// such set, as its maker's emulator (or, where there is none, its manual)
// shows it; every model that wears it is its own entry, so a customer finds
// the name printed on their own box.

// Notes several families share.
var (
	noteBehindModem = Text{
		EN: "The router's WAN (Internet) port goes to a LAN port of the modem; it reaches the server through the modem.",
		FA: "پورت WAN (Internet) روتر به یکی از پورت‌های LAN مودم وصل می‌شود؛ روتر از طریق مودم به سرور می‌رسد.",
	}
	noteDeviceList = Text{
		EN: "Turn VPN Client on, then under Device List add the devices that should go through it.",
		FA: "ابتدا VPN Client را روشن کنید، سپس در Device List دستگاه‌هایی را که باید از VPN بروند اضافه کنید.",
	}
	noteKey32 = Text{
		EN: "This page takes a key of at most 32 characters.",
		FA: "این صفحه کلیدی تا ۳۲ کاراکتر می‌پذیرد.",
	}
	noteNoClient = Text{
		EN: "This model has no L2TP client of its own. Put one of the routers in this list behind it, or set up each device itself.",
		FA: "این مدل کلاینت L2TP ندارد. یکی از روترهای این فهرست را پشتش بگذارید، یا روی خود هر دستگاه تنظیم کنید.",
	}
	note4G = Text{
		EN: "It runs over the SIM card's connection; nothing else is needed in front of it.",
		FA: "روی اینترنت همان سیم‌کارت کار می‌کند؛ چیز دیگری جلویش لازم نیست.",
	}
	noteNoMPPE = Text{
		EN: "Under All Settings, leave Authentication protocol on AUTO and set Encryption protocol to No encryption: the server does not do MPPE, and a router that requires it is refused.",
		FA: "در All Settings، گزینه Authentication protocol را روی AUTO بگذارید و Encryption protocol را روی No encryption: سرور MPPE ندارد و روتری که آن را اجباری کند رد می‌شود.",
	}
)

// tplinkInternet is TP-Link's and Mercusys's current pages (Archer C6 v4,
// C64, C80, TL-WR840N v7, Mercusys MW301R ...): the Internet page's L2TP.
func tplinkInternet(id string, name Text) Guide {
	return Guide{
		ID: id, Name: name, Kind: Router,
		Path: "Advanced > Network > Internet",
		Fields: []Field{
			{Label: "Internet Connection Type", Value: "L2TP"},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Dynamic IP / Static IP", Value: "Dynamic IP"},
			{Label: "VPN Server IP/Domain Name", Source: Server},
			{Label: "Connection Mode", Value: "Auto"},
		},
		Notes: []Text{noteBehindModem},
	}
}

// tplinkVPNClient is the VPN Client of TP-Link's Wi-Fi 6 routers and
// Mercusys's: L2TP/IPSec with the key, for the devices chosen in Device List.
func tplinkVPNClient(id string, name Text, path string, notes ...Text) Guide {
	return Guide{
		ID: id, Name: name, Kind: Router, IPsec: true,
		Path: path,
		Fields: []Field{
			{Label: "Description", Source: Anything},
			{Label: "VPN Type", Value: "L2TP/IPSec"},
			{Label: "VPN Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "IPSec Pre-Shared Key", Source: PSK},
		},
		Notes: append([]Text{noteDeviceList}, notes...),
	}
}

const tplinkVPNClientPath = "Advanced > VPN Client > Server List > Add"

// tplinkClassicMTK is the older green pages of Archer C20 and C50.
func tplinkClassicMTK(id string, name Text) Guide {
	return Guide{
		ID: id, Name: name, Kind: Router,
		Path: "Network > WAN",
		Fields: []Field{
			{Label: "Connection Type", Value: "L2TP"},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Addressing Type", Value: "Dynamic IP"},
			{Label: "Server IP Address/Name", Source: Server},
			{Label: "Connection Mode", Value: "Connect automatically"},
		},
		Notes: []Text{noteBehindModem},
	}
}

// tplinkUserRpm is the oldest pages, still on TL-WR940N and many TL-WR841N.
func tplinkUserRpm(id string, name Text) Guide {
	return Guide{
		ID: id, Name: name, Kind: Router,
		Path: "Network > WAN",
		Fields: []Field{
			{Label: "WAN Connection Type", Value: "L2TP/Russia L2TP"},
			{Label: "User Name", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Confirm Password", Source: Password},
			{Label: "Dynamic IP / Static IP", Value: "Dynamic IP"},
			{Label: "Server IP Address/Name", Source: Server},
			{Label: "Connection Mode", Value: "Connect Automatically"},
		},
		Notes: []Text{noteBehindModem},
	}
}

// tplink4G is the VPN Client of TP-Link's 4G routers: L2TP without IPsec,
// over the SIM card.
func tplink4G(id string, name Text) Guide {
	return Guide{
		ID: id, Name: name, Kind: Router,
		Path: "Advanced > VPN Client > Server List > Add",
		Fields: []Field{
			{Label: "Description", Source: Anything},
			{Label: "VPN Type", Value: "L2TPv2"},
			{Label: "VPN Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
		Notes: []Text{note4G, noteDeviceList},
	}
}

// tplinkEWAN is the DSL modem routers whose L2TP is only in Wireless Router
// mode, on the Ethernet WAN port.
func tplinkEWAN(id string, name Text) Guide {
	return Guide{
		ID: id, Name: name, Kind: Router,
		Path: "Advanced > Operation Mode: Wireless Router Mode, then Advanced > Network > Internet",
		Fields: []Field{
			{Label: "Connection Type", Value: "L2TP"},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Server IP Address/Name", Source: Server},
		},
		Notes: []Text{{
			EN: "Only in Wireless Router Mode: the phone line is not used, and the Ethernet WAN port goes to another modem's LAN port.",
			FA: "فقط در Wireless Router Mode: خط تلفن استفاده نمی‌شود و پورت Ethernet WAN به پورت LAN یک مودم دیگر وصل می‌شود.",
		}},
	}
}

// dlinkAnweb is D-Link's pages on DIR-615/T4, DIR-825/R1, DIR-842/R1 and
// the DWR-921 4G router.
func dlinkAnweb(id string, name Text, notes ...Text) Guide {
	return Guide{
		ID: id, Name: name, Kind: Router,
		Path: "Connections Setup > WAN > + (Add)",
		Fields: []Field{
			{Label: "Connection type", Value: "L2TP"},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "VPN server address", Source: Server},
		},
		Notes: append([]Text{noteNoMPPE}, notes...),
	}
}

// noClient is a model with no L2TP client at all, said so by name.
func noClient(id string, name Text, extra ...Text) Guide {
	return Guide{ID: id, Name: name, Kind: Router, Notes: append([]Text{noteNoClient}, extra...)}
}

func en(s string) Text { return Text{EN: s} }

// The catalogue. Phones and computers first, then routers with IPsec, the
// routers and modems without IPsec, and last the models with no client.
var catalogue = []Guide{
	// ── phones and computers ──────────────────────────────────────────────
	{
		ID: "windows", Name: en("Windows 10 / 11"), Kind: Device, IPsec: true,
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
		ID: "ios", Name: en("iPhone / iPad"), Kind: Device, IPsec: true,
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
		ID: "macos", Name: en("macOS"), Kind: Device, IPsec: true,
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
		ID: "linux", Name: en("Linux (NetworkManager)"), Kind: Device, IPsec: true,
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
		ID: "chromeos", Name: en("ChromeOS"), Kind: Device, IPsec: true,
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
		ID: "mikrotik", Name: en("MikroTik (RouterOS: hAP, hEX, RB, CCR)"), Kind: Router, IPsec: true,
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
		ID: "keenetic", Name: en("Keenetic (Giga, Ultra, Viva, Hero, Runner 4G and others)"), Kind: Router, IPsec: true,
		Path: "Internet > Other connections > VPN connections > Create connection",
		Fields: []Field{
			{Label: "Type (protocol)", Value: "L2TP/IPsec"},
			{Label: "Connection name", Source: Anything},
			{Label: "Server address", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Secret key", Source: PSK},
		},
		Notes: []Text{{
			EN: "Needs the system component \"IKEv1/IPsec and IKEv2/IPsec VPN servers, L2TP/IPsec VPN client, Site-to-site IPsec VPN\" (General system settings > Component options).",
			FA: "کامپوننت «IKEv1/IPsec and IKEv2/IPsec VPN servers, L2TP/IPsec VPN client, Site-to-site IPsec VPN» لازم است (General system settings > Component options).",
		}},
	},
	tplinkVPNClient("tplink-ax23", en("TP-Link Archer AX23 v2"), "Advanced > VPN Client > Server List > Add > Set up manually", noteKey32),
	tplinkVPNClient("tplink-ax55", en("TP-Link Archer AX55 / AX55 Pro"), tplinkVPNClientPath),
	tplinkVPNClient("tplink-ax73", en("TP-Link Archer AX73 v2"), tplinkVPNClientPath),
	tplinkVPNClient("tplink-ax53", en("TP-Link Archer AX53 v2"), tplinkVPNClientPath),
	tplinkVPNClient("tplink-ax10", en("TP-Link Archer AX10 v3 / AX1500 v3"), tplinkVPNClientPath),
	tplinkVPNClient("tplink-ax21", en("TP-Link Archer AX21 v3"), tplinkVPNClientPath),
	tplinkVPNClient("mercusys-mr70x", en("Mercusys MR70X"), tplinkVPNClientPath),
	{
		ID: "omada", Name: en("TP-Link Omada ER605 / ER7206"), Kind: Router, IPsec: true,
		Path: "VPN > L2TP > L2TP Client > Add",
		Fields: []Field{
			{Label: "Tunnel", Source: Anything},
			{Label: "Account Name", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Server IP", Source: ServerIP},
			{Label: "IPSec Encryption", Value: "Encrypted"},
			{Label: "Pre-shared Key", Source: PSK},
			{Label: "Working Mode", Value: "NAT"},
			{Label: "Status", Value: "Enable"},
		},
	},
	{
		ID: "dlink-842v2", Name: en("D-Link DIR-842 V2 (EU firmware)"), Kind: Router, IPsec: true,
		Path: "Settings > Internet > WAN > ADVANCED MODE > + (Add)",
		Fields: []Field{
			{Label: "Connection type", Value: "L2TP over IPsec"},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "VPN server address", Source: Server},
			{Label: "Pre-shared key", Source: PSK},
			{Label: "Enable PFS", Value: "off"},
		},
		Notes: []Text{{
			EN: "After APPLY, choose \"to the Internet\" and the connection the router already has.",
			FA: "بعد از APPLY گزینه «to the Internet» و اتصالی را که روتر از قبل دارد انتخاب کنید.",
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
		Notes: []Text{{EN: "TF-i60 E1 has no VPN settings.", FA: "مدل TF-i60 E1 تنظیمات VPN ندارد."}},
	},
	{
		ID: "huawei", Name: en("Huawei 4G/5G: B311B, B316, 4G CPE 3 (B535), 4G Router 2/3 Pro, 4G CPE 5, 5G CPE Pro"), Kind: Router,
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
		Notes: []Text{note4G},
	},
	tplink4G("tplink-mr6400", en("TP-Link TL-MR6400 v9 (4G)")),
	tplink4G("tplink-mr100", en("TP-Link TL-MR100 v5 (4G)")),
	tplink4G("tplink-mr200", en("TP-Link Archer MR200 v9 (4G)")),
	tplink4G("tplink-mr600", en("TP-Link Archer MR600 v5 (4G+)")),
	tplink4G("tplink-mr6500v", en("TP-Link TL-MR6500v v3 (4G)")),
	dlinkAnweb("dlink-dwr921", en("D-Link DWR-921 (4G)"), note4G),
	tplinkInternet("tplink-c6", en("TP-Link Archer C6 v4")),
	tplinkInternet("tplink-c64", en("TP-Link Archer C64")),
	tplinkInternet("tplink-c80", en("TP-Link Archer C80")),
	tplinkInternet("tplink-c86", en("TP-Link Archer C86")),
	tplinkInternet("tplink-a6", en("TP-Link Archer A6 v4")),
	tplinkInternet("tplink-c24", en("TP-Link Archer C24 v2")),
	tplinkInternet("tplink-ax12", en("TP-Link Archer AX12")),
	tplinkInternet("tplink-ax20", en("TP-Link Archer AX20 v2")),
	tplinkInternet("tplink-ax72", en("TP-Link Archer AX72")),
	tplinkInternet("tplink-wr840n", en("TP-Link TL-WR840N v7")),
	tplinkInternet("tplink-wr841n", en("TP-Link TL-WR841N v14")),
	tplinkInternet("tplink-wr845n", en("TP-Link TL-WR845N v4")),
	tplinkClassicMTK("tplink-c20", en("TP-Link Archer C20 v6")),
	tplinkClassicMTK("tplink-c50", en("TP-Link Archer C50 v6")),
	tplinkUserRpm("tplink-wr940n", en("TP-Link TL-WR940N, and TL-WR841N up to v13")),
	tplinkEWAN("tplink-vr600", en("TP-Link Archer VR600 v2")),
	tplinkEWAN("tplink-w8970", en("TP-Link TD-W8970 v3")),
	tplinkInternet("mercusys-mw301r", en("Mercusys MW301R")),
	tplinkInternet("mercusys-mw302r", en("Mercusys MW302R")),
	tplinkInternet("mercusys-mw325r", en("Mercusys MW325R")),
	tplinkInternet("mercusys-ac12", en("Mercusys AC12")),
	tplinkInternet("mercusys-mr50g", en("Mercusys MR50G")),
	dlinkAnweb("dlink-842r1", en("D-Link DIR-842/R1"), noteBehindModem),
	dlinkAnweb("dlink-825r1", en("D-Link DIR-825/R1"), noteBehindModem),
	dlinkAnweb("dlink-615t4", en("D-Link DIR-615/T4"), noteBehindModem),
	{
		ID: "asus", Name: en("ASUS (RT-AX, RT-AC, RT-N, TUF, ROG)"), Kind: Router,
		Path: "Advanced Settings > VPN > VPN Client (VPN Fusion) > Add profile > L2TP",
		Fields: []Field{
			{Label: "Description", Source: Anything},
			{Label: "VPN Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
		Notes: []Text{{EN: "Then click Activate.", FA: "سپس Activate را بزنید."}},
	},
	{
		ID: "xiaomi", Name: en("Xiaomi (Router 4A, 4C, AX3000T, AX3000 and others)"), Kind: Router,
		Path: "Additional settings > VPN > Add service",
		Fields: []Field{
			{Label: "Protocol", Value: "L2TP"},
			{Label: "Name", Source: Anything},
			{Label: "Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
		Notes: []Text{{EN: "Save, then turn the service on.", FA: "ذخیره کنید و سرویس را روشن کنید."}},
	},
	{
		ID: "openwrt", Name: en("OpenWrt"), Kind: Router,
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

	// ── models with no L2TP client ────────────────────────────────────────
	noClient("tplink-w9960", en("TP-Link TD-W9960 (VDSL/ADSL)")),
	noClient("tplink-w9970", en("TP-Link TD-W9970 (VDSL/ADSL)")),
	noClient("tplink-vr", en("TP-Link Archer VR300 / VR400 (VDSL/ADSL)")),
	{
		ID: adslID, Name: Text{EN: "Other ADSL / VDSL modems (TD-W8961N, Utel and others)", FA: "مودم‌های دیگر ADSL / VDSL (TD-W8961N، یوتل و دیگران)"}, Kind: Router,
		Notes: []Text{noteNoClient},
	},
	noClient("dlink-dsl", en("D-Link DSL-2750U / DSL-224")),
	noClient("tplink-extender", en("Wi-Fi extenders (TP-Link RE200, RE305, TL-WA850RE and others)"), Text{
		EN: "An extender only repeats the router's Wi-Fi: the router it extends is the one to set up.",
		FA: "تقویت‌کننده فقط وای‌فای روتر را تکرار می‌کند: باید خود روتری را که تقویت می‌کند تنظیم کنید.",
	}),
	noClient("tenda", en("Tenda AC6 v5, AC10 v3, N301 v4 and other current Tenda")),
}
