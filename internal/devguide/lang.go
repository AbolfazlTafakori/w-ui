package devguide

// The guides in the languages beyond English and Persian.
//
// A Text carries English and Persian beside each other, where they are
// written. The other languages the customer's page speaks are kept here,
// by the English they translate, so the catalogue stays readable and a
// note shared by forty devices is translated once. A device or brand name
// that reads the same in every language has no entry and is shown as it
// is; anything else without an entry fails TestEveryTextIsInEveryLanguage.

// Langs are the languages Strings answers in, English first.
var Langs = []string{"en", "fa", "ru", "tr", "zh"}

// more are the Russian, Turkish and Chinese of each English text.
var more = map[string][3]string{
	"After APPLY, choose \"to the Internet\" and the connection the router already has.": {
		"После APPLY выберите «to the Internet» и подключение, которое у роутера уже есть.",
		"APPLY'dan sonra \"to the Internet\" seçeneğini ve yönlendiricinin zaten sahip olduğu bağlantıyı seçin.",
		"点击 APPLY 后，选择“to the Internet”以及路由器已有的那个连接。"},
	"An extender only repeats the router's Wi-Fi: the router it extends is the one to set up.": {
		"Усилитель лишь повторяет Wi-Fi роутера: настраивать нужно сам роутер.",
		"Menzil genişletici yalnızca yönlendiricinin Wi-Fi'ını tekrarlar: kurulacak olan, genişlettiği yönlendiricidir.",
		"扩展器只是转发路由器的 Wi-Fi：需要设置的是它所扩展的那台路由器。"},
	"Android 11 and older": {"Android 11 и старше", "Android 11 ve öncesi", "Android 11 及更早版本"},
	"Android 12 and newer": {"Android 12 и новее", "Android 12 ve sonrası", "Android 12 及更新版本"},
	"Android 12 and newer have IKEv2 built in, and no L2TP or PPTP; WireGuard and OpenVPN work through their apps.": {
		"В Android 12 и новее встроен IKEv2, а L2TP и PPTP нет; WireGuard и OpenVPN работают через свои приложения.",
		"Android 12 ve sonrasında IKEv2 yerleşiktir, L2TP ve PPTP yoktur; WireGuard ve OpenVPN kendi uygulamalarıyla çalışır.",
		"Android 12 及更新版本内置 IKEv2，没有 L2TP 和 PPTP；WireGuard 和 OpenVPN 通过各自的应用使用。"},
	"Apple names the fields but not every choice under Authentication; the choice here may be worded a little differently on your Mac.": {
		"Apple описывает поля, но не все варианты в разделе «Аутентификация»; на вашем Mac этот вариант может называться немного иначе.",
		"Apple alanları adlandırır ama Kimlik Doğrulama altındaki her seçeneği değil; buradaki seçenek Mac'inizde biraz farklı yazılmış olabilir.",
		"Apple 说明了各字段，但没有列出“认证”下的所有选项；此处的选项在您的 Mac 上措辞可能略有不同。"},
	"Connection": {"Подключение", "Bağlantı", "连接方式"},
	"Copy the file to /config/auth/wui.ovpn, and add a line to it: auth-user-pass /config/auth/wui.pass. That second file holds the username on its first line and the password on its second.": {
		"Скопируйте файл в /config/auth/wui.ovpn и добавьте в него строку: auth-user-pass /config/auth/wui.pass. Во втором файле в первой строке — имя пользователя, во второй — пароль.",
		"Dosyayı /config/auth/wui.ovpn konumuna kopyalayın ve içine şu satırı ekleyin: auth-user-pass /config/auth/wui.pass. Bu ikinci dosyanın ilk satırında kullanıcı adı, ikinci satırında parola bulunur.",
		"将文件复制到 /config/auth/wui.ovpn，并在其中添加一行：auth-user-pass /config/auth/wui.pass。第二个文件第一行是用户名，第二行是密码。"},
	"Download the file from this page first, then choose it here.": {
		"Сначала скачайте файл с этой страницы, затем выберите его здесь.",
		"Önce dosyayı bu sayfadan indirin, sonra burada seçin.",
		"先从本页下载文件，然后在此处选择它。"},
	"Error 809: run this once in a Command Prompt opened as administrator, then restart:": {
		"Ошибка 809: выполните это один раз в командной строке, открытой от имени администратора, затем перезагрузите компьютер:",
		"Hata 809: bunu yönetici olarak açılmış bir Komut İstemi'nde bir kez çalıştırın, sonra yeniden başlatın:",
		"错误 809：在以管理员身份打开的命令提示符中运行一次以下命令，然后重启："},
	"First add the server's certificate to Keychain Access (open its file from this page) and set it to Always Trust.": {
		"Сначала добавьте сертификат сервера в «Связку ключей» (откройте его файл с этой страницы) и выберите «Всегда доверять».",
		"Önce sunucunun sertifikasını Anahtar Zinciri Erişimi'ne ekleyin (dosyasını bu sayfadan açın) ve Her Zaman Güven olarak ayarlayın.",
		"先将服务器证书添加到“钥匙串访问”（从本页打开其文件），并设为“始终信任”。"},
	"First install the server's certificate, once, in a Command Prompt opened as administrator, from the folder you saved it to:": {
		"Сначала один раз установите сертификат сервера в командной строке, открытой от имени администратора, из папки, куда вы его сохранили:",
		"Önce sunucunun sertifikasını, kaydettiğiniz klasörden, yönetici olarak açılmış bir Komut İstemi'nde bir kez yükleyin:",
		"先在以管理员身份打开的命令提示符中，从保存证书的文件夹里安装一次服务器证书："},
	"First install the server's certificate: Settings > Security and privacy > More security settings > Install from device storage > CA certificate, and choose the file from this page.": {
		"Сначала установите сертификат сервера: Настройки > Безопасность и конфиденциальность > Другие настройки безопасности > Установить из памяти устройства > Сертификат ЦС, и выберите файл с этой страницы.",
		"Önce sunucunun sertifikasını yükleyin: Ayarlar > Güvenlik ve gizlilik > Diğer güvenlik ayarları > Cihaz depolamasından yükle > CA sertifikası, ve bu sayfadaki dosyayı seçin.",
		"先安装服务器证书：设置 > 安全和隐私 > 更多安全设置 > 从设备存储空间安装 > CA 证书，然后选择本页的文件。"},
	"First install the server's certificate: open its file from this page, then Settings > Profile Downloaded > Install, and Settings > General > About > Certificate Trust Settings: turn it on.": {
		"Сначала установите сертификат сервера: откройте его файл с этой страницы, затем Настройки > Профиль загружен > Установить, и Настройки > Основные > Об этом устройстве > Доверие сертификатам: включите его.",
		"Önce sunucunun sertifikasını yükleyin: dosyasını bu sayfadan açın, sonra Ayarlar > Profil İndirildi > Yükle, ve Ayarlar > Genel > Hakkında > Sertifika Güven Ayarları: açın.",
		"先安装服务器证书：从本页打开其文件，然后 设置 > 已下载描述文件 > 安装，再到 设置 > 通用 > 关于本机 > 证书信任设置：将其开启。"},
	"First install your certificate: Settings > Security and privacy > More security settings > Install from device storage > VPN and app user certificate, and choose your .p12 file; it opens with your password.": {
		"Сначала установите свой сертификат: Настройки > Безопасность и конфиденциальность > Другие настройки безопасности > Установить из памяти устройства > Сертификат пользователя VPN и приложений, и выберите свой файл .p12; он открывается вашим паролем.",
		"Önce kendi sertifikanızı yükleyin: Ayarlar > Güvenlik ve gizlilik > Diğer güvenlik ayarları > Cihaz depolamasından yükle > VPN ve uygulama kullanıcı sertifikası, ve .p12 dosyanızı seçin; parolanızla açılır.",
		"先安装您的证书：设置 > 安全和隐私 > 更多安全设置 > 从设备存储空间安装 > VPN 和应用用户证书，然后选择您的 .p12 文件；用您的密码打开。"},
	"First install your certificate: open your .p12 file from this page, then Settings > Profile Downloaded > Install; it asks for your password.": {
		"Сначала установите свой сертификат: откройте файл .p12 с этой страницы, затем Настройки > Профиль загружен > Установить; будет запрошен ваш пароль.",
		"Önce kendi sertifikanızı yükleyin: .p12 dosyanızı bu sayfadan açın, sonra Ayarlar > Profil İndirildi > Yükle; parolanız sorulur.",
		"先安装您的证书：从本页打开您的 .p12 文件，然后 设置 > 已下载描述文件 > 安装；系统会要求输入您的密码。"},
	"Huawei and ZTE documents no VPN client for these models, only VPN pass-through.": {
		"Huawei и ZTE не указывают для этих моделей VPN-клиента, только пропуск VPN (pass-through).",
		"Huawei ve ZTE bu modeller için VPN istemcisi belirtmez, yalnızca VPN geçişi (pass-through).",
		"华为和中兴的文档中，这些型号没有 VPN 客户端，只有 VPN 穿透。"},
	"Huawei does not list these among the models with a VPN client.": {
		"Huawei не относит эти модели к моделям с VPN-клиентом.",
		"Huawei bu modelleri VPN istemcisi olan modeller arasında saymaz.",
		"华为未将这些型号列入带 VPN 客户端的型号。"},
	"IKEv2 · certificate (RSA)":            {"IKEv2 · сертификат (RSA)", "IKEv2 · sertifika (RSA)", "IKEv2 · 证书 (RSA)"},
	"IKEv2 · certificate in EAP (EAP-TLS)": {"IKEv2 · сертификат в EAP (EAP-TLS)", "IKEv2 · EAP içinde sertifika (EAP-TLS)", "IKEv2 · EAP 证书 (EAP-TLS)"},
	"IKEv2 · key (PSK)":                    {"IKEv2 · ключ (PSK)", "IKEv2 · anahtar (PSK)", "IKEv2 · 密钥 (PSK)"},
	"IKEv2 · username and password":        {"IKEv2 · имя пользователя и пароль", "IKEv2 · kullanıcı adı ve parola", "IKEv2 · 用户名和密码"},
	"If it does not answer, use instead":   {"Если не отвечает, используйте вместо него", "Yanıt vermezse bunun yerine kullanın", "如无响应，请改用"},
	"If it says the server is untrusted: Settings > Block Untrusted Servers off, then connect again. The server's certificate is its own, not one a public authority signed.": {
		"Если пишет, что сервер не доверенный: Настройки > Block Untrusted Servers — выключить, затем подключитесь снова. Сертификат сервера собственный, а не подписанный публичным центром.",
		"Sunucunun güvenilmez olduğunu söylerse: Ayarlar > Block Untrusted Servers kapalı, sonra yeniden bağlanın. Sunucunun sertifikası kendisine aittir, genel bir otoritenin imzaladığı değil.",
		"如果提示服务器不受信任：设置 > 关闭 Block Untrusted Servers，然后重新连接。服务器证书是自签的，而非公共机构签发。"},
	"If it says the server is untrusted: the gear > Preferences > clear Block connections to untrusted servers, then connect again. The server's certificate is its own, not one a public authority signed.": {
		"Если пишет, что сервер не доверенный: шестерёнка > Preferences > снимите Block connections to untrusted servers, затем подключитесь снова. Сертификат сервера собственный, а не подписанный публичным центром.",
		"Sunucunun güvenilmez olduğunu söylerse: dişli > Preferences > Block connections to untrusted servers işaretini kaldırın, sonra yeniden bağlanın. Sunucunun sertifikası kendisine aittir, genel bir otoritenin imzaladığı değil.",
		"如果提示服务器不受信任：齿轮 > Preferences > 取消勾选 Block connections to untrusted servers，然后重新连接。服务器证书是自签的，而非公共机构签发。"},
	"If its settings have no VPN section, this model has no VPN client: set up each device itself.": {
		"Если в настройках нет раздела VPN, у этой модели нет VPN-клиента: настройте каждое устройство отдельно.",
		"Ayarlarında VPN bölümü yoksa bu modelde VPN istemcisi yoktur: her cihazı ayrı ayrı kurun.",
		"如果设置中没有 VPN 部分，说明该型号没有 VPN 客户端：请在每台设备上分别设置。"},
	"Irancell FD-M40 (pocket 4G)":           {"Irancell FD-M40 (карманный 4G)", "Irancell FD-M40 (cep 4G)", "Irancell FD-M40（随身 4G）"},
	"Irancell FD-i40 / TD-i40 (4G/TD-LTE)":  {"Irancell FD-i40 / TD-i40 (4G/TD-LTE)", "Irancell FD-i40 / TD-i40 (4G/TD-LTE)", "Irancell FD-i40 / TD-i40 (4G/TD-LTE)"},
	"Irancell TF-i60 (G1, B1, H1), GP-2101": {"Irancell TF-i60 (G1, B1, H1), GP-2101", "Irancell TF-i60 (G1, B1, H1), GP-2101", "Irancell TF-i60 (G1, B1, H1), GP-2101"},
	"It runs over the SIM card's connection; nothing else is needed in front of it.": {
		"Работает через подключение SIM-карты; ничего больше перед ним не нужно.",
		"SIM kartın bağlantısı üzerinden çalışır; önünde başka bir şeye gerek yoktur.",
		"它通过 SIM 卡的网络工作；前面无需其他设备。"},
	"L2TP without IPsec (not encrypted)": {"L2TP без IPsec (без шифрования)", "IPsec'siz L2TP (şifresiz)", "不带 IPsec 的 L2TP（不加密）"},
	"Models with no VPN client":          {"Модели без VPN-клиента", "VPN istemcisi olmayan modeller", "没有 VPN 客户端的型号"},
	"Needs the luci-proto-openconnect package; put the new interface in the wan firewall zone. The hash field takes the pin as it is.": {
		"Нужен пакет luci-proto-openconnect; поместите новый интерфейс в зону брандмауэра wan. Поле hash принимает pin как есть.",
		"luci-proto-openconnect paketi gerekir; yeni arayüzü wan güvenlik duvarı bölgesine koyun. hash alanı pin değerini olduğu gibi alır.",
		"需要 luci-proto-openconnect 软件包；将新接口放入 wan 防火墙区域。hash 字段直接填入 pin 值。"},
	"Needs the luci-proto-wireguard package; put the new interface in the wan firewall zone.": {
		"Нужен пакет luci-proto-wireguard; поместите новый интерфейс в зону брандмауэра wan.",
		"luci-proto-wireguard paketi gerekir; yeni arayüzü wan güvenlik duvarı bölgesine koyun.",
		"需要 luci-proto-wireguard 软件包；将新接口放入 wan 防火墙区域。"},
	"Needs the network-manager-l2tp package (network-manager-l2tp-gnome on Ubuntu).": {
		"Нужен пакет network-manager-l2tp (network-manager-l2tp-gnome в Ubuntu).",
		"network-manager-l2tp paketi gerekir (Ubuntu'da network-manager-l2tp-gnome).",
		"需要 network-manager-l2tp 软件包（Ubuntu 上为 network-manager-l2tp-gnome）。"},
	"Needs the network-manager-openconnect-gnome package. The username and password are asked for when it connects, and the first time, whether to accept the server's certificate. Or run:": {
		"Нужен пакет network-manager-openconnect-gnome. Имя пользователя и пароль запрашиваются при подключении, а в первый раз — ещё и принять ли сертификат сервера. Или выполните:",
		"network-manager-openconnect-gnome paketi gerekir. Kullanıcı adı ve parola bağlanırken sorulur; ilk seferde sunucunun sertifikasının kabul edilip edilmeyeceği de sorulur. Ya da şunu çalıştırın:",
		"需要 network-manager-openconnect-gnome 软件包。连接时会询问用户名和密码，首次连接还会询问是否接受服务器证书。或者运行："},
	"Needs the network-manager-openvpn package (network-manager-openvpn-gnome on Ubuntu).": {
		"Нужен пакет network-manager-openvpn (network-manager-openvpn-gnome в Ubuntu).",
		"network-manager-openvpn paketi gerekir (Ubuntu'da network-manager-openvpn-gnome).",
		"需要 network-manager-openvpn 软件包（Ubuntu 上为 network-manager-openvpn-gnome）。"},
	"Needs the ppp-mod-pptp and luci-proto-ppp packages; put the new interface in the wan firewall zone.": {
		"Нужны пакеты ppp-mod-pptp и luci-proto-ppp; поместите новый интерфейс в зону брандмауэра wan.",
		"ppp-mod-pptp ve luci-proto-ppp paketleri gerekir; yeni arayüzü wan güvenlik duvarı bölgesine koyun.",
		"需要 ppp-mod-pptp 和 luci-proto-ppp 软件包；将新接口放入 wan 防火墙区域。"},
	"Needs the system component \"IKEv1/IPsec and IKEv2/IPsec VPN servers, L2TP/IPsec VPN client, Site-to-site IPsec VPN\" (General system settings > Component options).": {
		"Нужен системный компонент «IKEv1/IPsec and IKEv2/IPsec VPN servers, L2TP/IPsec VPN client, Site-to-site IPsec VPN» (Общие настройки системы > Параметры компонентов).",
		"\"IKEv1/IPsec and IKEv2/IPsec VPN servers, L2TP/IPsec VPN client, Site-to-site IPsec VPN\" sistem bileşeni gerekir (Genel sistem ayarları > Bileşen seçenekleri).",
		"需要系统组件“IKEv1/IPsec and IKEv2/IPsec VPN servers, L2TP/IPsec VPN client, Site-to-site IPsec VPN”（常规系统设置 > 组件选项）。"},
	"Needs the xl2tpd and ppp-mod-pppol2tp packages; put the new interface in the wan firewall zone. Or run:": {
		"Нужны пакеты xl2tpd и ppp-mod-pppol2tp; поместите новый интерфейс в зону брандмауэра wan. Или выполните:",
		"xl2tpd ve ppp-mod-pppol2tp paketleri gerekir; yeni arayüzü wan güvenlik duvarı bölgesine koyun. Ya da şunu çalıştırın:",
		"需要 xl2tpd 和 ppp-mod-pppol2tp 软件包；将新接口放入 wan 防火墙区域。或者运行："},
	"No device matches; the full list is below.": {"Устройство не найдено; полный список ниже.", "Eşleşen cihaz yok; tam liste aşağıda.", "没有匹配的设备；完整列表在下方。"},
	"Not checked against this model's own pages: the names here may differ a little from what it shows.": {
		"Не сверено с интерфейсом самой модели: названия здесь могут немного отличаться от того, что она показывает.",
		"Bu modelin kendi sayfalarıyla karşılaştırılmadı: buradaki adlar gösterdiklerinden biraz farklı olabilir.",
		"未与该型号自己的页面核对：此处的名称可能与其显示的略有不同。"},
	"On a Samsung: Settings > Connections > More connection settings > VPN > ⋮ > Add VPN profile.": {
		"На Samsung: Настройки > Подключения > Другие настройки > VPN > ⋮ > Добавить профиль VPN.",
		"Samsung'da: Ayarlar > Bağlantılar > Diğer bağlantı ayarları > VPN > ⋮ > VPN profili ekle.",
		"三星手机：设置 > 连接 > 更多连接设置 > VPN > ⋮ > 添加 VPN 配置文件。"},
	"On an AmneziaWG tunnel, use the AmneziaWG app instead: the same steps, the same file.": {
		"Для туннеля AmneziaWG используйте приложение AmneziaWG: те же шаги, тот же файл.",
		"AmneziaWG tünelinde bunun yerine AmneziaWG uygulamasını kullanın: aynı adımlar, aynı dosya.",
		"如果是 AmneziaWG 隧道，请改用 AmneziaWG 应用：步骤相同，文件相同。"},
	"One device per address: two devices behind the same router or the same mobile carrier address cannot both use L2TP/IPsec at once; the second ends the first. A router carries the whole home on one connection.": {
		"Одно устройство на адрес: два устройства за одним роутером или одним адресом мобильного оператора не могут одновременно использовать L2TP/IPsec — второе отключает первое. Роутер подключает весь дом одним соединением.",
		"Adres başına bir cihaz: aynı yönlendiricinin ya da aynı mobil operatör adresinin arkasındaki iki cihaz aynı anda L2TP/IPsec kullanamaz; ikincisi birincisini düşürür. Bir yönlendirici bütün evi tek bağlantıyla taşır.",
		"每个地址一台设备：同一路由器或同一移动运营商地址后的两台设备不能同时使用 L2TP/IPsec，后连的会断开先连的。路由器可用一个连接承载全家。"},
	"Only in Wireless Router Mode: the phone line is not used, and the Ethernet WAN port goes to another modem's LAN port.": {
		"Только в режиме Wireless Router: телефонная линия не используется, а порт Ethernet WAN подключается к порту LAN другого модема.",
		"Yalnızca Wireless Router Modunda: telefon hattı kullanılmaz ve Ethernet WAN portu başka bir modemin LAN portuna bağlanır.",
		"仅限 Wireless Router 模式：不使用电话线，Ethernet WAN 口接到另一台调制解调器的 LAN 口。"},
	"OpenConnect (Cisco AnyConnect)": {"OpenConnect (Cisco AnyConnect)", "OpenConnect (Cisco AnyConnect)", "OpenConnect (Cisco AnyConnect)"},
	"Other ADSL / VDSL modems (TD-W8961N, Utel and others)": {
		"Другие модемы ADSL / VDSL (TD-W8961N, Utel и другие)",
		"Diğer ADSL / VDSL modemler (TD-W8961N, Utel ve diğerleri)",
		"其他 ADSL / VDSL 调制解调器（TD-W8961N、Utel 等）"},
	"PPTP (weak encryption)": {"PPTP (слабое шифрование)", "PPTP (zayıf şifreleme)", "PPTP（弱加密）"},
	"PPTP is accepted only with MS-CHAPv2 and 128-bit MPPE: choose those wherever the page offers them.": {
		"PPTP принимается только с MS-CHAPv2 и 128-битным MPPE: выбирайте их везде, где страница это предлагает.",
		"PPTP yalnızca MS-CHAPv2 ve 128 bit MPPE ile kabul edilir: sayfa nerede sunuyorsa bunları seçin.",
		"PPTP 仅接受 MS-CHAPv2 和 128 位 MPPE：页面中凡有这些选项都请选择它们。"},
	"Paste the commands; then route what should go through it to the wui-wg interface.": {
		"Вставьте команды; затем направьте на интерфейс wui-wg то, что должно идти через него.",
		"Komutları yapıştırın; sonra içinden geçmesi gerekeni wui-wg arayüzüne yönlendirin.",
		"粘贴这些命令；然后把需要经过它的流量路由到 wui-wg 接口。"},
	"Phones and computers":             {"Телефоны и компьютеры", "Telefonlar ve bilgisayarlar", "手机和电脑"},
	"Routers and modems":               {"Роутеры и модемы", "Yönlendiriciler ve modemler", "路由器和调制解调器"},
	"Save, then turn the service on.":  {"Сохраните, затем включите службу.", "Kaydedin, sonra hizmeti açın.", "保存，然后开启该服务。"},
	"Search for your device or router": {"Найдите своё устройство или роутер", "Cihazınızı ya da yönlendiricinizi arayın", "搜索您的设备或路由器"},
	"Set up a device":                  {"Настройка устройства", "Cihaz kurulumu", "设置设备"},
	"TF-i60 E1 has no VPN settings.":   {"У TF-i60 E1 нет настроек VPN.", "TF-i60 E1'de VPN ayarı yoktur.", "TF-i60 E1 没有 VPN 设置。"},
	"The router's WAN (Internet) port goes to a LAN port of the modem; it reaches the server through the modem.": {
		"Порт WAN (Internet) роутера подключается к порту LAN модема; к серверу роутер выходит через модем.",
		"Yönlendiricinin WAN (Internet) portu modemin bir LAN portuna bağlanır; sunucuya modem üzerinden ulaşır.",
		"路由器的 WAN（Internet）口接到调制解调器的 LAN 口；路由器通过调制解调器连接服务器。"},
	"The username and password are asked for when it connects.":   {"Имя пользователя и пароль запрашиваются при подключении.", "Kullanıcı adı ve parola bağlanırken sorulur.", "连接时会询问用户名和密码。"},
	"Then VPN and Remote Access >> Connection Management > Dial.": {"Затем VPN and Remote Access >> Connection Management > Dial.", "Sonra VPN and Remote Access >> Connection Management > Dial.", "然后 VPN and Remote Access >> Connection Management > Dial。"},
	"Then click Activate.": {"Затем нажмите Activate.", "Sonra Activate'e tıklayın.", "然后点击 Activate。"},
	"This connection is not encrypted, or only with a cipher long since broken. Use it only where nothing else will do.": {
		"Это подключение не шифруется или шифруется давно взломанным шифром. Используйте его, только если ничего другого не подходит.",
		"Bu bağlantı şifrelenmez ya da çoktan kırılmış bir şifreyle şifrelenir. Yalnızca başka hiçbir şey işe yaramıyorsa kullanın.",
		"此连接不加密，或仅使用早已被破解的加密。仅在别无选择时使用。"},
	"This device can dial none of the connections on your plan. It can dial:": {
		"Это устройство не поддерживает ни одно подключение вашего тарифа. Оно поддерживает:",
		"Bu cihaz planınızdaki bağlantıların hiçbirini kuramaz. Kurabildikleri:",
		"此设备无法使用您套餐中的任何连接方式。它支持："},
	"This model has no VPN client of its own. Put one of the routers in this list behind it, or set up each device itself.": {
		"У этой модели нет собственного VPN-клиента. Поставьте за ней один из роутеров из этого списка или настройте каждое устройство отдельно.",
		"Bu modelin kendi VPN istemcisi yoktur. Arkasına bu listedeki yönlendiricilerden birini koyun ya da her cihazı ayrı ayrı kurun.",
		"该型号没有自带 VPN 客户端。请在它后面接一台本列表中的路由器，或在每台设备上分别设置。"},
	"This page takes a key of at most 32 characters.": {"Эта страница принимает ключ не длиннее 32 символов.", "Bu sayfa en fazla 32 karakterlik anahtar kabul eder.", "此页面接受的密钥最多 32 个字符。"},
	"Turn VPN Client on, then under Device List add the devices that should go through it.": {
		"Включите VPN Client, затем в Device List добавьте устройства, которые должны идти через него.",
		"VPN Client'ı açın, sonra Device List altında içinden geçmesi gereken cihazları ekleyin.",
		"开启 VPN Client，然后在 Device List 中添加需要经过它的设备。"},
	"Under All Settings, leave Authentication protocol on AUTO and set Encryption protocol to No encryption: the server does not do MPPE on L2TP, and a router that requires it is refused.": {
		"В All Settings оставьте Authentication protocol на AUTO, а Encryption protocol установите в No encryption: сервер не использует MPPE на L2TP, и роутер, который его требует, получает отказ.",
		"All Settings altında Authentication protocol'ü AUTO'da bırakın ve Encryption protocol'ü No encryption yapın: sunucu L2TP'de MPPE kullanmaz ve bunu zorunlu tutan yönlendirici reddedilir.",
		"在 All Settings 中，Authentication protocol 保持 AUTO，Encryption protocol 设为 No encryption：服务器在 L2TP 上不使用 MPPE，强制要求它的路由器会被拒绝。"},
	"Under Options, turn on Send all traffic over VPN connection, or only the tunnel's own network goes through it.": {
		"В разделе «Параметры» включите «Отправлять весь трафик через VPN», иначе через него пойдёт только сеть самого туннеля.",
		"Seçenekler altında Tüm trafiği VPN bağlantısı üzerinden gönder'i açın, yoksa yalnızca tünelin kendi ağı içinden geçer.",
		"在“选项”中开启“通过 VPN 连接发送所有流量”，否则只有隧道自身的网络会经过它。"},
	"WireGuard":   {"WireGuard", "WireGuard", "WireGuard"},
	"Your device": {"Ваше устройство", "Cihazınız", "您的设备"},
	"ZTE documents no VPN client for these models, only VPN pass-through.": {
		"ZTE не указывает для этих моделей VPN-клиента, только пропуск VPN (pass-through).",
		"ZTE bu modeller için VPN istemcisi belirtmez, yalnızca VPN geçişi (pass-through).",
		"中兴的文档中，这些型号没有 VPN 客户端，只有 VPN 穿透。"},
	"any name":    {"любое имя", "herhangi bir ad", "任意名称"},
	"leave empty": {"оставьте пустым", "boş bırakın", "留空"},
	"{n} found":   {"найдено: {n}", "{n} bulundu", "找到 {n} 个"},
}

// inLang is t in lang: English and Persian from the text itself, the
// others from more, and English for a name with no translation.
func inLang(t Text, lang string) string {
	switch lang {
	case "en":
		return t.EN
	case "fa":
		if t.FA != "" {
			return t.FA
		}
		return t.EN
	}
	i, known := map[string]int{"ru": 0, "tr": 1, "zh": 2}[lang]
	if m, ok := more[t.EN]; known && ok && m[i] != "" {
		return m[i]
	}
	return t.EN
}
