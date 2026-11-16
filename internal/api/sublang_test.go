package api

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/devguide"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/service"
)

// The page speaks exactly the panel's languages: a language added to the
// panel fails here until the page has it too.
func TestThePageSpeaksEveryLanguageThePanelDoes(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	panel := append([]string(nil), cat.Locales()...)
	var page []string
	for _, l := range subLangs {
		page = append(page, l.Code)
	}
	guides := append([]string(nil), devguide.Langs...)
	sort.Strings(panel)
	sort.Strings(page)
	sort.Strings(guides)
	if strings.Join(panel, ",") != strings.Join(page, ",") {
		t.Fatalf("the panel speaks %v and the page %v", panel, page)
	}
	if strings.Join(page, ",") != strings.Join(guides, ",") {
		t.Fatalf("the page speaks %v and its device guides %v", page, guides)
	}
}

// Every word the page has in English it has in every language: a word
// missing in one would leave the last language's word on screen.
func TestEveryLanguageHasEveryWord(t *testing.T) {
	for _, l2tp := range []bool{false, true} {
		for _, setup := range []bool{false, true} {
			dict := pageStrings(l2tp, setup)
			for _, lang := range subLangs {
				words := dict[lang.Code]
				if words == nil {
					t.Fatalf("no words in %s", lang.Code)
				}
				for k, en := range dict["en"] {
					v, ok := words[k]
					if !ok || strings.TrimSpace(v) == "" {
						t.Errorf("%s has no %q (%q)", lang.Code, k, en)
					}
				}
			}
		}
	}
}

// Every data-i a page carries is a word the page has, on pages of every
// kind: a plan shared or not, a user's own, files of every protocol.
func TestEveryLabelOnThePageIsAWord(t *testing.T) {
	dict := pageStrings(true, true)["en"]
	re := regexp.MustCompile(`data-i(?:-title|-ph)?="([A-Za-z0-9]+)"`)
	pages := []*service.SubPage{
		{Name: "A", Status: "active"},
		{Name: "B", Status: "active", QuotaBytes: 1 << 30, PlanUsers: 3, OwnUser: 1, SubURL: "https://h/s/x"},
		{Name: "C", Status: "active", PlanUsers: 2, SubURL: "https://h/s/y",
			Users: []service.SubPageUser{{User: 1, Token: "a", Link: "https://h/s/a"}, {User: 2, Token: "b", Link: "https://h/s/b"}}},
		{Name: "D", Status: "active", Devices: []service.SubPageDevice{
			{ID: 1, Protocol: "wireguard", Tunnel: "wg0", Name: "device-1", Config: "[Interface]", Filename: "d.conf"},
			{ID: 2, Protocol: "openvpn", Tunnel: "ovpn0", Name: "device-1", Config: "client", Filename: "d.ovpn"},
			{ID: 3, Protocol: "l2tp", Tunnel: "l2tp0", Name: "device-1", Config: "Server: x", Username: "u", Filename: "d.txt"},
		}},
	}
	for _, p := range pages {
		var buf strings.Builder
		if err := subPageTemplate.Execute(&buf, newSubView(p, "token", false, "")); err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(buf.String(), -1) {
			if _, ok := dict[m[1]]; !ok {
				t.Errorf("the page labels something %q, which no language has", m[1])
			}
		}
	}
}

func TestThePageOpensInTheBrowsersLanguage(t *testing.T) {
	for accept, want := range map[string]string{
		"":                                 "en",
		"ru-RU,ru;q=0.9,en-US;q=0.8":       "ru",
		"tr":                               "tr",
		"zh-CN,zh;q=0.9":                   "zh",
		"zh-TW":                            "zh",
		"fa-IR,fa;q=0.9,en;q=0.5":          "fa",
		"de-DE,de;q=0.9,fr;q=0.8":          "en",
		"de-DE,de;q=0.9,tr;q=0.4,ru;q=0.7": "ru",
		"en;q=0.1,fa;q=0.8":                "fa",
		"ru;q=0":                           "en",
		"ru;q=abc":                         "ru",
		"*":                                "en",
	} {
		if got := pickSubLang(accept); got != want {
			t.Errorf("Accept-Language %q opened in %q, want %q", accept, got, want)
		}
	}
	long := strings.Repeat("xx,", 10000) + "ru"
	if got := pickSubLang(long); got != "en" {
		t.Errorf("an endless header was read to its end: %q", got)
	}
}

func TestTheLanguageMenuOffersEveryLanguage(t *testing.T) {
	v := newSubView(&service.SubPage{Name: "Ali", Status: "active", Locale: "zh"}, "token", false, "")
	if v.Lang != "zh" {
		t.Fatalf("the page opened in %q", v.Lang)
	}
	var buf strings.Builder
	if err := subPageTemplate.Execute(&buf, v); err != nil {
		t.Fatal(err)
	}
	for _, l := range subLangs {
		if !strings.Contains(buf.String(), `data-lang="`+l.Code+`"`) {
			t.Errorf("the menu has no %s", l.Code)
		}
	}
	if v := newSubView(&service.SubPage{Name: "Ali", Status: "active", Locale: "xx"}, "t", false, ""); v.Lang != "en" {
		t.Fatalf("an unknown language opened as %q", v.Lang)
	}
}

// A plan shared by several users marks its total and remaining as shared,
// with what that means on hover; a user's own page also has a row with
// what they spent; a plan for one is as it was.
func TestASharedPlanSaysItsAllowanceIsEveryones(t *testing.T) {
	render := func(p *service.SubPage) string {
		var buf strings.Builder
		if err := subPageTemplate.Execute(&buf, newSubView(p, "token", false, "")); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	base := func() *service.SubPage {
		return &service.SubPage{Name: "Family", Status: "active", QuotaBytes: 50 << 30, UsedBytes: 30 << 30}
	}

	plan := base()
	plan.PlanUsers = 3
	html := render(plan)
	for _, want := range []string{`data-i-title="sharedNote"`, `data-i="sharedTag"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the plan's page has no %s", want)
		}
	}
	if strings.Contains(html, `id="lv-own"`) {
		t.Error("the plan's page shows one user's usage")
	}

	mine := base()
	mine.PlanUsers, mine.OwnUser, mine.OwnUsedBytes = 3, 2, 10<<30
	html = render(mine)
	for _, want := range []string{`data-i-title="sharedNoteUser"`, `data-i="yourUsage"`, `id="lv-own">10.00 GiB<`, `20.00 GiB`} {
		if !strings.Contains(html, want) {
			t.Errorf("a user's page has no %s", want)
		}
	}

	one := base()
	one.PlanUsers = 1
	html = render(one)
	if strings.Contains(html, "sharedTag") || strings.Contains(html, "yourUsage") {
		t.Error("a plan for one says it is shared")
	}
}
