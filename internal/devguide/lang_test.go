package devguide

import "testing"

// Every text the guides show in English is shown in every language: a
// text written in Persian too is words, not a name, and must have its
// Russian, Turkish and Chinese here. A name the same in English and
// Persian -- a device, a brand -- is shown as it is.
func TestEveryTextIsInEveryLanguage(t *testing.T) {
	s := Strings()
	for _, lang := range Langs {
		if len(s[lang]) != len(s["en"]) {
			t.Fatalf("%s has %d words, English %d", lang, len(s[lang]), len(s["en"]))
		}
	}
	for key, en := range s["en"] {
		if s["fa"][key] == en {
			continue
		}
		m, ok := more[en]
		if !ok {
			t.Errorf("%s has no Russian, Turkish or Chinese: %q", key, en)
			continue
		}
		for i, lang := range []string{"ru", "tr", "zh"} {
			if m[i] == "" {
				t.Errorf("%s has no %s: %q", key, lang, en)
			}
			if s[lang][key] != m[i] {
				t.Errorf("%s in %s is %q, want %q", key, lang, s[lang][key], m[i])
			}
		}
	}
}

// A translation whose English is no longer in the guides is left over from
// a text that changed, and the changed text is then untranslated.
func TestNoTranslationIsLeftOver(t *testing.T) {
	used := map[string]bool{}
	for _, en := range Strings()["en"] {
		used[en] = true
	}
	for en := range more {
		if !used[en] {
			t.Errorf("a translation for text the guides no longer have: %q", en)
		}
	}
}
