package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every language has every English string, and each says it with the same
// placeholders and markup. A {n} lost in translation shows the operator a
// sentence with a hole in it; one renamed shows them the braces; markup
// changed could break the page. Russian, Turkish and Chinese had 276 of
// 2,413 strings until they were translated whole; this keeps them whole.
func TestEveryLanguageSaysEverythingEnglishDoes(t *testing.T) {
	read := func(locale string) map[string]string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("locales", locale+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", locale, err)
		}
		return m
	}
	ph := regexp.MustCompile(`\{[a-zA-Z0-9_]+\}`)
	tag := regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	distinct := func(re *regexp.Regexp, s string, dropN bool) string {
		seen := map[string]bool{}
		for _, m := range re.FindAllString(s, -1) {
			if dropN && m == "{n}" {
				continue
			}
			seen[m] = true
		}
		out := make([]string, 0, len(seen))
		for m := range seen {
			out = append(out, m)
		}
		sort.Strings(out)
		return strings.Join(out, " ")
	}
	en := read("en")
	for _, locale := range []string{"fa", "ru", "tr", "zh"} {
		m := read(locale)
		for key, src := range en {
			got, ok := m[key]
			if !ok {
				t.Errorf("%s: %q is missing", locale, key)
				continue
			}
			if strings.TrimSpace(got) == "" {
				t.Errorf("%s: %q is empty", locale, key)
				continue
			}
			// "{n} customer" and "one customer" both read right for one.
			one := strings.HasSuffix(key, ".one")
			if a, b := distinct(ph, src, one), distinct(ph, got, one); a != b {
				t.Errorf("%s: %q has placeholders [%s], English has [%s]", locale, key, b, a)
			}
			if a, b := tag.FindAllString(src, -1), tag.FindAllString(got, -1); strings.Join(a, "") != strings.Join(b, "") {
				t.Errorf("%s: %q has markup %v, English has %v", locale, key, b, a)
			}
		}
	}
}
