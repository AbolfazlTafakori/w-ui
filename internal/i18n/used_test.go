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

// Every label the panel's pages ask for exists in every language. A missing
// one is not an error anywhere else: the page shows the key itself --
// "set.externalTrafficInformEnable" where a setting's name should be -- and
// nobody notices until an operator does.
func TestEveryLabelThePagesUseExists(t *testing.T) {
	src := filepath.Join("..", "..", "web", "src")
	if _, err := os.Stat(src); err != nil {
		t.Skip("web sources not checked out")
	}
	// t('key') and tn('key', n); tn falls back from key.one / key.other to
	// key itself, so either is enough for it.
	plain := regexp.MustCompile(`\bt\(\s*'([a-zA-Z][a-zA-Z0-9_.]*)'`)
	plural := regexp.MustCompile(`\btn\(\s*'([a-zA-Z][a-zA-Z0-9_.]*)'`)
	used := map[string]string{} // key -> a file that uses it
	pluralUsed := map[string]string{}
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !(strings.HasSuffix(path, ".vue") || strings.HasSuffix(path, ".js")) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range plain.FindAllStringSubmatch(string(raw), -1) {
			used[m[1]] = filepath.Base(path)
		}
		for _, m := range plural.FindAllStringSubmatch(string(raw), -1) {
			pluralUsed[m[1]] = filepath.Base(path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(used) < 100 {
		t.Fatalf("only %d labels found in %s; the pattern no longer matches how the pages ask", len(used), src)
	}

	for _, locale := range []string{"en", "fa"} {
		raw, err := os.ReadFile(filepath.Join("locales", locale+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var messages map[string]string
		if err := json.Unmarshal(raw, &messages); err != nil {
			t.Fatal(err)
		}
		var missing []string
		for key, file := range used {
			if _, ok := messages[key]; !ok {
				missing = append(missing, key+" ("+file+")")
			}
		}
		for key, file := range pluralUsed {
			_, whole := messages[key]
			_, one := messages[key+".one"]
			_, other := messages[key+".other"]
			if !whole && !(one && other) {
				missing = append(missing, key+".one/.other ("+file+")")
			}
		}
		sort.Strings(missing)
		for _, m := range missing {
			t.Errorf("%s.json has no %s", locale, m)
		}
	}
}
