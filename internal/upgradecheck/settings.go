package upgradecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

// Every setting a fixture's panel can store is stored, and with a value that
// is not its default -- so an update that renames a key, drops one, or reads
// it under another name and falls back to the default is caught: the value
// the operator chose would come back as the default.
//
// The settings live behind several pages: the settings page itself, the
// basic routing switches, the engine, the node authority and the
// subscription service (set up in SetUpSubscription). Each is read back after
// it is saved, and that answer -- what the panel says it now holds -- is what
// a later panel must give back.
//
// A few fields keep their default on purpose, each because a different value
// would change how the test reaches the panel rather than test it: the URL
// path, the domain the panel answers to, its own certificate, and the outbound
// its own traffic leaves through (which must name an outbound that exists).
// They are still stored -- the settings page writes every key on every save --
// so their keys are still checked.

// panelSettings are the non-default values for the settings page, by the
// field name the page sends. A release that lacks a field is not sent it.
var panelSettings = map[string]any{
	"trustedProxyCIDRs":           "10.0.0.0/8",
	"sessionMaxAge":               1440,
	"defaultLocale":               "fa",
	"pageSize":                    50,
	"timeLocation":                "UTC",
	"datepicker":                  "jalalian",
	"expireDiff":                  5,
	"trafficDiff":                 3,
	"externalTrafficInformURI":    "https://inform.example.test/traffic",
	"defaultQuotaBytes":           21474836480,
	"defaultExpiryDays":           45,
	"defaultDeviceLimit":          3,
	"defaultRateBitsPerSec":       50000000,
	"defaultResetCycle":           "monthly",
	"notifyChatId":                "424242",
	"notifyKinds":                 []string{"login", "expired", "backup"},
	"notifyBotToken":              "123456:fixture-bot-token",
	"notifyLang":                  "fa",
	"notifyAPIServer":             "https://tg.example.test",
	"notifyRunTime":               "@weekly",
	"notifyBackup":                true,
	"notifyBackupTime":            "0 0 4 * * *",
	"notifyCPUThreshold":          70,
	"notifyMemoryThreshold":       75,
	"notifyOutboundDownThreshold": 40,
	"backupEveryHours":            12,
	"backupKeep":                  5,
	"mailHost":                    "smtp.example.test",
	"mailPort":                    2525,
	"mailUsername":                "fixture",
	"mailPassword":                "fixture-mail-pass",
	"mailFrom":                    "panel@example.test",
	"mailFromName":                "W-UI fixture",
	"mailTo":                      "ops@example.test",
	"mailEncryption":              "tls",
	"mailKinds":                   []string{"login"},
}

// engineSettings and routingSettings are the same for the engine page and the
// basic routing switches.
var engineSettings = map[string]any{
	"collectInterval":  3,
	"outboundCounters": true,
	"onlineWindow":     300,
	"logLevel":         "warn",
	"accessLog":        true,
	"maskAddress":      true,
	"probeInterval":    600,
	"probeConcurrency": true,
	"outboundTestUrl":  "https://probe.example.test/generate_204",
}

var routingSettings = map[string]any{
	"blockBitTorrent": true,
	"blockIps":        []string{"192.0.2.0/24"},
	"blockDomains":    []string{"blocked.example.test"},
	"blockPorts":      []string{"25"},
	"directIps":       []string{"198.51.100.0/24"},
	"directDomains":   []string{"direct.example.test"},
	"ipv4Domains":     []string{"v4.example.test"},
	"failClosed":      true,
}

// The pages a fixture's settings are read back from.
const (
	PageSettings = "settings"
	PageEngine   = "engine"
	PageRouting  = "routing"
	PageNodeCA   = "nodeAuthority"
)

// SeedSettings saves every settings page with non-default values and returns
// what each then says it holds. The address, port and path the panel is
// reached on are written back as the page gives them -- the ones it runs on
// -- so they are stored without moving the panel away from the test.
// defaultInterface is a tunnel that exists, for the default a new customer is
// put on.
func SeedSettings(ctx context.Context, c *Client, defaultInterface uint) (map[string]any, error) {
	pages := map[string]any{}

	// The settings page: what it holds, with every field it has changed.
	var got struct {
		Settings map[string]any `json:"settings"`
	}
	if err := c.Do(ctx, http.MethodGet, "api/settings", nil, &got); err != nil {
		return nil, err
	}
	in := got.Settings
	want := map[string]any{"defaultInterfaceId": defaultInterface}
	for k, v := range panelSettings {
		want[k] = v
	}
	for k, v := range want {
		if _, ok := in[k]; ok {
			in[k] = v
		}
	}
	if err := c.Do(ctx, http.MethodPut, "api/settings", in, nil); err != nil {
		return nil, fmt.Errorf("save the settings page: %w", err)
	}
	if err := c.Do(ctx, http.MethodGet, "api/settings", nil, &got); err != nil {
		return nil, err
	}
	pages[PageSettings] = got.Settings

	// The engine: answered as {"settings": {...}} today, as the settings
	// alone by a release that had no defaults to send beside them.
	var engine map[string]any
	if err := c.Do(ctx, http.MethodGet, "api/engine", nil, &engine); err != nil {
		return nil, err
	}
	eng := engine
	if s, ok := engine["settings"].(map[string]any); ok {
		eng = s
	}
	for k, v := range engineSettings {
		if _, ok := eng[k]; ok {
			eng[k] = v
		}
	}
	if err := c.Do(ctx, http.MethodPut, "api/engine", eng, nil); err != nil {
		return nil, fmt.Errorf("save the engine: %w", err)
	}
	if err := c.Do(ctx, http.MethodGet, "api/engine", nil, &engine); err != nil {
		return nil, err
	}
	if s, ok := engine["settings"].(map[string]any); ok {
		engine = s
	}
	pages[PageEngine] = engine

	// The basic routing switches.
	var routing struct {
		Basic map[string]any `json:"basic"`
	}
	if err := c.Do(ctx, http.MethodGet, "api/routing", nil, &routing); err != nil {
		return nil, err
	}
	for k, v := range routingSettings {
		if _, ok := routing.Basic[k]; ok {
			routing.Basic[k] = v
		}
	}
	if err := c.Do(ctx, http.MethodPut, "api/routing", routing.Basic, nil); err != nil {
		return nil, fmt.Errorf("save the routing switches: %w", err)
	}
	if err := c.Do(ctx, http.MethodGet, "api/routing", nil, &routing); err != nil {
		return nil, err
	}
	pages[PageRouting] = routing.Basic

	// The node authority, minted on first asking, and trusted as the one a
	// managing panel must present.
	var ca struct {
		CACert string `json:"caCert"`
	}
	if err := c.Do(ctx, http.MethodGet, "api/nodes/mtls/authority", nil, &ca); err != nil {
		return nil, err
	}
	if err := c.Do(ctx, http.MethodPost, "api/nodes/mtls/trust", map[string]string{"caCert": ca.CACert}, nil); err != nil {
		return nil, fmt.Errorf("trust the node authority: %w", err)
	}
	pages[PageNodeCA] = ca.CACert
	return pages, nil
}

// ReadPages reads back the pages SeedSettings saved.
func ReadPages(ctx context.Context, c *Client) (map[string]any, error) {
	pages := map[string]any{}
	var s struct {
		Settings map[string]any `json:"settings"`
	}
	if err := c.Do(ctx, http.MethodGet, "api/settings", nil, &s); err != nil {
		return nil, err
	}
	pages[PageSettings] = s.Settings
	var engine map[string]any
	if err := c.Do(ctx, http.MethodGet, "api/engine", nil, &engine); err != nil {
		return nil, err
	}
	if inner, ok := engine["settings"].(map[string]any); ok {
		engine = inner
	}
	pages[PageEngine] = engine
	var routing struct {
		Basic map[string]any `json:"basic"`
	}
	if err := c.Do(ctx, http.MethodGet, "api/routing", nil, &routing); err != nil {
		return nil, err
	}
	pages[PageRouting] = routing.Basic
	var ca struct {
		CACert string `json:"caCert"`
	}
	if err := c.Do(ctx, http.MethodGet, "api/nodes/mtls/authority", nil, &ca); err != nil {
		return nil, err
	}
	pages[PageNodeCA] = ca.CACert
	return pages, nil
}

// pagesDiff lists every value an earlier panel's page held that this panel's
// page does not give back the same. A field this panel added is not
// compared; one it lost is a difference.
func pagesDiff(was, is map[string]any) []string {
	var out []string
	for page, w := range was {
		i, ok := is[page]
		if !ok {
			out = append(out, page+": not read back")
			continue
		}
		wm, wok := w.(map[string]any)
		im, iok := i.(map[string]any)
		if !wok || !iok {
			if !reflect.DeepEqual(normalize(w), normalize(i)) {
				out = append(out, fmt.Sprintf("%s was %.60v, is %.60v", page, w, i))
			}
			continue
		}
		for k, wv := range wm {
			iv, ok := im[k]
			switch {
			case !ok:
				out = append(out, fmt.Sprintf("%s.%s is gone", page, k))
			case !reflect.DeepEqual(normalize(wv), normalize(iv)):
				out = append(out, fmt.Sprintf("%s.%s was %v, is %v", page, k, wv, iv))
			}
		}
	}
	sort.Strings(out)
	return out
}

// normalize makes two JSON values comparable whatever way they were decoded:
// numbers as their text, lists and objects element by element.
func normalize(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	_ = d.Decode(&out)
	return out
}

// accessFields are the settings page's fields for how the panel is reached.
// A restore keeps the receiving panel's own (backup.panelAccessSettings): they
// describe that server, not the one the backup came from.
var accessFields = []string{"webListen", "webPort", "webBasePath", "webDomain", "webCertFile", "webKeyFile", "trustedProxyCIDRs"}

// withoutAccess is pages with the settings page's access fields left out.
func withoutAccess(pages map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range pages {
		out[k] = v
	}
	if s, ok := pages[PageSettings].(map[string]any); ok {
		kept := map[string]any{}
		for k, v := range s {
			kept[k] = v
		}
		for _, f := range accessFields {
			delete(kept, f)
		}
		out[PageSettings] = kept
	}
	return out
}
