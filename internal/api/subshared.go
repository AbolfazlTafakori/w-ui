package api

import (
	"encoding/json"
	"html/template"
	"sort"
	"strconv"
	"strings"
)

// What a customer's page writes once and uses in many places.
//
// Every file on the page has its own setup card, and every card held every
// device's notes, the device list and a guide for each device, written out
// whole: the same steps over and over with a file's own values in them --
// 50 KB a file, 560 KB for a customer of ten. A part is now written once, as
// a <template>, and each place it is used is an empty stub naming it, with
// its own values when it has any. The page's script gives each stub a copy
// of its part before anything else runs, so everything after it finds the
// page as it was.
//
// A value goes where its slot is, @@0@@, @@1@@ and on, in an element's text
// or an attribute, put there as text: a value is never markup.

// subShared is a page's parts, each kept once however often it is used.
// A part is kept as what it is written from, and written once, by the page's
// own template: nothing is rendered before the page is, so a part that cannot
// be written fails the page the way anything else in it does.
type subShared struct {
	ids   map[string]int
	Parts []subPart
}

// subPart is one part, as the page writes it: <template id="part-ID">, from
// the template named Kind with Data.
type subPart struct {
	ID   int
	Kind string
	Data any
}

// put keeps a part to be written from template kind with data, and is its
// id: the same kind with the same data is the same part.
func (s *subShared) put(kind string, data any) int {
	if s.ids == nil {
		s.ids = map[string]int{}
	}
	key, err := json.Marshal(data)
	if err != nil {
		// Unshared rather than wrongly shared: a part with a key of its own,
		// which no JSON can be.
		key = []byte("unkeyed:" + strconv.Itoa(len(s.Parts)))
	}
	k := kind + "|" + string(key)
	if id, ok := s.ids[k]; ok {
		return id
	}
	id := len(s.Parts)
	s.ids[k] = id
	s.Parts = append(s.Parts, subPart{ID: id, Kind: kind, Data: data})
	return id
}

// subSlot is the mark of slot n in a part.
func subSlot(n int) string { return "@@" + strconv.Itoa(n) + "@@" }

// subSlots is a stub's values, in the order of their slots.
func subSlots(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	b, _ := json.Marshal(vals)
	return string(b)
}

// subIconSprite is every icon drawn once, for the page to point at: an icon
// is used on every row of every guide -- the copy button -- and was 500 bytes
// each time.
func subIconSprite() (template.HTML, map[string]template.HTML) {
	names := make([]string, 0, len(antIconPaths))
	for name := range antIconPaths {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	icons := make(map[string]template.HTML, len(names))
	b.WriteString(`<svg class="icons" aria-hidden="true">`)
	for _, name := range names {
		b.WriteString(`<symbol id="i-` + name + `" viewBox="64 64 896 896">`)
		for _, d := range antIconPaths[name] {
			b.WriteString(`<path d="` + d + `"/>`)
		}
		b.WriteString(`</symbol>`)
		icons[name] = template.HTML(`<svg width="1em" height="1em" fill="currentColor" focusable="false" aria-hidden="true"><use href="#i-` + name + `"/></svg>`)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()), icons
}

// The sprite is the same on every page: drawn once.
var subSprite, subIcons = subIconSprite()
