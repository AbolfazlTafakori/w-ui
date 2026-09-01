// Package i18n resolves user-facing strings.
//
// English is the source locale: every key exists in en.json, and any other
// locale is an overlay that may be incomplete. A missing translation falls back
// to English rather than showing the raw key, so a partially translated build
// stays usable.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

// DefaultLocale is the source locale and the fallback for every other one.
const DefaultLocale = "en"

// Direction reports the writing direction of a locale, which the UI needs to
// set on the document root.
type Direction string

const (
	LTR Direction = "ltr"
	RTL Direction = "rtl"
)

var rtlLocales = map[string]bool{"fa": true, "ar": true, "he": true, "ur": true}

// DirectionOf returns the writing direction for a locale tag.
func DirectionOf(locale string) Direction {
	if rtlLocales[strings.ToLower(locale)] {
		return RTL
	}
	return LTR
}

// Catalog holds every loaded locale.
type Catalog struct {
	mu      sync.RWMutex
	locales map[string]map[string]string
}

// Load reads the embedded locale files.
func Load() (*Catalog, error) {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		return nil, fmt.Errorf("i18n: read locales: %w", err)
	}

	c := &Catalog{locales: map[string]map[string]string{}}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("i18n: read %s: %w", e.Name(), err)
		}
		var msgs map[string]string
		if err := json.Unmarshal(raw, &msgs); err != nil {
			return nil, fmt.Errorf("i18n: parse %s: %w", e.Name(), err)
		}
		c.locales[strings.TrimSuffix(e.Name(), ".json")] = msgs
	}

	if _, ok := c.locales[DefaultLocale]; !ok {
		return nil, fmt.Errorf("i18n: source locale %q is missing", DefaultLocale)
	}
	return c, nil
}

// T resolves key in locale, falling back to English and finally to the key.
func (c *Catalog) T(locale, key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if msgs, ok := c.locales[locale]; ok {
		if v, ok := msgs[key]; ok && v != "" {
			return v
		}
	}
	if v, ok := c.locales[DefaultLocale][key]; ok {
		return v
	}
	return key
}

// Locales lists the loaded locale tags, sorted.
func (c *Catalog) Locales() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]string, 0, len(c.locales))
	for l := range c.locales {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Messages returns a locale's messages merged over English, which is what the
// frontend loads so it never has to handle a missing key.
func (c *Catalog) Messages(locale string) map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]string, len(c.locales[DefaultLocale]))
	for k, v := range c.locales[DefaultLocale] {
		out[k] = v
	}
	for k, v := range c.locales[locale] {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// Missing lists keys a locale has not translated yet, sorted. It backs a build
// check so drift is visible rather than silent.
func (c *Catalog) Missing(locale string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var out []string
	for k := range c.locales[DefaultLocale] {
		if v, ok := c.locales[locale][k]; !ok || v == "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
