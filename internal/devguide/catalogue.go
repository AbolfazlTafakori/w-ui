package devguide

import (
	"fmt"
	"strings"
)

// Words the pages show that are not a device's own.
const (
	keyAnything  = "dgAnything"
	keyEmpty     = "dgEmpty"
	keyOneDevice = "dgOneDevice"
)

var words = map[string]Text{
	keyAnything: {EN: "any name", FA: "هر نامی"},
	keyEmpty:    {EN: "leave empty", FA: "خالی بگذارید"},
	keyOneDevice: {
		EN: "One device per address: two devices behind the same router or the same mobile carrier address cannot both use L2TP/IPsec at once; the second ends the first. A router carries the whole home on one connection.",
		FA: "یک دستگاه برای هر آدرس: دو دستگاه پشت یک مودم یا یک آدرس اپراتور موبایل نمی‌توانند هم‌زمان با L2TP/IPsec وصل باشند؛ دومی اولی را قطع می‌کند. یک روتر کل خانه را با یک اتصال می‌برد.",
	},
	"dgDevice":      {EN: "Your device", FA: "دستگاه شما"},
	"dgProtocol":    {EN: "Connection", FA: "اتصال"},
	"dgSetup":       {EN: "Set up a device", FA: "راه‌اندازی روی دستگاه"},
	"dgGroupDevice": {EN: "Phones and computers", FA: "گوشی و کامپیوتر"},
	"dgGroupRouter": {EN: "Routers and modems", FA: "روتر و مودم"},
	"dgGroupNone":   {EN: "Models with no VPN client", FA: "مدل‌هایی که کلاینت VPN ندارند"},
	"dgWeak": {
		EN: "This connection is not encrypted, or only with a cipher long since broken. Use it only where nothing else will do.",
		FA: "این اتصال رمزگذاری نمی‌شود، یا با رمزی که سال‌هاست شکسته شده. فقط وقتی استفاده کنید که راه دیگری نیست.",
	},
	"dgNoneOfPlan": {
		EN: "This device can dial none of the connections on your plan. It can dial:",
		FA: "این دستگاه هیچ‌کدام از اتصال‌های پلن شما را ندارد. این‌ها را دارد:",
	},
	"dgOthers":  {EN: "If it does not answer, use instead", FA: "اگر جواب نداد، به‌جایش"},
	"dgSearch":  {EN: "Search for your device or router", FA: "جستجوی دستگاه یا روتر"},
	"dgNoMatch": {EN: "No device matches; the full list is below.", FA: "دستگاهی پیدا نشد؛ فهرست کامل پایین است."},
	"dgMatches": {EN: "{n} found", FA: "پیدا شد: {n}"},
}

// rosQuote is a value as RouterOS's console reads it back unchanged: quoted,
// with the one character that means something inside quotes escaped. The rest
// a password or key may hold here -- no spaces, quotes or backslashes -- is
// literal there.
func rosQuote(v string) string {
	return `"` + strings.ReplaceAll(v, "$", `\$`) + `"`
}

// ── notes several devices share ──────────────────────────────────────────

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
		EN: "This model has no VPN client of its own. Put one of the routers in this list behind it, or set up each device itself.",
		FA: "این مدل کلاینت VPN ندارد. یکی از روترهای این فهرست را پشتش بگذارید، یا روی خود هر دستگاه تنظیم کنید.",
	}
	note4G = Text{
		EN: "It runs over the SIM card's connection; nothing else is needed in front of it.",
		FA: "روی اینترنت همان سیم‌کارت کار می‌کند؛ چیز دیگری جلویش لازم نیست.",
	}
	noteNoMPPE = Text{
		EN: "Under All Settings, leave Authentication protocol on AUTO and set Encryption protocol to No encryption: the server does not do MPPE on L2TP, and a router that requires it is refused.",
		FA: "در All Settings، گزینه Authentication protocol را روی AUTO بگذارید و Encryption protocol را روی No encryption: سرور روی L2TP رمزگذاری MPPE ندارد و روتری که آن را اجباری کند رد می‌شود.",
	}
	notePPTPMPPE = Text{
		EN: "PPTP is accepted only with MS-CHAPv2 and 128-bit MPPE: choose those wherever the page offers them.",
		FA: "سرور، PPTP را فقط با MS-CHAPv2 و MPPE ۱۲۸ بیتی می‌پذیرد: هر جا صفحه گزینه‌اش را دارد همین‌ها را انتخاب کنید.",
	}
	noteUnchecked = Text{
		EN: "Not checked against this model's own pages: the names here may differ a little from what it shows.",
		FA: "این راهنما با صفحه‌های خود این مدل چک نشده: ممکن است نام گزینه‌ها کمی فرق داشته باشد.",
	}
	noteMaybeNone = Text{
		EN: "If its settings have no VPN section, this model has no VPN client: set up each device itself.",
		FA: "اگر تنظیماتش بخش VPN ندارد، این مدل کلاینت VPN ندارد: روی خود هر دستگاه تنظیم کنید.",
	}
	noteImportFile = Text{
		EN: "Download the file from this page first, then choose it here.",
		FA: "ابتدا فایل را از همین صفحه دانلود کنید، سپس اینجا انتخابش کنید.",
	}
	noteWGApp = Text{
		EN: "On an AmneziaWG tunnel, use the AmneziaWG app instead: the same steps, the same file.",
		FA: "اگر تانل شما AmneziaWG است، به‌جایش اپ AmneziaWG را نصب کنید: همان مراحل، همان فایل.",
	}
)

// ── families ─────────────────────────────────────────────────────────────
//
// Most makers put one set of pages on many models. Each family below is one
// such set, as its maker's emulator (or, where there is none, its manual)
// shows it; every model that wears it is its own entry, so a customer finds
// the name printed on their own box.

func en(s string) Text { return Text{EN: s} }

func router(id string, name Text, setups ...Setup) Guide {
	return Guide{ID: id, Name: name, Kind: Router, Setups: setups}
}

func withNotes(s Setup, notes ...Text) Setup {
	s.Notes = append(append([]Text(nil), s.Notes...), notes...)
	return s
}

// noClient is a model with no VPN client at all, said so by name.
func noClient(id string, name Text, extra ...Text) Guide {
	return Guide{ID: id, Name: name, Kind: Router, Notes: append([]Text{noteNoClient}, extra...)}
}

// TP-Link's and Mercusys's current pages (Archer C6 v4, C64, C80, TL-WR840N
// v7, Mercusys MW301R ...) and their older Archer C6 v2: the Internet page.
func tplinkInternet(p Protocol) Setup {
	return Setup{
		Protocol: p,
		Path:     "Advanced > Network > Internet",
		Fields: []Field{
			{Label: "Internet Connection Type", Value: wanName(p)},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Dynamic IP / Static IP", Value: "Dynamic IP"},
			{Label: "VPN Server IP/Domain Name", Source: Server},
			{Label: "Connection Mode", Value: "Auto"},
		},
		Notes: []Text{noteBehindModem},
	}
}

func wanName(p Protocol) string {
	if p == PPTP {
		return "PPTP"
	}
	return "L2TP"
}

func internetRouter(id string, name Text) Guide {
	return router(id, name, tplinkInternet(L2TPPlain), tplinkInternet(PPTP))
}

// The VPN Client of TP-Link's Wi-Fi 6 routers and Mercusys's.
const tplinkVPNClientPath = "Advanced > VPN Client > Server List > Add"

func tplinkVPNClient(path string, p Protocol) Setup {
	base := []Field{{Label: "Description", Source: Anything}}
	var fields []Field
	switch p {
	case WireGuard:
		fields = append(base, Field{Label: "VPN Type", Value: "WireGuard"}, Field{Label: "Import from Config File", Source: File})
	case OpenVPN:
		fields = append(base, Field{Label: "VPN Type", Value: "OpenVPN"},
			Field{Label: "Username", Source: Username}, Field{Label: "Password", Source: Password},
			Field{Label: "Import .ovpn File", Source: File})
	case L2TP:
		fields = append(base, Field{Label: "VPN Type", Value: "L2TP/IPSec"},
			Field{Label: "VPN Server", Source: Server}, Field{Label: "Username", Source: Username},
			Field{Label: "Password", Source: Password}, Field{Label: "IPSec Pre-Shared Key", Source: PSK})
	case PPTP:
		fields = append(base, Field{Label: "VPN Type", Value: "PPTP"},
			Field{Label: "VPN Server", Source: Server}, Field{Label: "Username", Source: Username},
			Field{Label: "Password", Source: Password}, Field{Label: "Encryption", Value: "Max (MPPE)"})
	}
	s := Setup{Protocol: p, Path: path, Fields: fields, Notes: []Text{noteDeviceList}, NoAmnezia: p == WireGuard}
	if p == WireGuard || p == OpenVPN {
		s.Notes = append(s.Notes, noteImportFile)
	}
	return s
}

func vpnClientRouter(id string, name Text, path string, protocols []Protocol, extra ...Text) Guide {
	g := router(id, name)
	for _, p := range protocols {
		s := tplinkVPNClient(path, p)
		if p == L2TP {
			s = withNotes(s, extra...)
		}
		g.Setups = append(g.Setups, s)
	}
	return g
}

var (
	opl  = []Protocol{OpenVPN, PPTP, L2TP}
	wopl = []Protocol{WireGuard, OpenVPN, PPTP, L2TP}
)

// The older green pages of Archer C20, C50 and TL-WR840N v5/v6.
func tplinkClassicMTK(p Protocol) Setup {
	return Setup{
		Protocol: p,
		Path:     "Network > WAN",
		Fields: []Field{
			{Label: "Connection Type", Value: wanName(p)},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Addressing Type", Value: "Dynamic IP"},
			{Label: "Server IP Address/Name", Source: Server},
			{Label: "Connection Mode", Value: "Connect automatically"},
		},
		Notes: []Text{noteBehindModem},
	}
}

func classicRouter(id string, name Text) Guide {
	return router(id, name, tplinkClassicMTK(L2TPPlain), tplinkClassicMTK(PPTP))
}

// The oldest pages, still on TL-WR940N and many TL-WR841N.
func tplinkUserRpm(p Protocol) Setup {
	return Setup{
		Protocol: p,
		Path:     "Network > WAN",
		Fields: []Field{
			{Label: "WAN Connection Type", Value: wanName(p) + "/Russia " + wanName(p)},
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

// The VPN Client of TP-Link's 4G routers, over the SIM card.
func tplink4G(p Protocol) Setup {
	path := "Advanced > VPN Client > Server List > Add"
	base := []Field{{Label: "Description", Source: Anything}}
	var fields []Field
	switch p {
	case WireGuard:
		fields = append(base, Field{Label: "VPN Type", Value: "WireGuard"}, Field{Label: "Import wireguard File", Source: File})
	case OpenVPN:
		fields = append(base, Field{Label: "VPN Type", Value: "OpenVPN"},
			Field{Label: "Username", Source: Username}, Field{Label: "Password", Source: Password},
			Field{Label: "Import .ovpn File", Source: File})
	case L2TPPlain:
		fields = append(base, Field{Label: "VPN Type", Value: "L2TPv2"},
			Field{Label: "VPN Server", Source: Server}, Field{Label: "Username", Source: Username},
			Field{Label: "Password", Source: Password})
	case PPTP:
		fields = append(base, Field{Label: "VPN Type", Value: "PPTP"},
			Field{Label: "VPN Server", Source: Server}, Field{Label: "Username", Source: Username},
			Field{Label: "Password", Source: Password}, Field{Label: "Encryption", Value: "Max(MPPE)"})
	}
	s := Setup{Protocol: p, Path: path, Fields: fields, Notes: []Text{note4G, noteDeviceList}, NoAmnezia: p == WireGuard}
	if p == WireGuard || p == OpenVPN {
		s.Notes = append(s.Notes, noteImportFile)
	}
	return s
}

func router4G(id string, name Text, protocols ...Protocol) Guide {
	g := router(id, name)
	for _, p := range protocols {
		g.Setups = append(g.Setups, tplink4G(p))
	}
	return g
}

// The DSL modem routers whose L2TP and PPTP are only in Wireless Router
// mode, on the Ethernet WAN port.
func tplinkEWAN(p Protocol) Setup {
	return Setup{
		Protocol: p,
		Path:     "Advanced > Operation Mode: Wireless Router Mode, then Advanced > Network > Internet",
		Fields: []Field{
			{Label: "Connection Type", Value: wanName(p)},
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

// D-Link's pages on DIR-615/T4, DIR-825/R1, DIR-842/R1 and the DWR-921.
func dlinkAnweb(p Protocol, extra ...Text) Setup {
	s := Setup{
		Protocol: p,
		Path:     "Connections Setup > WAN > + (Add)",
		Fields: []Field{
			{Label: "Connection type", Value: wanName(p)},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "VPN server address", Source: Server},
		},
	}
	if p == PPTP {
		s.Fields = append(s.Fields, Field{Label: "Authentication protocol", Value: "MS-CHAPv2"}, Field{Label: "Encryption protocol", Value: "MPPE 128 bit"})
	} else {
		s.Notes = []Text{noteNoMPPE}
	}
	s.Notes = append(s.Notes, extra...)
	return s
}

func anwebRouter(id string, name Text, extra ...Text) Guide {
	return router(id, name, dlinkAnweb(L2TPPlain, extra...), dlinkAnweb(PPTP, extra...))
}

// D-Link's international pages (DIR-819, DIR-859 and the others with
// Settings > Internet).
func dlinkIntl(p Protocol) Setup {
	return Setup{
		Protocol: p,
		Path:     "Settings > Internet",
		Fields: []Field{
			{Label: "My Internet Connection is", Value: wanName(p)},
			{Label: "Address Mode", Value: "Dynamic IP"},
			{Label: wanName(p) + " Server IP Address", Source: ServerIP},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Reconnect Mode", Value: "Always on"},
		},
		Notes: []Text{noteBehindModem},
	}
}

// Phones and computers ─────────────────────────────────────────────────────

func wgApp(path string, fields ...Field) Setup {
	return Setup{Protocol: WireGuard, Path: path, Fields: fields, Notes: []Text{noteWGApp}}
}

func ovpnApp(path string) Setup {
	return Setup{
		Protocol: OpenVPN,
		Path:     path,
		Fields: []Field{
			{Label: "File", Source: File},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
		Notes: []Text{noteImportFile},
	}
}

// The catalogue. Phones and computers first, then routers and modems, and
// last the models with no client.
var catalogue = []Guide{
	{
		ID: "windows", Name: en("Windows 10 / 11"), Kind: Device,
		Setups: []Setup{
			wgApp("WireGuard app > Add Tunnel > Import tunnel(s) from file", Field{Label: "File", Source: File}),
			ovpnApp("OpenVPN Connect > + > Upload File"),
			{
				Protocol: L2TP,
				Path:     "Settings > Network & internet > VPN > Add VPN",
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
				Protocol: IKEv2Password,
				Path:     "Settings > Network & internet > VPN > Add VPN",
				Fields: []Field{
					{Label: "VPN provider", Value: "Windows (built-in)"},
					{Label: "Connection name", Source: Anything},
					{Label: "Server name or address", Source: Server},
					{Label: "VPN type", Value: "IKEv2"},
					{Label: "Type of sign-in info", Value: "User name and password"},
					{Label: "User name", Source: Username},
					{Label: "Password", Source: Password},
				},
				Notes: []Text{Text{
					EN: "First install the server's certificate, once, in a Command Prompt opened as administrator, from the folder you saved it to:",
					FA: "اول گواهی سرور را یک بار نصب کنید، در Command Prompt با دسترسی Administrator، از پوشه‌ای که ذخیره‌اش کرده‌اید:",
				}},
				Command: func(v Values) string {
					return `certutil -addstore Root "` + v.IKEv2.CAFile + `"`
				},
			},
			{
				Protocol: PPTP,
				Path:     "Settings > Network & internet > VPN > Add VPN",
				Fields: []Field{
					{Label: "VPN provider", Value: "Windows (built-in)"},
					{Label: "Connection name", Source: Anything},
					{Label: "Server name or address", Source: Server},
					{Label: "VPN type", Value: "Point to Point Tunneling Protocol (PPTP)"},
					{Label: "Type of sign-in info", Value: "User name and password"},
					{Label: "User name", Source: Username},
					{Label: "Password", Source: Password},
				},
			},
		},
	},
	{
		ID: "ios", Name: en("iPhone / iPad"), Kind: Device,
		Setups: []Setup{
			wgApp("WireGuard app > + > Create from file or archive", Field{Label: "File", Source: File}),
			ovpnApp("OpenVPN Connect > Import Profile > File"),
			{
				Protocol: IKEv2Password,
				Path:     "Settings > General > VPN & Device Management > VPN > Add VPN Configuration",
				Fields: []Field{
					{Label: "Type", Value: "IKEv2"},
					{Label: "Description", Source: Anything},
					{Label: "Server", Source: Server},
					{Label: "Remote ID", Source: RemoteID},
					{Label: "Local ID", Source: Empty},
					{Label: "User Authentication", Value: "Username"},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
				},
				Notes: []Text{Text{
					EN: "First install the server's certificate: open its file from this page, then Settings > Profile Downloaded > Install, and Settings > General > About > Certificate Trust Settings: turn it on.",
					FA: "اول گواهی سرور را نصب کنید: فایلش را از همین صفحه باز کنید، بعد تنظیمات > Profile Downloaded > Install، و تنظیمات > General > About > Certificate Trust Settings: روشنش کنید.",
				}},
			},
			{
				Protocol: IKEv2PSK,
				Path:     "Settings > General > VPN & Device Management > VPN > Add VPN Configuration",
				Fields: []Field{
					{Label: "Type", Value: "IKEv2"},
					{Label: "Description", Source: Anything},
					{Label: "Server", Source: Server},
					{Label: "Remote ID", Source: RemoteID},
					{Label: "Local ID", Source: Username},
					{Label: "User Authentication", Value: "None"},
					{Label: "Use Certificate", Value: "off"},
					{Label: "Secret", Source: Password},
				},
			},
			{
				Protocol: IKEv2RSA,
				Path:     "Settings > General > VPN & Device Management > VPN > Add VPN Configuration",
				Fields: []Field{
					{Label: "Type", Value: "IKEv2"},
					{Label: "Description", Source: Anything},
					{Label: "Server", Source: Server},
					{Label: "Remote ID", Source: RemoteID},
					{Label: "Local ID", Source: Username},
					{Label: "User Authentication", Value: "None"},
					{Label: "Use Certificate", Value: "on"},
					{Label: "Certificate", Source: CertFile},
				},
				Notes: []Text{Text{
					EN: "First install your certificate: open your .p12 file from this page, then Settings > Profile Downloaded > Install; it asks for your password.",
					FA: "اول گواهی خودتان را نصب کنید: فایل .p12 را از همین صفحه باز کنید، بعد تنظیمات > Profile Downloaded > Install؛ رمز خودتان را می‌خواهد.",
				}, Text{
					EN: "First install the server's certificate: open its file from this page, then Settings > Profile Downloaded > Install, and Settings > General > About > Certificate Trust Settings: turn it on.",
					FA: "اول گواهی سرور را نصب کنید: فایلش را از همین صفحه باز کنید، بعد تنظیمات > Profile Downloaded > Install، و تنظیمات > General > About > Certificate Trust Settings: روشنش کنید.",
				}},
			},
			{
				Protocol: L2TP,
				Path:     "Settings > General > VPN & Device Management > VPN > Add VPN Configuration",
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
		},
	},
	{
		ID: "macos", Name: en("macOS"), Kind: Device,
		Setups: []Setup{
			wgApp("WireGuard app > Import Tunnel(s) from File", Field{Label: "File", Source: File}),
			ovpnApp("OpenVPN Connect > Import Profile > File"),
			{
				Protocol: IKEv2Password,
				Path:     "System Settings > Network > ⋯ > Add VPN Configuration > IKEv2",
				Fields: []Field{
					{Label: "Display Name", Source: Anything},
					{Label: "Server Address", Source: Server},
					{Label: "Remote ID", Source: RemoteID},
					{Label: "Local ID", Source: Empty},
					{Label: "Authentication", Value: "Username"},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
				},
				Notes: []Text{Text{
					EN: "First add the server's certificate to Keychain Access (open its file from this page) and set it to Always Trust.",
					FA: "اول گواهی سرور را به Keychain Access اضافه کنید (فایلش را از همین صفحه باز کنید) و روی Always Trust بگذارید.",
				}, Text{
					EN: "Apple names the fields but not every choice under Authentication; the choice here may be worded a little differently on your Mac.",
					FA: "اپل نام فیلدها را آورده ولی همه‌ی گزینه‌های Authentication را نه؛ ممکن است گزینه روی مک شما کمی متفاوت نوشته شده باشد.",
				}},
			},
			{
				Protocol: IKEv2PSK,
				Path:     "System Settings > Network > ⋯ > Add VPN Configuration > IKEv2",
				Fields: []Field{
					{Label: "Display Name", Source: Anything},
					{Label: "Server Address", Source: Server},
					{Label: "Remote ID", Source: RemoteID},
					{Label: "Local ID", Source: Username},
					{Label: "Authentication", Value: "Shared secret"},
					{Label: "Shared secret", Source: Password},
				},
				Notes: []Text{Text{
					EN: "Apple names the fields but not every choice under Authentication; the choice here may be worded a little differently on your Mac.",
					FA: "اپل نام فیلدها را آورده ولی همه‌ی گزینه‌های Authentication را نه؛ ممکن است گزینه روی مک شما کمی متفاوت نوشته شده باشد.",
				}},
			},
			{
				Protocol: IKEv2RSA,
				Path:     "System Settings > Network > ⋯ > Add VPN Configuration > IKEv2",
				Fields: []Field{
					{Label: "Display Name", Source: Anything},
					{Label: "Server Address", Source: Server},
					{Label: "Remote ID", Source: RemoteID},
					{Label: "Local ID", Source: Username},
					{Label: "Authentication", Value: "Certificate"},
					{Label: "Certificate", Source: CertFile},
				},
				Notes: []Text{Text{
					EN: "First add the server's certificate to Keychain Access (open its file from this page) and set it to Always Trust.",
					FA: "اول گواهی سرور را به Keychain Access اضافه کنید (فایلش را از همین صفحه باز کنید) و روی Always Trust بگذارید.",
				}, Text{
					EN: "Apple names the fields but not every choice under Authentication; the choice here may be worded a little differently on your Mac.",
					FA: "اپل نام فیلدها را آورده ولی همه‌ی گزینه‌های Authentication را نه؛ ممکن است گزینه روی مک شما کمی متفاوت نوشته شده باشد.",
				}},
			},
			{
				Protocol: L2TP,
				Path:     "System Settings > VPN > Add VPN Configuration > L2TP over IPSec",
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
		},
	},
	{
		ID: "android", Name: Text{EN: "Android 11 and older", FA: "اندروید ۱۱ و قدیمی‌تر"}, Kind: Device,
		Setups: []Setup{
			wgApp("WireGuard app > + > Import from file or archive", Field{Label: "File", Source: File}),
			ovpnApp("OpenVPN Connect > Import Profile > File"),
			{
				Protocol: L2TP,
				Path:     "Settings > Network & internet > VPN > +",
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
				Protocol: PPTP,
				Path:     "Settings > Network & internet > VPN > +",
				Fields: []Field{
					{Label: "Name", Source: Anything},
					{Label: "Type", Value: "PPTP"},
					{Label: "Server address", Source: Server},
					{Label: "PPP encryption (MPPE)", Value: "on"},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
				},
			},
		},
	},
	{
		ID: "android12", Name: Text{EN: "Android 12 and newer", FA: "اندروید ۱۲ و جدیدتر"}, Kind: Device,
		Notes: []Text{{
			EN: "Android 12 and newer have IKEv2 built in, and no L2TP or PPTP; WireGuard and OpenVPN work through their apps.",
			FA: "اندروید ۱۲ به بعد IKEv2 را داخل خودش دارد و L2TP و PPTP ندارد؛ وایرگارد و OpenVPN با اپ خودشان کار می‌کنند.",
		}},
		Setups: []Setup{
			wgApp("WireGuard app > + > Import from file or archive", Field{Label: "File", Source: File}),
			ovpnApp("OpenVPN Connect > Import Profile > File"),
			{
				Protocol: IKEv2Password,
				Path:     "Settings > Network & internet > VPN > +",
				Fields: []Field{
					{Label: "Name", Source: Anything},
					{Label: "Type", Value: "IKEv2/IPSec MSCHAPv2"},
					{Label: "Server address", Source: Server},
					{Label: "IPSec identifier", Source: Empty},
					{Label: "IPSec CA certificate", Source: CAFile},
					{Label: "IPSec server certificate", Value: "Received from server"},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
				},
				Notes: []Text{Text{
					EN: "First install the server's certificate: Settings > Security and privacy > More security settings > Install from device storage > CA certificate, and choose the file from this page.",
					FA: "اول گواهی سرور را نصب کنید: تنظیمات > امنیت و حریم خصوصی > تنظیمات امنیتی بیشتر > نصب از حافظه دستگاه > گواهی CA، و فایل را از همین صفحه انتخاب کنید.",
				}, Text{
					EN: "On a Samsung: Settings > Connections > More connection settings > VPN > ⋮ > Add VPN profile.",
					FA: "در سامسونگ: تنظیمات > اتصالات > تنظیمات بیشتر اتصال > VPN > ⋮ > افزودن نمایه VPN.",
				}},
			},
			{
				Protocol: IKEv2PSK,
				Path:     "Settings > Network & internet > VPN > +",
				Fields: []Field{
					{Label: "Name", Source: Anything},
					{Label: "Type", Value: "IKEv2/IPSec PSK"},
					{Label: "Server address", Source: Server},
					{Label: "IPSec identifier", Source: Username},
					{Label: "IPSec pre-shared key", Source: Password},
				},
				Notes: []Text{Text{
					EN: "On a Samsung: Settings > Connections > More connection settings > VPN > ⋮ > Add VPN profile.",
					FA: "در سامسونگ: تنظیمات > اتصالات > تنظیمات بیشتر اتصال > VPN > ⋮ > افزودن نمایه VPN.",
				}},
			},
			{
				Protocol: IKEv2RSA,
				Path:     "Settings > Network & internet > VPN > +",
				Fields: []Field{
					{Label: "Name", Source: Anything},
					{Label: "Type", Value: "IKEv2/IPSec RSA"},
					{Label: "Server address", Source: Server},
					{Label: "IPSec identifier", Source: Username},
					{Label: "IPSec user certificate", Source: CertFile},
					{Label: "IPSec CA certificate", Source: CAFile},
					{Label: "IPSec server certificate", Value: "Received from server"},
				},
				Notes: []Text{Text{
					EN: "First install your certificate: Settings > Security and privacy > More security settings > Install from device storage > VPN and app user certificate, and choose your .p12 file; it opens with your password.",
					FA: "اول گواهی خودتان را نصب کنید: تنظیمات > امنیت و حریم خصوصی > تنظیمات امنیتی بیشتر > نصب از حافظه دستگاه > گواهی کاربر VPN و برنامه، و فایل .p12 خود را انتخاب کنید؛ با رمز خودتان باز می‌شود.",
				}, Text{
					EN: "On a Samsung: Settings > Connections > More connection settings > VPN > ⋮ > Add VPN profile.",
					FA: "در سامسونگ: تنظیمات > اتصالات > تنظیمات بیشتر اتصال > VPN > ⋮ > افزودن نمایه VPN.",
				}},
			},
			{
				Protocol: IKEv2TLS,
				Path:     "Settings > Network & internet > VPN > +",
				Fields: []Field{
					{Label: "Name", Source: Anything},
					{Label: "Type", Value: "IKEv2/IPSec EAP-TLS"},
					{Label: "Server address", Source: Server},
					{Label: "IPSec identifier", Source: TLSID},
					{Label: "IPSec user certificate", Source: CertFile},
					{Label: "IPSec CA certificate", Source: CAFile},
					{Label: "IPSec server certificate", Value: "Received from server"},
				},
				Notes: []Text{Text{
					EN: "First install your certificate: Settings > Security and privacy > More security settings > Install from device storage > VPN and app user certificate, and choose your .p12 file; it opens with your password.",
					FA: "اول گواهی خودتان را نصب کنید: تنظیمات > امنیت و حریم خصوصی > تنظیمات امنیتی بیشتر > نصب از حافظه دستگاه > گواهی کاربر VPN و برنامه، و فایل .p12 خود را انتخاب کنید؛ با رمز خودتان باز می‌شود.",
				}, Text{
					EN: "On a Samsung: Settings > Connections > More connection settings > VPN > ⋮ > Add VPN profile.",
					FA: "در سامسونگ: تنظیمات > اتصالات > تنظیمات بیشتر اتصال > VPN > ⋮ > افزودن نمایه VPN.",
				}},
			},
		},
	},
	{
		ID: "linux", Name: en("Linux (NetworkManager)"), Kind: Device,
		Setups: []Setup{
			wgApp("Settings > Network > VPN > + > Import from file…", Field{Label: "File", Source: File}),
			{
				Protocol: OpenVPN,
				Path:     "Settings > Network > VPN > + > Import from file…",
				Fields: []Field{
					{Label: "File", Source: File},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
				},
				Notes: []Text{{
					EN: "Needs the network-manager-openvpn package (network-manager-openvpn-gnome on Ubuntu).",
					FA: "بسته network-manager-openvpn لازم است (در اوبونتو network-manager-openvpn-gnome).",
				}},
			},
			{
				Protocol: L2TP,
				Path:     "Settings > Network > VPN > + > Layer 2 Tunneling Protocol (L2TP)",
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
				Protocol: PPTP,
				Path:     "Settings > Network > VPN > + > Point-to-Point Tunneling Protocol (PPTP)",
				Fields: []Field{
					{Label: "Gateway", Source: Server},
					{Label: "User name", Source: Username},
					{Label: "Password", Source: Password},
					{Label: "Advanced: Use Point-to-Point encryption (MPPE)", Value: "on"},
				},
			},
		},
	},
	{
		ID: "chromeos", Name: en("ChromeOS"), Kind: Device,
		Setups: []Setup{
			wgApp("Play Store > WireGuard app > + > Import from file or archive", Field{Label: "File", Source: File}),
			{
				Protocol: L2TP,
				Path:     "Settings > Network > Add connection > Add built-in VPN",
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
		},
	},

	// ── routers and modems ───────────────────────────────────────────────
	{
		ID: "mikrotik", Name: en("MikroTik (RouterOS 7: hAP, hEX, RB, CCR)"), Kind: Router,
		Setups: []Setup{
			{
				Protocol: WireGuard,
				Path:     "Terminal (WinBox > New Terminal)",
				Fields: []Field{
					{Label: "Private Key", Source: WGPrivateKey},
					{Label: "Address", Source: WGAddress},
					{Label: "Peer Public Key", Source: WGPublicKey},
					{Label: "Preshared Key", Source: WGPresharedKey},
					{Label: "Endpoint", Source: Server},
					{Label: "Endpoint Port", Source: Port},
				},
				Notes: []Text{{
					EN: "Paste the commands; then route what should go through it to the wui-wg interface.",
					FA: "دستورها را وارد کنید؛ سپس ترافیکی را که باید از تانل برود به اینترفیس wui-wg مسیر بدهید.",
				}},
				Command: func(v Values) string {
					return fmt.Sprintf("/interface wireguard add name=wui-wg private-key=%s mtu=%d; /interface wireguard peers add interface=wui-wg public-key=%s preshared-key=%s endpoint-address=%s endpoint-port=%d allowed-address=0.0.0.0/0 persistent-keepalive=%ds; /ip address add address=%s interface=wui-wg",
						rosQuote(v.WG.PrivateKey), v.WG.MTU, rosQuote(v.WG.PublicKey), rosQuote(v.WG.PresharedKey), rosQuote(v.Server), v.Port, v.WG.Keepalive, v.WG.Address)
				},
				NoAmnezia: true,
			},
			{
				Protocol: L2TP,
				Path:     "Interfaces > + > L2TP Client > Dial Out (or paste the command in Terminal)",
				Fields: []Field{
					{Label: "Connect To", Source: Server},
					{Label: "User", Source: Username},
					{Label: "Password", Source: Password},
					{Label: "Use IPsec", Value: "yes"},
					{Label: "IPsec Secret", Source: PSK},
					{Label: "Allow", Value: "mschap2"},
				},
				Command: func(v Values) string {
					return fmt.Sprintf("/interface l2tp-client add name=wui-l2tp connect-to=%s user=%s password=%s use-ipsec=yes ipsec-secret=%s allow=mschap2 disabled=no",
						rosQuote(v.Server), rosQuote(v.Username), rosQuote(v.Password), rosQuote(v.PSK))
				},
			},
			{
				Protocol: L2TPPlain,
				Path:     "Interfaces > + > L2TP Client > Dial Out (or paste the command in Terminal)",
				Fields: []Field{
					{Label: "Connect To", Source: Server},
					{Label: "User", Source: Username},
					{Label: "Password", Source: Password},
					{Label: "Use IPsec", Value: "no"},
				},
				Command: func(v Values) string {
					return fmt.Sprintf("/interface l2tp-client add name=wui-l2tp connect-to=%s user=%s password=%s use-ipsec=no allow=mschap2,chap disabled=no",
						rosQuote(v.Server), rosQuote(v.Username), rosQuote(v.Password))
				},
			},
			{
				Protocol: PPTP,
				Path:     "Interfaces > + > PPTP Client > Dial Out (or paste the command in Terminal)",
				Fields: []Field{
					{Label: "Connect To", Source: Server},
					{Label: "User", Source: Username},
					{Label: "Password", Source: Password},
					{Label: "Allow", Value: "mschap2"},
				},
				Command: func(v Values) string {
					return fmt.Sprintf("/interface pptp-client add name=wui-pptp connect-to=%s user=%s password=%s allow=mschap2 profile=default-encryption disabled=no",
						rosQuote(v.Server), rosQuote(v.Username), rosQuote(v.Password))
				},
			},
		},
	},
	{
		ID: "keenetic", Name: en("Keenetic (Giga, Ultra, Viva, Hero, Runner 4G and others)"), Kind: Router,
		Setups: []Setup{
			{
				Protocol: L2TP,
				Path:     "Internet > Other connections > VPN connections > Create connection",
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
			{
				Protocol: PPTP,
				Path:     "Internet > Other connections > VPN connections > Create connection",
				Fields: []Field{
					{Label: "Type (protocol)", Value: "PPTP"},
					{Label: "Connection name", Source: Anything},
					{Label: "Server address", Source: Server},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
				},
			},
		},
	},
	vpnClientRouter("tplink-ax23", en("TP-Link Archer AX23 v2"), "Advanced > VPN Client > Server List > Add > Set up manually", wopl, noteKey32),
	vpnClientRouter("tplink-ax55pro", en("TP-Link Archer AX55 Pro"), tplinkVPNClientPath, wopl),
	vpnClientRouter("tplink-ax53", en("TP-Link Archer AX53 v2"), tplinkVPNClientPath, wopl),
	vpnClientRouter("tplink-ax10", en("TP-Link Archer AX10 v3 / AX1500 v3"), tplinkVPNClientPath, wopl),
	vpnClientRouter("tplink-ax55", en("TP-Link Archer AX55 v2"), tplinkVPNClientPath, opl),
	vpnClientRouter("tplink-ax73", en("TP-Link Archer AX73 v2"), tplinkVPNClientPath, opl),
	vpnClientRouter("tplink-ax21", en("TP-Link Archer AX21 v3"), tplinkVPNClientPath, opl),
	vpnClientRouter("mercusys-mr70x", en("Mercusys MR70X"), tplinkVPNClientPath, wopl),
	{
		ID: "omada", Name: en("TP-Link Omada ER605 / ER7206"), Kind: Router,
		Setups: []Setup{
			{
				Protocol: WireGuard,
				Path:     "VPN > WireGuard: add an interface on the WireGuard tab, then its peer on the Peers tab",
				Fields: []Field{
					{Label: "WireGuard: Name", Source: Anything},
					{Label: "WireGuard: MTU", Source: WGMTU},
					{Label: "WireGuard: Private Key", Source: WGPrivateKey},
					{Label: "WireGuard: Local IP Address", Source: WGAddress},
					{Label: "Peers: Public key", Source: WGPublicKey},
					{Label: "Peers: Endpoint", Source: ServerIP},
					{Label: "Peers: Endpoint Port", Source: Port},
					{Label: "Peers: Allowed Address", Source: WGAllowedIPs},
					{Label: "Peers: Preshared Key", Source: WGPresharedKey},
				},
				NoAmnezia: true,
			},
			{
				Protocol: OpenVPN,
				Path:     "VPN > OpenVPN > Client > Add",
				Fields: []Field{
					{Label: "Client Name", Source: Anything},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
					{Label: "Import", Source: File},
				},
				Notes: []Text{noteImportFile},
			},
			{
				Protocol: L2TP,
				Path:     "VPN > L2TP > L2TP Client > Add",
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
				Protocol: PPTP,
				Path:     "VPN > PPTP > PPTP Client > Add",
				Fields: []Field{
					{Label: "Tunnel", Source: Anything},
					{Label: "Account Name", Source: Username},
					{Label: "Password", Source: Password},
					{Label: "Server IP", Source: ServerIP},
					{Label: "MPPE Encryption", Value: "Encrypted"},
					{Label: "Working Mode", Value: "NAT"},
					{Label: "Status", Value: "Enable"},
				},
			},
		},
	},
	{
		ID: "dlink-842v2", Name: en("D-Link DIR-842 V2 (EU firmware)"), Kind: Router,
		Setups: []Setup{
			{
				Protocol: L2TP,
				Path:     "Settings > Internet > WAN > ADVANCED MODE > + (Add)",
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
			{
				Protocol: PPTP,
				Path:     "Settings > Internet > WAN > ADVANCED MODE > + (Add)",
				Fields: []Field{
					{Label: "Connection type", Value: "PPTP"},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
					{Label: "VPN server address", Source: Server},
					{Label: "Authentication protocol", Value: "MS-CHAPv2"},
					{Label: "Encryption protocol", Value: "MPPE 128 bit"},
				},
			},
		},
	},
	{
		ID: "draytek", Name: en("DrayTek Vigor (2915, 2927 and others)"), Kind: Router,
		Setups: []Setup{{
			Protocol: L2TP,
			Path:     "VPN and Remote Access >> LAN to LAN > an empty index",
			Fields: []Field{
				{Label: "Profile Name", Source: Anything},
				{Label: "Call Direction", Value: "Dial-Out"},
				{Label: "Dial-Out Settings: Type of Server I am calling", Value: "L2TP with IPsec Policy: Must"},
				{Label: "Server IP/Host Name for VPN", Source: Server},
				{Label: "Username", Source: Username},
				{Label: "Password", Source: Password},
				{Label: "IKE Pre-Shared Key", Source: PSK},
				{Label: "TCP/IP Network Settings: Change default route to this VPN tunnel", Value: "on"},
			},
			Notes: []Text{{
				EN: "Then VPN and Remote Access >> Connection Management > Dial.",
				FA: "سپس از VPN and Remote Access >> Connection Management گزینه Dial را بزنید.",
			}},
		}},
	},
	router("huawei", en("Huawei 4G/5G: B311B, B316, 4G CPE 3 (B535), 4G Router 2/3 Pro, 4G CPE 5, 5G CPE Pro"), Setup{
		Protocol: L2TPPlain,
		Path:     "Advanced > Router > VPN",
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
	}),
	router("tfi60", Text{EN: "Irancell TF-i60 (G1, B1, H1), GP-2101", FA: "ایرانسل TF-i60 (G1، B1، H1)، GP-2101"}, Setup{
		Protocol: L2TPPlain,
		Path:     "Settings > VPN",
		Fields: []Field{
			{Label: "VPN", Value: "Enable"},
			{Label: "Protocol", Value: "L2TP"},
			{Label: "VPN Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Tunnel Password", Source: TunnelPassword},
		},
		Notes: []Text{{EN: "TF-i60 E1 has no VPN settings.", FA: "مدل TF-i60 E1 تنظیمات VPN ندارد."}},
	}),
	router4G("tplink-mr600", en("TP-Link Archer MR600 v5 (4G+)"), WireGuard, OpenVPN, PPTP, L2TPPlain),
	router4G("tplink-mr6400", en("TP-Link TL-MR6400 v9 (4G)"), OpenVPN, PPTP, L2TPPlain),
	router4G("tplink-mr100", en("TP-Link TL-MR100 v5 (4G)"), OpenVPN, PPTP, L2TPPlain),
	router4G("tplink-mr200", en("TP-Link Archer MR200 v9 (4G)"), OpenVPN, PPTP, L2TPPlain),
	router4G("tplink-mr6500v", en("TP-Link TL-MR6500v v3 (4G)"), OpenVPN, PPTP, L2TPPlain),
	router("dlink-dwr921", en("D-Link DWR-921 (4G)"), dlinkAnweb(L2TPPlain, note4G), dlinkAnweb(PPTP, note4G)),
	{
		ID: "asus", Name: en("ASUS (RT-AX, RT-AC, RT-N, TUF, ROG)"), Kind: Router,
		Setups: []Setup{
			{
				Protocol: OpenVPN,
				Path:     "Advanced Settings > VPN > VPN Client (VPN Fusion) > Add profile > OpenVPN",
				Fields: []Field{
					{Label: "Description", Source: Anything},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
					{Label: "Import .ovpn file", Source: File},
				},
				Notes: []Text{noteImportFile, {EN: "Then click Activate.", FA: "سپس Activate را بزنید."}},
			},
			{
				Protocol: L2TPPlain,
				Path:     "Advanced Settings > VPN > VPN Client (VPN Fusion) > Add profile > L2TP",
				Fields: []Field{
					{Label: "Description", Source: Anything},
					{Label: "VPN Server", Source: Server},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
				},
				Notes: []Text{{EN: "Then click Activate.", FA: "سپس Activate را بزنید."}},
			},
			{
				Protocol: PPTP,
				Path:     "Advanced Settings > VPN > VPN Client (VPN Fusion) > Add profile > PPTP",
				Fields: []Field{
					{Label: "Description", Source: Anything},
					{Label: "VPN Server", Source: Server},
					{Label: "Username", Source: Username},
					{Label: "Password", Source: Password},
					{Label: "PPTP Options", Value: "MPPE 128"},
				},
				Notes: []Text{{EN: "Then click Activate.", FA: "سپس Activate را بزنید."}},
			},
		},
	},
	{
		ID: "xiaomi", Name: en("Xiaomi (Router 4A, 4C, AX3000T, AX3000 and others)"), Kind: Router,
		Setups: []Setup{xiaomi(L2TPPlain), xiaomi(PPTP)},
	},
	{
		ID: "openwrt", Name: en("OpenWrt"), Kind: Router,
		Setups: []Setup{
			{
				Protocol: WireGuard,
				Path:     "Network > Interfaces > Add new interface > Protocol: WireGuard VPN > Load configuration…",
				Fields:   []Field{{Label: "Load configuration…", Source: File}},
				Notes: []Text{{
					EN: "Needs the luci-proto-wireguard package; put the new interface in the wan firewall zone.",
					FA: "بسته luci-proto-wireguard لازم است؛ اینترفیس جدید را در زون فایروال wan بگذارید.",
				}},
				NoAmnezia: true,
			},
			{
				Protocol: L2TPPlain,
				Path:     "Network > Interfaces > Add new interface > Protocol: L2TP",
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
				Protocol: PPTP,
				Path:     "Network > Interfaces > Add new interface > Protocol: PPtP",
				Fields: []Field{
					{Label: "VPN Server", Source: Server},
					{Label: "PAP/CHAP username", Source: Username},
					{Label: "PAP/CHAP password", Source: Password},
				},
				Notes: []Text{{
					EN: "Needs the ppp-mod-pptp and luci-proto-ppp packages; put the new interface in the wan firewall zone.",
					FA: "بسته‌های ppp-mod-pptp و luci-proto-ppp لازم است؛ اینترفیس جدید را در زون فایروال wan بگذارید.",
				}},
			},
		},
	},
	{
		ID: "cisco-rv34x", Name: en("Cisco RV340 / RV345"), Kind: Router,
		Setups: []Setup{ciscoRV(L2TPPlain), ciscoRV(PPTP)},
	},
	internetRouter("tplink-c6", en("TP-Link Archer C6 v4")),
	internetRouter("tplink-c6v2", en("TP-Link Archer C6 v2")),
	internetRouter("tplink-c64", en("TP-Link Archer C64")),
	internetRouter("tplink-c80", en("TP-Link Archer C80")),
	internetRouter("tplink-c86", en("TP-Link Archer C86")),
	internetRouter("tplink-a6", en("TP-Link Archer A6 v4")),
	internetRouter("tplink-c24", en("TP-Link Archer C24 v2")),
	internetRouter("tplink-ax12", en("TP-Link Archer AX12")),
	internetRouter("tplink-ax20", en("TP-Link Archer AX20 v2")),
	internetRouter("tplink-ax72", en("TP-Link Archer AX72")),
	internetRouter("tplink-wr840n", en("TP-Link TL-WR840N v7")),
	internetRouter("tplink-wr841n", en("TP-Link TL-WR841N v14")),
	internetRouter("tplink-wr845n", en("TP-Link TL-WR845N v4")),
	classicRouter("tplink-wr840n-v6", en("TP-Link TL-WR840N v5 / v6")),
	classicRouter("tplink-c20", en("TP-Link Archer C20 v4 / v5 / v6")),
	classicRouter("tplink-c50", en("TP-Link Archer C50 v4 / v6")),
	router("tplink-wr940n", en("TP-Link TL-WR940N, and TL-WR841N up to v13"), tplinkUserRpm(L2TPPlain), tplinkUserRpm(PPTP)),
	router("tplink-vr600", en("TP-Link Archer VR600 v2"), tplinkEWAN(L2TPPlain), tplinkEWAN(PPTP)),
	router("tplink-w8970", en("TP-Link TD-W8970 v3"), tplinkEWAN(L2TPPlain), tplinkEWAN(PPTP)),
	internetRouter("mercusys-mw301r", en("Mercusys MW301R")),
	internetRouter("mercusys-mw302r", en("Mercusys MW302R")),
	internetRouter("mercusys-mw325r", en("Mercusys MW325R")),
	internetRouter("mercusys-ac12", en("Mercusys AC12")),
	internetRouter("mercusys-mr50g", en("Mercusys MR50G")),
	anwebRouter("dlink-842r1", en("D-Link DIR-842/R1"), noteBehindModem),
	anwebRouter("dlink-825r1", en("D-Link DIR-825/R1"), noteBehindModem),
	anwebRouter("dlink-615t4", en("D-Link DIR-615/T4"), noteBehindModem),
	router("dlink-intl", en("D-Link, international firmware (DIR-819, DIR-859 and others with Settings > Internet)"), dlinkIntl(L2TPPlain), dlinkIntl(PPTP)),

	// ── models with no VPN client ────────────────────────────────────────
	noClient("tplink-w9960", en("TP-Link TD-W9960 (VDSL/ADSL)")),
	noClient("tplink-w9970", en("TP-Link TD-W9970 (VDSL/ADSL)")),
	noClient("tplink-vr", en("TP-Link Archer VR300 / VR400 (VDSL/ADSL)")),
	noClient("adsl", Text{EN: "Other ADSL / VDSL modems (TD-W8961N, Utel and others)", FA: "مودم‌های دیگر ADSL / VDSL (TD-W8961N، یوتل و دیگران)"}),
	noClient("dlink-dsl", en("D-Link DSL-2750U / DSL-224")),
	noClient("zte-vdsl", en("ZTE ZXHN H108N / H168N / H267A (TCI, Shatel, Mobinnet modems)"), noteNotDocumented("ZTE")),
	noClient("fiber-ont", en("Fiber modems: Huawei HG8245H, ZTE F660 / F670"), noteNotDocumented("Huawei and ZTE")),
	noClient("zte-4g", en("ZTE 4G: MF286, MF283, MF297D, MF920"), noteNotDocumented("ZTE")),
	noClient("huawei-old", en("Huawei 4G: B310, B315, B525, B612, B618, E5577"), Text{
		EN: "Huawei does not list these among the models with a VPN client.",
		FA: "هوآوی این مدل‌ها را جزو مدل‌های دارای کلاینت VPN نام نمی‌برد.",
	}),
	noClient("tplink-extender", en("Wi-Fi extenders (TP-Link RE200, RE305, TL-WA850RE and others)"), Text{
		EN: "An extender only repeats the router's Wi-Fi: the router it extends is the one to set up.",
		FA: "تقویت‌کننده فقط وای‌فای روتر را تکرار می‌کند: باید خود روتری را که تقویت می‌کند تنظیم کنید.",
	}),
	noClient("tenda", en("Tenda AC6 v5, AC10 v3, N301 v4 and other current Tenda")),

	// ── found in the maker's own guide, without an emulator ──────────────
	router("cisco-rv160", en("Cisco RV160 / RV260"), ciscoRV160(L2TPPlain), ciscoRV160(PPTP)),
	router("dlink-x1560", en("D-Link DIR-X1560 / DIR-X1530 / DIR-X1860"), dlinkIntl(L2TPPlain), dlinkIntl(PPTP)),

	// ── not checked against the device: no manual or emulator found ──────
	router("netis", en("Netis WF2409E / WF2419 and other Netis"), uncheckedWAN("Advanced > Network > WAN", L2TPPlain), uncheckedWAN("Advanced > Network > WAN", PPTP)),
	router("totolink", en("TOTOLINK N300RT / A3002R and other TOTOLINK"), uncheckedWAN("Advanced Setup > Network > WAN", L2TPPlain), uncheckedWAN("Advanced Setup > Network > WAN", PPTP)),
	router("irancell-i40", Text{EN: "Irancell FD-i40 / TD-i40 (4G/TD-LTE)", FA: "ایرانسل FD-i40 / TD-i40 (4G/TD-LTE)"},
		withNotes(uncheckedWAN("VPN", L2TPPlain), note4G), withNotes(uncheckedWAN("VPN", PPTP), note4G)),
	router("irancell-m40", Text{EN: "Irancell FD-M40 (pocket 4G)", FA: "ایرانسل FD-M40 (جیبی 4G)"},
		withNotes(uncheckedWAN("VPN", L2TPPlain), note4G, noteMaybeNone), withNotes(uncheckedWAN("VPN", PPTP), note4G, noteMaybeNone)),
	{
		ID: "edgerouter", Name: en("Ubiquiti EdgeRouter X / 4 (EdgeOS)"), Kind: Router,
		Setups: []Setup{{
			Protocol: OpenVPN,
			Path:     "CLI (SSH), configure",
			Fields: []Field{
				{Label: "/config/auth/wui.ovpn", Source: File},
				{Label: "Username", Source: Username},
				{Label: "Password", Source: Password},
			},
			Notes: []Text{
				{
					EN: "Copy the file to /config/auth/wui.ovpn, and add a line to it: auth-user-pass /config/auth/wui.pass. That second file holds the username on its first line and the password on its second.",
					FA: "فایل را در /config/auth/wui.ovpn کپی کنید و این خط را به آن اضافه کنید: auth-user-pass /config/auth/wui.pass. فایل دوم در خط اول نام کاربری و در خط دوم رمز را دارد.",
				},
				noteUnchecked,
			},
			Command: func(Values) string {
				return "set interfaces openvpn vtun0 config-file /config/auth/wui.ovpn\ncommit ; save"
			},
		}},
	},
}

// The maker's pages for these were not found: the guide is the settings
// every such page asks for, and says so.
func uncheckedWAN(path string, p Protocol) Setup {
	return Setup{
		Protocol: p,
		Path:     path,
		Fields: []Field{
			{Label: "WAN / Connection Type", Value: wanName(p)},
			{Label: "Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
		Notes: []Text{noteBehindModem, noteUnchecked},
	}
}

// RV160 and RV260, from Cisco's own WAN guide: the same as the RV34x but
// for the tab, and PPTP's MPPE is a box of its own.
func ciscoRV160(p Protocol) Setup {
	f := []Field{
		{Label: "Connection Type", Value: wanName(p)},
		{Label: "IP Assignment", Value: "DHCP"},
		{Label: wanName(p) + " Server IP/FQDN", Source: Server},
		{Label: "Username", Source: Username},
		{Label: "Password", Source: Password},
		{Label: "Keep Alive", Value: "✓"},
	}
	notes := []Text{noteBehindModem}
	if p == PPTP {
		f = append(f, Field{Label: "MPPE Encryption", Value: "✓"})
		notes = append(notes, notePPTPMPPE)
	}
	return Setup{Protocol: p, Path: "WAN > WAN Settings > IPv4", Fields: f, Notes: notes}
}

func noteNotDocumented(maker string) Text {
	return Text{
		EN: maker + " documents no VPN client for these models, only VPN pass-through.",
		FA: "سازنده (" + maker + ") برای این مدل‌ها کلاینت VPN مستند نکرده، فقط عبور VPN (pass-through).",
	}
}

func xiaomi(p Protocol) Setup {
	return Setup{
		Protocol: p,
		Path:     "Additional settings > VPN > Add service",
		Fields: []Field{
			{Label: "Protocol", Value: wanName(p)},
			{Label: "Name", Source: Anything},
			{Label: "Server", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
		},
		Notes: []Text{{EN: "Save, then turn the service on.", FA: "ذخیره کنید و سرویس را روشن کنید."}},
	}
}

func ciscoRV(p Protocol) Setup {
	return Setup{
		Protocol: p,
		Path:     "WAN > WAN Settings > Add > IPv4",
		Fields: []Field{
			{Label: "Connection type", Value: wanName(p)},
			{Label: "IP Assignment", Value: "DHCP"},
			{Label: wanName(p) + " Server IP/FQDN", Source: Server},
			{Label: "Username", Source: Username},
			{Label: "Password", Source: Password},
			{Label: "Connect Mode", Value: "Keep Alive"},
			{Label: "Authentication Type", Value: map[Protocol]string{L2TPPlain: "Auto", PPTP: "MS-CHAPv2"}[p]},
		},
		Notes: []Text{noteBehindModem},
	}
}

// A PPTP page with no encryption field of its own is told what the server
// accepts: those that have one are filled in with it above.
func init() {
	for gi := range catalogue {
		for si := range catalogue[gi].Setups {
			s := &catalogue[gi].Setups[si]
			if s.Protocol != PPTP {
				continue
			}
			says := false
			for _, f := range s.Fields {
				if strings.Contains(f.Label, "MPPE") || strings.Contains(f.Label, "Encryption") || strings.Contains(f.Label, "Options") || strings.Contains(f.Label, "Authentication") {
					says = true
				}
			}
			if !says {
				s.Notes = append(append([]Text(nil), s.Notes...), notePPTPMPPE)
			}
		}
	}
}
