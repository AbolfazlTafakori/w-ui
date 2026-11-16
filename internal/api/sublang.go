package api

import (
	"sort"
	"strconv"
	"strings"
)

// The languages of a customer's page.
//
// The page speaks every language the panel does (internal/i18n's
// locales; a test holds the two lists together), so a seller who runs the
// panel in Russian hands out pages a Russian customer can read. The page
// opens in the language the customer's browser asks for first, when it has
// it, and English otherwise; the customer's own choice from the menu is
// kept in their browser and wins from then on.

// subLang is one language the page offers in its menu.
type subLang struct {
	Code string
	Flag string
	Name string
}

// subLangs are the page's languages, in the order the menu lists them.
var subLangs = []subLang{
	{"en", "🇬🇧", "English"},
	{"fa", "🇮🇷", "فارسی"},
	{"ru", "🇷🇺", "Русский"},
	{"tr", "🇹🇷", "Türkçe"},
	{"zh", "🇨🇳", "中文"},
}

// subLangOK reports whether the page speaks code.
func subLangOK(code string) bool {
	for _, l := range subLangs {
		if l.Code == code {
			return true
		}
	}
	return false
}

// pickSubLang is the language a browser's Accept-Language asks for first
// among the page's, by its quality values; English when it names none of
// them. Only the primary tag counts: "zh-CN" and "zh-TW" are both Chinese
// here, "fa-IR" Persian.
func pickSubLang(accept string) string {
	type choice struct {
		code string
		q    float64
		at   int
	}
	var all []choice
	for i, part := range strings.Split(accept, ",") {
		if i >= 32 {
			break // a header longer than any browser sends is not read further
		}
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		code := strings.ToLower(tag)
		if j := strings.IndexAny(code, "-_"); j >= 0 {
			code = code[:j]
		}
		if !subLangOK(code) {
			continue
		}
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
				q = f
			}
		}
		if q > 0 {
			all = append(all, choice{code, q, i})
		}
	}
	if len(all) == 0 {
		return "en"
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].q > all[b].q })
	return all[0].code
}

// Words new to the page in every language, and the page's words in the
// languages it gained after English and Persian. Merged into the page's
// dictionaries once, at start; a test checks every language has every word.
func init() {
	for lang, words := range subPageExtra {
		if subPageStrings[lang] == nil {
			subPageStrings[lang] = map[string]string{}
		}
		for k, v := range words {
			subPageStrings[lang][k] = v
		}
	}
	for lang, words := range subPageL2TPExtra {
		if subPageL2TPStrings[lang] == nil {
			subPageL2TPStrings[lang] = map[string]string{}
		}
		for k, v := range words {
			subPageL2TPStrings[lang][k] = v
		}
	}
}

var subPageExtra = map[string]map[string]string{
	"en": {
		"sharedTag":      "shared",
		"sharedNote":     "The total and the remaining are for all users of this plan together: what one user spends comes off everyone's remaining.",
		"sharedNoteUser": "This plan's volume is shared by all its users: the total and the remaining are for everyone together, not for you alone. What one user spends comes off everyone's remaining.",
	},
	"fa": {
		"sharedTag":      "مشترک",
		"sharedNote":     "حجم کل و باقی‌مانده برای همهٔ کاربران این پلن با هم است: هر چه یک کاربر مصرف کند از باقی‌ماندهٔ همه کم می‌شود.",
		"sharedNoteUser": "حجم این پلن بین همهٔ کاربرانش مشترک است: حجم کل و باقی‌مانده برای همه با هم است، نه فقط برای شما. هر چه یک کاربر مصرف کند از باقی‌ماندهٔ همه کم می‌شود.",
	},
	"ru": {
		"title": "Подписка", "subId": "ID подписки", "email": "Имя", "status": "Статус",
		"active": "Активна", "inactive": "Неактивна", "unlimited": "Безлимит",
		"downloaded": "Скачано", "uploaded": "Отправлено", "usage": "Использовано", "totalQuota": "Всего",
		"remained": "Осталось", "lastOnline": "Последний вход", "expiry": "Срок", "noExpiry": "Бессрочно",
		"expired": "Истекла", "copy": "Копировать", "copied": "Скопировано", "download": "Скачать",
		"copyLink": "Ссылки", "copyAll": "Копировать все конфиги", "copyAllDone": "Все конфиги скопированы",
		"config": "Конфиг WireGuard", "ovpnConfig": "Конфиг OpenVPN", "xrayConfig": "Ссылка V2Ray", "theme": "Тема", "language": "Язык",
		"users": "польз.", "user": "Пользователь", "show": "Показать", "oneUser": "1 пользователь",
		"usageTable": "Трафик по пользователям и туннелям", "usageSince": "По пользователям с", "total": "Итого", "allUsers": "Все пользователи", "tunnel": "Туннель",
		"live": "Сейчас", "online": "Онлайн", "idle": "Простой", "offline": "Офлайн",
		"subSettings": "Подписка", "tapToClose": "Нажмите вне окна, чтобы закрыть", "amneziaKeyHint": "Для приложения AmneziaVPN: скопируйте ключ или отсканируйте его.",
		"sharedTag":      "общий",
		"sharedNote":     "Общий объём и остаток — на всех пользователей тарифа вместе: то, что тратит один, уходит из общего остатка.",
		"sharedNoteUser": "Объём этого тарифа общий для всех его пользователей: общий объём и остаток — на всех вместе, а не только на вас. То, что тратит один, уходит из общего остатка.",
	},
	"tr": {
		"title": "Abonelik bilgisi", "subId": "Abonelik kimliği", "email": "Ad", "status": "Durum",
		"active": "Etkin", "inactive": "Etkin değil", "unlimited": "Sınırsız",
		"downloaded": "İndirilen", "uploaded": "Yüklenen", "usage": "Kullanım", "totalQuota": "Toplam kota",
		"remained": "Kalan", "lastOnline": "Son çevrimiçi", "expiry": "Bitiş", "noExpiry": "Süresiz",
		"expired": "Süresi doldu", "copy": "Kopyala", "copied": "Kopyalandı", "download": "İndir",
		"copyLink": "Bağlantılar", "copyAll": "Tüm yapılandırmaları kopyala", "copyAllDone": "Tüm yapılandırmalar kopyalandı",
		"config": "WireGuard yapılandırması", "ovpnConfig": "OpenVPN yapılandırması", "xrayConfig": "V2Ray bağlantısı", "theme": "Tema", "language": "Dil",
		"users": "kullanıcı", "user": "Kullanıcı", "show": "Göster", "oneUser": "1 kullanıcı",
		"usageTable": "Kullanıcı ve tünele göre kullanım", "usageSince": "Kullanıcıya göre ayrım başlangıcı", "total": "Toplam", "allUsers": "Tüm kullanıcılar", "tunnel": "Tünel",
		"live": "Canlı", "online": "Çevrimiçi", "idle": "Boşta", "offline": "Kapalı",
		"subSettings": "Abonelik", "tapToClose": "Kapatmak için dışarıya dokunun", "amneziaKeyHint": "AmneziaVPN uygulaması için: anahtarı kopyalayın ya da tarayın.",
		"sharedTag":      "ortak",
		"sharedNote":     "Toplam kota ve kalan, bu planın tüm kullanıcıları içindir: bir kullanıcının harcadığı herkesin kalanından düşer.",
		"sharedNoteUser": "Bu planın kotası tüm kullanıcıları arasında ortaktır: toplam ve kalan yalnızca sizin değil, herkesin birlikte. Bir kullanıcının harcadığı herkesin kalanından düşer.",
	},
	"zh": {
		"title": "订阅信息", "subId": "订阅 ID", "email": "名称", "status": "状态",
		"active": "有效", "inactive": "无效", "unlimited": "不限量",
		"downloaded": "已下载", "uploaded": "已上传", "usage": "已用", "totalQuota": "总流量",
		"remained": "剩余", "lastOnline": "最后在线", "expiry": "到期", "noExpiry": "永不到期",
		"expired": "已到期", "copy": "复制", "copied": "已复制", "download": "下载",
		"copyLink": "复制链接", "copyAll": "复制全部配置", "copyAllDone": "已复制全部配置",
		"config": "WireGuard 配置", "ovpnConfig": "OpenVPN 配置", "xrayConfig": "V2Ray 链接", "theme": "主题", "language": "语言",
		"users": "个用户", "user": "用户", "show": "显示", "oneUser": "1 个用户",
		"usageTable": "按用户和隧道统计用量", "usageSince": "按用户统计起始于", "total": "合计", "allUsers": "全部用户", "tunnel": "隧道",
		"live": "实时", "online": "在线", "idle": "空闲", "offline": "离线",
		"subSettings": "订阅", "tapToClose": "点击外部关闭", "amneziaKeyHint": "用于 AmneziaVPN 应用：复制密钥或扫码。",
		"sharedTag":      "共享",
		"sharedNote":     "总流量和剩余流量属于本套餐的全部用户：任何一个用户的用量都会从大家的剩余中扣除。",
		"sharedNoteUser": "本套餐的流量由全部用户共享：总流量和剩余流量属于所有人，而不只是您。任何一个用户的用量都会从大家的剩余中扣除。",
	},
}

var subPageL2TPExtra = map[string]map[string]string{
	"ru": {"l2tpConfig": "Вход L2TP/IPsec", "pptpConfig": "Вход PPTP", "ikev2Config": "Вход IKEv2", "ocConfig": "Вход OpenConnect (Cisco)", "l2tpBuiltIn": "L2TP/IPsec: встроен, в Настройках → VPN",
		"revealExpired": "Страница открыта давно: обновите её и скопируйте снова.",
		"revealBusy":    "Слишком много копирований подряд: подождите минуту.",
		"revealFailed":  "Не удалось скопировать; попробуйте ещё раз.",
		"siServer":      "Сервер", "siServerAddress": "Адрес сервера", "siRemoteID": "Remote ID",
		"siOwnPort": "Порт (для приложений с настройкой порта)", "siPSK": "Общий ключ (PSK)",
		"siUsername": "Имя пользователя", "siPassword": "Пароль"},
	"tr": {"l2tpConfig": "L2TP/IPsec girişi", "pptpConfig": "PPTP girişi", "ikev2Config": "IKEv2 girişi", "ocConfig": "OpenConnect (Cisco) girişi", "l2tpBuiltIn": "L2TP/IPsec: cihazda yerleşik, Ayarlar → VPN",
		"revealExpired": "Bu sayfa bir süredir açık: yenileyin, sonra yeniden kopyalayın.",
		"revealBusy":    "Art arda çok fazla kopyalama: bir dakika bekleyin.",
		"revealFailed":  "Kopyalanamadı; yeniden deneyin.",
		"siServer":      "Sunucu", "siServerAddress": "Sunucu adresi", "siRemoteID": "Remote ID",
		"siOwnPort": "Port (port ayarı olan uygulamalar için)", "siPSK": "Ön paylaşımlı anahtar",
		"siUsername": "Kullanıcı adı", "siPassword": "Parola"},
	"zh": {"l2tpConfig": "L2TP/IPsec 登录", "pptpConfig": "PPTP 登录", "ikev2Config": "IKEv2 登录", "ocConfig": "OpenConnect (Cisco) 登录", "l2tpBuiltIn": "L2TP/IPsec：系统内置，位于 设置 → VPN",
		"revealExpired": "此页面已打开较久：请刷新后再复制。",
		"revealBusy":    "连续复制次数过多：请等待一分钟。",
		"revealFailed":  "复制失败，请重试。",
		"siServer":      "服务器", "siServerAddress": "服务器地址", "siRemoteID": "Remote ID",
		"siOwnPort": "端口（适用于可设置端口的应用）", "siPSK": "预共享密钥",
		"siUsername": "用户名", "siPassword": "密码"},
}
